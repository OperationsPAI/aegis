//go:build duckdb_arrow

package duckdbquery

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"aegis/platform/consts"

	"github.com/apache/arrow-go/v18/arrow/ipc"
)

// writeParquet uses DuckDB's own COPY to materialize a small parquet at
// path. This is test-fixture code — it bypasses the validator on
// purpose so the lib's public surface can be exercised against a real
// file.
func writeParquet(t *testing.T, path, selectExpr string) {
	t.Helper()
	connector, err := newConnector()
	if err != nil {
		t.Fatalf("newConnector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	stmt := "COPY (" + selectExpr + ") TO " + quoteLiteral(path) + " (FORMAT PARQUET)"
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
}

func readIPCRowCount(t *testing.T, r interface{ Read([]byte) (int, error) }) int {
	t.Helper()
	rdr, err := ipc.NewReader(r)
	if err != nil {
		t.Fatalf("ipc.NewReader: %v", err)
	}
	defer rdr.Release()
	var rows int
	for rdr.Next() {
		rec := rdr.RecordBatch()
		rows += int(rec.NumRows())
	}
	if err := rdr.Err(); err != nil {
		t.Fatalf("ipc read: %v", err)
	}
	return rows
}

func metricsSource(t *testing.T, dir string) Source {
	t.Helper()
	path := filepath.Join(dir, "metrics.parquet")
	writeParquet(t, path,
		"SELECT * FROM (VALUES (1, 10.0), (2, 20.0), (3, 30.0)) AS t(id, latency)")
	return Source{View: SanitizeViewName("metrics"), URL: path}
}

func TestValidateSQL_RejectsWrites(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO metrics VALUES (1)",
		"DELETE FROM metrics",
		"CREATE TABLE x AS SELECT 1",
		"DROP VIEW metrics",
		"SELECT * FROM read_parquet('/etc/passwd')",
		"COPY metrics TO '/tmp/x.csv'",
		"SELECT 1; SELECT 2",
		"",
	} {
		if _, err := ValidateSQL(q); err == nil {
			t.Errorf("expected rejection for %q", q)
		} else if !errors.Is(err, consts.ErrBadRequest) {
			t.Errorf("%q: expected ErrBadRequest, got %v", q, err)
		}
	}
}

func TestValidateSQL_AllowsReads(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM metrics",
		"WITH t AS (SELECT 1) SELECT * FROM t",
		"select count(*) from metrics where id > 1",
		"SELECT * FROM metrics;",
	} {
		if _, err := ValidateSQL(q); err != nil {
			t.Errorf("%q: unexpected rejection: %v", q, err)
		}
	}
}

func TestQuery_ReturnsRows(t *testing.T) {
	dir := t.TempDir()
	src := metricsSource(t, dir)

	rc, err := Query(context.Background(), []Source{src},
		"SELECT id, latency FROM metrics ORDER BY id", Limits{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if n := readIPCRowCount(t, rc); n != 3 {
		t.Fatalf("row count: got %d want 3", n)
	}
}

func TestQueryRows_JSON(t *testing.T) {
	dir := t.TempDir()
	src := metricsSource(t, dir)

	count, rows, err := QueryRows(context.Background(), []Source{src},
		"SELECT id, latency FROM metrics ORDER BY id", Limits{})
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}
	if count != 3 || len(rows) != 3 {
		t.Fatalf("count=%d rows=%d want 3", count, len(rows))
	}
	if _, ok := rows[0]["id"]; !ok {
		t.Fatalf("missing id column in %v", rows[0])
	}
}

func TestPercentileMacros(t *testing.T) {
	dir := t.TempDir()
	src := metricsSource(t, dir)

	count, rows, err := QueryRows(context.Background(), []Source{src},
		"SELECT p50(latency) AS p50, p95(latency) AS p95 FROM metrics", Limits{})
	if err != nil {
		t.Fatalf("QueryRows with percentiles: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
	if _, ok := rows[0]["p50"]; !ok {
		t.Fatalf("missing p50 in %v", rows[0])
	}
}

func TestSchema(t *testing.T) {
	dir := t.TempDir()
	src := metricsSource(t, dir)

	tables, err := Schema(context.Background(), []Source{src}, Limits{})
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if tables[0].Table != "metrics" {
		t.Fatalf("table name: got %q", tables[0].Table)
	}
	if tables[0].RowCount != 3 {
		t.Fatalf("row count: got %d want 3", tables[0].RowCount)
	}
	if len(tables[0].Columns) != 2 {
		t.Fatalf("columns: got %d want 2", len(tables[0].Columns))
	}
}

// TestSelfConfine_BlocksUnregisteredLocalFile verifies the
// allowed_directories + lock_configuration fence: a VIEW pointing at a
// parquet OUTSIDE the allowed directory fails to resolve at query time,
// even though the file exists on disk and was registered as a source.
// This proves the fence — not just the SQL denylist — confines local
// reads.
func TestSelfConfine_BlocksUnregisteredLocalFile(t *testing.T) {
	allowedDir := t.TempDir()
	secretDir := t.TempDir()

	allowedSrc := metricsSource(t, allowedDir)
	secretPath := filepath.Join(secretDir, "secret.parquet")
	writeParquet(t, secretPath, "SELECT 42 AS x")
	secretSrc := Source{View: "secret", URL: secretPath}

	// The allowed-dir VIEW resolves under the fence.
	if _, _, err := QueryRows(context.Background(), []Source{allowedSrc},
		"SELECT count(*) AS c FROM metrics",
		Limits{AllowedDirectories: []string{allowedDir}}); err != nil {
		t.Fatalf("allowed-dir query should succeed: %v", err)
	}

	// A VIEW over a file outside the allowed dir must be denied at read
	// time by the fence.
	_, _, err := QueryRows(context.Background(), []Source{secretSrc},
		"SELECT * FROM secret",
		Limits{AllowedDirectories: []string{allowedDir}})
	if err == nil {
		t.Fatalf("query over out-of-dir file should be blocked by the fence")
	}
	if !strings.HasPrefix(secretPath, secretDir) {
		t.Fatalf("test setup invariant")
	}
}
