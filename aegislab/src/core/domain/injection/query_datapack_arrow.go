//go:build duckdb_arrow

package injection

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aegis/platform/consts"

	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/duckdb/duckdb-go/v2"
	"github.com/sirupsen/logrus"
)

func (s *Service) queryDatapackFileContent(ctx context.Context, id int, filePath string) (string, int64, io.ReadCloser, error) {
	injection, err := s.getReadyDatapack(id)
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

	connector, err := newDuckDBConnector()
	if err != nil {
		return "", 0, nil, err
	}

	countConn, err := connector.Connect(ctx)
	if err != nil {
		logrus.Errorf("connect failed: %v", err)
		return "", 0, nil, err
	}
	defer func() { _ = countConn.Close() }()

	var totalRows int64
	countQuery := fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", fullPath)

	db := sql.OpenDB(connector)
	if err := db.QueryRowContext(ctx, countQuery).Scan(&totalRows); err != nil {
		return "", 0, nil, err
	}

	safeSQL, err := buildSafeParquetSQL(ctx, db, fullPath)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to build safe parquet SQL: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()

		conn, err := connector.Connect(ctx)
		if err != nil {
			logrus.Errorf("connect failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		arrow, err := duckdb.NewArrowFromConn(conn)
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("failed to get arrow interface: %w", err))
			return
		}

		rdr, err := arrow.QueryContext(ctx, safeSQL)
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("query context failed: %w", err))
			return
		}
		defer rdr.Release()

		writer := ipc.NewWriter(pw, ipc.WithSchema(rdr.Schema()), ipc.WithZstd())
		defer func() { _ = writer.Close() }()

		for rdr.Next() {
			record := rdr.RecordBatch()
			if err := writer.Write(record); err != nil {
				record.Release()
				_ = pw.CloseWithError(fmt.Errorf("failed to write arrow record: %w", err))
				return
			}
			record.Release()
		}

		if err := rdr.Err(); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("reader error: %w", err))
		}
	}()

	return filepath.Base(fullPath), totalRows, pr, nil
}

func buildSafeParquetSQL(ctx context.Context, db *sql.DB, filePath string) (string, error) {
	fallbackSQL := fmt.Sprintf("SELECT * FROM read_parquet('%s')", filePath)
	describeQuery := fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet('%s')", filePath)
	rows, err := db.QueryContext(ctx, describeQuery)
	if err != nil {
		logrus.Warnf("failed to describe parquet file, falling back to SELECT *: %v", err)
		return fallbackSQL, nil
	}
	defer func() { _ = rows.Close() }()

	var columns []string
	for rows.Next() {
		var colName, colType string
		var null, key, def, extra sql.NullString
		if err := rows.Scan(&colName, &colType, &null, &key, &def, &extra); err != nil {
			return "", err
		}
		_ = null
		_ = key
		_ = def
		_ = extra

		quotedName := fmt.Sprintf("\"%s\"", strings.ReplaceAll(colName, "\"", "\"\""))
		normalized := strings.ToUpper(strings.TrimSpace(colType))

		switch normalized {
		case "UINT64", "UHUGEINT":
			logrus.Infof("parquet column %q: casting %s -> BIGINT", colName, colType)
			columns = append(columns, fmt.Sprintf("CAST(%s AS BIGINT) AS %s", quotedName, quotedName))
		default:
			columns = append(columns, quotedName)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return fallbackSQL, nil
	}

	return fmt.Sprintf("SELECT %s FROM read_parquet('%s')", strings.Join(columns, ", "), filePath), nil
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
		view := sanitizeViewName(strings.TrimSuffix(base, filepath.Ext(base)))
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

// newDuckDBConnector returns a duckdb connector whose init callback
// loads the httpfs extension on every new connection. DuckDB extensions
// are per-connection, so this must run before any read_parquet() against
// an HTTPS URL (the S3-backed presigned URLs we hand out).
func newDuckDBConnector() (*duckdb.Connector, error) {
	return duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		for _, stmt := range []string{
			"INSTALL httpfs",
			"LOAD httpfs",
		} {
			if _, err := execer.ExecContext(context.Background(), stmt, nil); err != nil {
				return fmt.Errorf("duckdb init %s: %w", stmt, err)
			}
		}
		return nil
	})
}

var viewNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func sanitizeViewName(raw string) string {
	cleaned := viewNameSanitizer.ReplaceAllString(raw, "_")
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		return ""
	}
	if cleaned[0] >= '0' && cleaned[0] <= '9' {
		cleaned = "_" + cleaned
	}
	return cleaned
}

