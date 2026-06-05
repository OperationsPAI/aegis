//go:build duckdb_arrow

package duckdbquery

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/url"
	"strings"

	"aegis/platform/consts"

	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/duckdb/duckdb-go/v2"
	"github.com/sirupsen/logrus"
)

// newConnector returns a duckdb connector whose init callback loads the
// httpfs extension and registers the percentile macros on every new
// connection (both are per-connection in DuckDB). It sets NO S3 secret:
// only the presigned URLs the caller hands to read_parquet() resolve.
func newConnector() (*duckdb.Connector, error) {
	return duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		stmts := []string{
			"INSTALL httpfs",
			"LOAD httpfs",
		}
		stmts = append(stmts, percentileMacros...)
		for _, stmt := range stmts {
			if _, err := execer.ExecContext(context.Background(), stmt, nil); err != nil {
				return fmt.Errorf("duckdb init %q: %w", stmt, err)
			}
		}
		return nil
	})
}

// percentileMacros expose p50/p90/p95/p99 as quantile_cont aliases so
// agent SQL can call them directly. CREATE OR REPLACE keeps the init
// idempotent across reconnects.
var percentileMacros = []string{
	"CREATE OR REPLACE MACRO p50(x) AS quantile_cont(x, 0.50)",
	"CREATE OR REPLACE MACRO p90(x) AS quantile_cont(x, 0.90)",
	"CREATE OR REPLACE MACRO p95(x) AS quantile_cont(x, 0.95)",
	"CREATE OR REPLACE MACRO p99(x) AS quantile_cont(x, 0.99)",
}

// isLocalURL reports whether a Source URL reads from the local
// filesystem rather than a remote (https/s3/http) endpoint. Local
// sources get the allowed_directories + lock_configuration fence.
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	switch strings.ToLower(u.Scheme) {
	case "", "file":
		return true
	default:
		return false
	}
}

func allLocal(sources []Source) bool {
	for _, s := range sources {
		if !isLocalURL(s.URL) {
			return false
		}
	}
	return true
}

// registerViews creates one VIEW per Source on conn. read_parquet runs
// lazily, so the URLs are not dereferenced until the user query runs.
func registerViews(ctx context.Context, execer driver.ExecerContext, sources []Source) error {
	for _, s := range sources {
		if s.View == "" {
			return fmt.Errorf("%w: source has empty view name", consts.ErrBadRequest)
		}
		stmt := fmt.Sprintf(
			"CREATE OR REPLACE VIEW %s AS SELECT * FROM read_parquet(%s)",
			quoteIdent(s.View), quoteLiteral(s.URL),
		)
		if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
			return fmt.Errorf("register view %s: %w", s.View, err)
		}
	}
	return nil
}

// applyFence sets the per-session resource limits and — for all-local
// source sets — fences read_parquet against allowed_directories then
// locks the configuration so the user query cannot widen it. The fence
// is applied AFTER the views are registered: views are lazy
// read_parquet(url) calls and enable_external_access must stay on for
// presigned URLs, so we rely on the no-S3-secret posture + the SQL
// denylist for remote sources and the directory fence for local ones.
func applyFence(ctx context.Context, execer driver.ExecerContext, sources []Source, lim Limits) error {
	settings := []string{
		fmt.Sprintf("SET memory_limit=%s", quoteLiteral(lim.memoryLimit())),
		fmt.Sprintf("SET threads=%d", lim.threads()),
	}
	if allLocal(sources) && len(lim.AllowedDirectories) > 0 {
		quoted := make([]string, len(lim.AllowedDirectories))
		for i, d := range lim.AllowedDirectories {
			quoted[i] = quoteLiteral(d)
		}
		settings = append(settings,
			fmt.Sprintf("SET allowed_directories=[%s]", strings.Join(quoted, ", ")),
			"SET enable_external_access=false",
		)
	}
	// lock_configuration must be last — once set, further SET fails.
	settings = append(settings, "SET lock_configuration=true")
	for _, stmt := range settings {
		if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
			return fmt.Errorf("apply fence %q: %w", stmt, err)
		}
	}
	return nil
}

