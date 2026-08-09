//go:build duckdb_arrow

package injection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/duckdbquery"

	"github.com/sirupsen/logrus"
)

// datapackQueryLimits is the per-session resource fence for datapack
// queries. Presigned-URL sources are remote, so allowed_directories is
// not set; the no-S3-secret posture + SQL denylist confine remote reads.
var datapackQueryLimits = duckdbquery.Limits{
	MemoryLimit: "2GB",
	Threads:     2,
}

func (s *Service) queryDatapackFileContent(ctx context.Context, scope authz.CallerScope, id int, filePath string) (string, int64, io.ReadCloser, error) {
	injection, err := s.getReadyDatapack(scope, id)
	if err != nil {
		return "", 0, nil, err
	}
	if filepath.Ext(filePath) != ".parquet" {
		return "", 0, nil, fmt.Errorf("file is not a parquet file: %s", filePath)
	}
	fullPath, err := s.store.ParquetReaderPath(ctx, injection.Name, filePath, 15*time.Minute)
	if err != nil {
		return "", 0, nil, fmt.Errorf("resolve parquet: %w", err)
	}

	view := duckdbquery.SanitizeViewName(strings.TrimSuffix(filepath.Base(fullPath), filepath.Ext(fullPath)))
	if view == "" {
		view = "data"
	}
	src := duckdbquery.Source{View: view, URL: fullPath}

	tables, err := duckdbquery.Schema(ctx, []duckdbquery.Source{src}, datapackQueryLimits)
	if err != nil {
		return "", 0, nil, err
	}
	var totalRows int64 = -1
	if len(tables) > 0 {
		totalRows = tables[0].RowCount
	}

	// Cast wide-unsigned columns to BIGINT so the Arrow stream stays
	// portable; query the registered VIEW, not read_parquet (denied).
	selectSQL := castSafeSelect(view, tables)
	reader, err := duckdbquery.Query(ctx, []duckdbquery.Source{src}, selectSQL, datapackQueryLimits)
	if err != nil {
		return "", 0, nil, err
	}
	return filepath.Base(fullPath), totalRows, reader, nil
}

// castSafeSelect builds a projection over the VIEW that casts UINT64 /
// UHUGEINT columns to BIGINT. Falls back to SELECT * when no schema is
// available.
func castSafeSelect(view string, tables []duckdbquery.Table) string {
	if len(tables) == 0 || len(tables[0].Columns) == 0 {
		return "SELECT * FROM " + quoteIdent(view)
	}
	cols := make([]string, 0, len(tables[0].Columns))
	for _, c := range tables[0].Columns {
		quoted := quoteIdent(c.Name)
		switch strings.ToUpper(strings.TrimSpace(c.Type)) {
		case "UINT64", "UHUGEINT":
			cols = append(cols, fmt.Sprintf("CAST(%s AS BIGINT) AS %s", quoted, quoted))
		default:
			cols = append(cols, quoted)
		}
	}
	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), quoteIdent(view))
}

func quoteIdent(name string) string {
	return fmt.Sprintf("\"%s\"", strings.ReplaceAll(name, "\"", "\"\""))
}

// datapackParquet binds a logical VIEW name to the parquet file it reads.
type datapackParquet struct {
	view string // VIEW name (filename without .parquet, sanitized)
	file string // Relative path inside the datapack (for the response)
	path string // Resolved path passed verbatim to read_parquet()
}

// listDatapackParquets enumerates parquet files that actually exist in the
// datapack root and resolves each to a DuckDB-readable path (filesystem
// path for local stores, presigned HTTPS URL for S3-backed stores).
// Missing files are silently skipped — that is data, not error.
func (s *Service) listDatapackParquets(ctx context.Context, injectionName string) ([]datapackParquet, error) {
	tree, err := s.store.BuildFileTree(injectionName, "", 0)
	if err != nil {
		return nil, err
	}
	var out []datapackParquet
	for _, item := range tree.Files {
		if len(item.Children) > 0 {
			continue
		}
		base := filepath.Base(item.Path)
		if !strings.HasSuffix(strings.ToLower(base), ".parquet") {
			continue
		}
		view := duckdbquery.SanitizeViewName(strings.TrimSuffix(base, filepath.Ext(base)))
		if view == "" {
			continue
		}
		// 15-minute TTL covers schema describe + count(*) + several
		// follow-up user queries against the same VIEW in this connection.
		resolved, err := s.store.ParquetReaderPath(ctx, injectionName, item.Path, 15*time.Minute)
		if err != nil {
			// Existence was just confirmed by BuildFileTree; a resolve failure
			// here is genuinely abnormal and worth surfacing.
			logrus.WithError(err).Warnf("resolve %s/%s failed", injectionName, item.Path)
			continue
		}
		out = append(out, datapackParquet{view: view, file: item.Path, path: resolved})
	}
	return out, nil
}