func (s *Service) getDatapackSchema(ctx context.Context, id int) (*DatapackSchemaResp, error) {
	injection, err := s.getReadyDatapack(id)
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

	connector, err := newDuckDBConnector()
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()

	tables := make([]DatapackTableSchema, 0, len(parquets))
	for _, p := range parquets {
		cols, err := describeParquetColumns(ctx, db, p.path)
		if err != nil {
			// One missing/corrupted parquet shouldn't blow up the whole listing.
			logrus.WithError(err).Warnf("describe %s skipped", p.file)
			continue
		}
		var rows int64 = -1
		if err := db.QueryRowContext(ctx,
			fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", p.path),
		).Scan(&rows); err != nil {
			logrus.WithError(err).Warnf("count %s failed, leaving rows=-1", p.file)
			rows = -1
		}
		tables = append(tables, DatapackTableSchema{
			Name:    p.view,
			File:    p.file,
			Rows:    rows,
			Columns: cols,
		})
	}
	return &DatapackSchemaResp{Tables: tables}, nil
}

func describeParquetColumns(ctx context.Context, db *sql.DB, parquetPath string) ([]DatapackColumnSchema, error) {
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet('%s')", parquetPath),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", consts.ErrNotFound, err)
	}
	defer func() { _ = rows.Close() }()

	var out []DatapackColumnSchema
	for rows.Next() {
		var name, typ string
		var null, key, def, extra sql.NullString
		if err := rows.Scan(&name, &typ, &null, &key, &def, &extra); err != nil {
			return nil, err
		}
		out = append(out, DatapackColumnSchema{Name: name, Type: typ})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// validateDatapackSQL applies a conservative whitelist: only SELECT / WITH
// queries, no semicolons (single trailing semicolon allowed), no extension
// loading, no DDL/DML, no raw file-reader functions. The user is expected to
// query the pre-registered VIEWs.
func validateDatapackSQL(raw string) (string, error) {
	stripped := stripSQLComments(raw)
	stripped = strings.TrimSpace(stripped)
	stripped = strings.TrimRight(stripped, ";")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return "", fmt.Errorf("%w: SQL is empty", consts.ErrBadRequest)
	}
	if strings.Contains(stripped, ";") {
		return "", fmt.Errorf("%w: multi-statement SQL is not allowed", consts.ErrBadRequest)
	}
	lowered := strings.ToLower(stripped)
	first := strings.Fields(lowered)[0]
	if first != "select" && first != "with" {
		return "", fmt.Errorf("%w: only SELECT / WITH queries are allowed", consts.ErrBadRequest)
	}
	for _, word := range sqlBlacklist {
		if wordRegex(word).MatchString(lowered) {
			return "", fmt.Errorf("%w: keyword %q is not allowed", consts.ErrBadRequest, word)
		}
	}
	return stripped, nil
}

var sqlBlacklist = []string{
	"attach", "copy", "pragma", "install", "load", "call",
	"insert", "update", "delete", "create", "drop", "alter", "truncate",
	"export", "import", "set", "reset", "begin", "commit", "rollback",
	"read_parquet", "read_csv", "read_json", "read_ndjson", "read_text",
	"read_blob", "glob", "system", "shell_exec",
}

func wordRegex(word string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`)
}

var (
	lineCommentRe  = regexp.MustCompile(`--[^\n]*`)
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func stripSQLComments(in string) string {
	out := lineCommentRe.ReplaceAllString(in, " ")
	out = blockCommentRe.ReplaceAllString(out, " ")
	return out
}

func (s *Service) runDatapackQuery(ctx context.Context, id int, userSQL string) (io.ReadCloser, error) {
	cleanSQL, err := validateDatapackSQL(userSQL)
	if err != nil {
		return nil, err
	}
	injection, err := s.getReadyDatapack(id)
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

	connector, err := newDuckDBConnector()
	if err != nil {
		return nil, err
	}

	// Register VIEWs (httpfs is loaded by the connector init for every conn).
	setupDB := sql.OpenDB(connector)
	for _, p := range parquets {
		stmt := fmt.Sprintf(
			"CREATE OR REPLACE VIEW %s AS SELECT * FROM read_parquet('%s')",
			quoteIdent(p.view), p.path,
		)
		if _, err := setupDB.ExecContext(ctx, stmt); err != nil {
			_ = setupDB.Close()
			return nil, fmt.Errorf("failed to register view %s: %w", p.view, err)
		}
	}
	_ = setupDB.Close()

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()

		conn, err := connector.Connect(ctx)
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("connect failed: %w", err))
			return
		}
		defer func() { _ = conn.Close() }()

		arrow, err := duckdb.NewArrowFromConn(conn)
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("failed to get arrow interface: %w", err))
			return
		}

		rdr, err := arrow.QueryContext(ctx, cleanSQL)
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("%w: %v", consts.ErrBadRequest, err))
			return
		}
		defer rdr.Release()

		writer := ipc.NewWriter(pw, ipc.WithSchema(rdr.Schema()), ipc.WithZstd())
		defer func() { _ = writer.Close() }()

		for rdr.Next() {
			record := rdr.RecordBatch()
			if err := writer.Write(record); err != nil {
				record.Release()
				_ = pw.CloseWithError(fmt.Errorf("failed to write arrow record: %w", err))
				return
			}
			record.Release()
		}
		if err := rdr.Err(); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("reader error: %w", err))
		}
	}()

	return pr, nil
}

func quoteIdent(name string) string {
	return fmt.Sprintf("\"%s\"", strings.ReplaceAll(name, "\"", "\"\""))
}