// setupConn opens one connection, registers the views, and applies the
// fence. The same connection is used for the query: go-duckdb tears the
// in-memory DB down with the last open conn, so a second connection
// opened after this one is released cannot see the views.
func setupConn(ctx context.Context, connector *duckdb.Connector, sources []Source, lim Limits) (driver.Conn, error) {
	conn, err := connector.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("duckdb connect: %w", err)
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("duckdb conn does not implement ExecerContext")
	}
	if err := registerViews(ctx, execer, sources); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := applyFence(ctx, execer, sources, lim); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func query(ctx context.Context, sources []Source, userSQL string, lim Limits) (io.ReadCloser, error) {
	cleanSQL, err := validateSQL(userSQL)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("%w: no sources to query", consts.ErrBadRequest)
	}
	connector, err := newConnector()
	if err != nil {
		return nil, err
	}
	conn, err := setupConn(ctx, connector, sources, lim)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
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

		writer := ipc.NewWriter(pw, ipc.WithSchema(rdr.Schema()))
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

func schema(ctx context.Context, sources []Source, lim Limits) ([]Table, error) {
	if len(sources) == 0 {
		return []Table{}, nil
	}
	connector, err := newConnector()
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()

	out := make([]Table, 0, len(sources))
	for _, s := range sources {
		cols, err := describeColumns(ctx, db, s.URL)
		if err != nil {
			logrus.WithError(err).Warnf("duckdbquery: describe %s skipped", s.View)
			continue
		}
		var rows int64 = -1
		if err := db.QueryRowContext(ctx,
			fmt.Sprintf("SELECT count(*) FROM read_parquet(%s)", quoteLiteral(s.URL)),
		).Scan(&rows); err != nil {
			logrus.WithError(err).Warnf("duckdbquery: count %s failed", s.View)
			rows = -1
		}
		out = append(out, Table{Table: s.View, RowCount: rows, Columns: cols})
	}
	return out, nil
}

func describeColumns(ctx context.Context, db *sql.DB, parquetURL string) ([]Column, error) {
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet(%s)", quoteLiteral(parquetURL)),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", consts.ErrNotFound, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Column
	for rows.Next() {
		var name, typ string
		var null, key, def, extra sql.NullString
		if err := rows.Scan(&name, &typ, &null, &key, &def, &extra); err != nil {
			return nil, err
		}
		out = append(out, Column{Name: name, Type: typ})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// queryRows runs userSQL via database/sql and scans each row into a
// JSON-friendly map. Used by the Accept: application/json path so the
// caller does not have to decode Arrow IPC client-side.
func queryRows(ctx context.Context, sources []Source, userSQL string, lim Limits) (int64, []map[string]any, error) {
	cleanSQL, err := validateSQL(userSQL)
	if err != nil {
		return 0, nil, err
	}
	if len(sources) == 0 {
		return 0, nil, fmt.Errorf("%w: no sources to query", consts.ErrBadRequest)
	}
	connector, err := newConnector()
	if err != nil {
		return 0, nil, err
	}
	conn, err := setupConn(ctx, connector, sources, lim)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = conn.Close() }()

	queryer, ok := conn.(driver.QueryerContext)
	if !ok {
		return 0, nil, fmt.Errorf("duckdb conn does not implement QueryerContext")
	}
	rows, err := queryer.QueryContext(ctx, cleanSQL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", consts.ErrBadRequest, err)
	}
	defer func() { _ = rows.Close() }()

	cols := rows.Columns()
	out := make([]map[string]any, 0)
	var count int64
	dest := make([]driver.Value, len(cols))
	for {
		if err := rows.Next(dest); err != nil {
			if err == io.EOF {
				break
			}
			return 0, nil, fmt.Errorf("scan row: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, name := range cols {
			row[name] = normalizeValue(dest[i])
		}
		out = append(out, row)
		count++
	}
	return count, out, nil
}

// normalizeValue coerces a driver.Value into something json.Marshal
// renders cleanly. []byte becomes a string (DuckDB hands back BLOB/JSON
// as bytes); everything else passes through.
func normalizeValue(v driver.Value) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}