func parquetSources(parquets []datapackParquet) []duckdbquery.Source {
	out := make([]duckdbquery.Source, 0, len(parquets))
	for _, p := range parquets {
		out = append(out, duckdbquery.Source{View: p.view, URL: p.path})
	}
	return out
}

func (s *Service) getDatapackSchema(ctx context.Context, scope authz.CallerScope, id int) (*DatapackSchemaResp, error) {
	injection, err := s.getReadyDatapack(scope, id)
	if err != nil {
		// Not-ready datapack is a normal pre-data state for the SQL editor;
		// return an empty schema instead of bubbling 404 so the page can
		// show "no tables yet" rather than an error.
		if errors.Is(err, consts.ErrNotFound) {
			return &DatapackSchemaResp{Tables: []DatapackTableSchema{}}, nil
		}
		return nil, err
	}
	parquets, err := s.listDatapackParquets(ctx, injection.Name)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return &DatapackSchemaResp{Tables: []DatapackTableSchema{}}, nil
		}
		return nil, err
	}
	if len(parquets) == 0 {
		return &DatapackSchemaResp{Tables: []DatapackTableSchema{}}, nil
	}

	libTables, err := duckdbquery.Schema(ctx, parquetSources(parquets), datapackQueryLimits)
	if err != nil {
		return nil, err
	}
	// duckdbquery.Schema keys by VIEW name; re-attach the source file
	// path the API contract exposes alongside each table.
	fileByView := make(map[string]string, len(parquets))
	for _, p := range parquets {
		fileByView[p.view] = p.file
	}
	tables := make([]DatapackTableSchema, 0, len(libTables))
	for _, lt := range libTables {
		cols := make([]DatapackColumnSchema, 0, len(lt.Columns))
		for _, c := range lt.Columns {
			cols = append(cols, DatapackColumnSchema{Name: c.Name, Type: c.Type})
		}
		tables = append(tables, DatapackTableSchema{
			Name:    lt.Table,
			File:    fileByView[lt.Table],
			Rows:    lt.RowCount,
			Columns: cols,
		})
	}
	return &DatapackSchemaResp{Tables: tables}, nil
}

func (s *Service) runDatapackQuery(ctx context.Context, scope authz.CallerScope, id int, userSQL string) (io.ReadCloser, error) {
	// Reject bad SQL before touching the datapack so an invalid query
	// surfaces as 400 regardless of datapack readiness (preserves the
	// pre-refactor ordering).
	if _, err := duckdbquery.ValidateSQL(userSQL); err != nil {
		return nil, err
	}
	injection, err := s.getReadyDatapack(scope, id)
	if err != nil {
		return nil, err
	}
	parquets, err := s.listDatapackParquets(ctx, injection.Name)
	if err != nil {
		return nil, err
	}
	if len(parquets) == 0 {
		return nil, fmt.Errorf("%w: datapack has no parquet files", consts.ErrNotFound)
	}
	return duckdbquery.Query(ctx, parquetSources(parquets), userSQL, datapackQueryLimits)
}

// countParquetRows resolves the parquet file's reader path and returns
// its row count. Returns (0,false) if any step fails — the caller treats
// nil-rows as "unknown" rather than "zero".
func (s *Service) countParquetRows(ctx context.Context, datapackName, file string) (int64, bool) {
	path, err := s.store.ParquetReaderPath(ctx, datapackName, file, 15*time.Minute)
	if err != nil {
		return 0, false
	}
	view := duckdbquery.SanitizeViewName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if view == "" {
		view = "data"
	}
	tables, err := duckdbquery.Schema(ctx, []duckdbquery.Source{{View: view, URL: path}}, datapackQueryLimits)
	if err != nil || len(tables) == 0 || tables[0].RowCount < 0 {
		return 0, false
	}
	return tables[0].RowCount, true
}
