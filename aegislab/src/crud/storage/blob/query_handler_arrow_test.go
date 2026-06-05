//go:build duckdb_arrow

package blob

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"aegis/platform/testutil"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/gin-gonic/gin"
)

// newQueryHarness wires a Handler over an S3-driver-backed registry
// pointed at a mock S3 server. The mock serves real parquet bytes so the
// DuckDB httpfs path resolves the presigned URLs end-to-end.
func newQueryHarness(t *testing.T, cfg BucketConfig) (*Handler, *mockS3) {
	t.Helper()
	mock := newMockS3()
	srv := httptest.NewServer(mock.handler(t))
	t.Cleanup(srv.Close)

	cfg.Driver = "s3"
	cfg.Endpoint = srv.URL
	cfg.Region = "us-east-1"
	cfg.AccessKey = "minioadmin"
	cfg.SecretKey = "minioadmin"
	if cfg.Bucket == "" {
		cfg.Bucket = cfg.Name
	}
	cfg.PathStyle = true
	drv, err := NewS3Driver(cfg)
	if err != nil {
		t.Fatalf("NewS3Driver: %v", err)
	}
	reg := NewTestRegistry(map[string]*Bucket{cfg.Name: {Config: cfg, Driver: drv}})
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	svc := NewService(reg, NewRepository(db), NewClock())
	h := NewHandler(svc, NewAuthorizer(), RegistryDeps{SigningKey: []byte("test-key")})
	return h, mock
}

// seedParquet materializes a parquet via DuckDB COPY and stores it as a
// mock-S3 object under key.
func seedParquet(t *testing.T, mock *mockS3, key, selectExpr string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmp.parquet")
	connector, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(),
		"COPY ("+selectExpr+") TO '"+path+"' (FORMAT PARQUET)"); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read parquet: %v", err)
	}
	mock.mu.Lock()
	mock.objects[key] = data
	mock.cts[key] = "application/parquet"
	mock.mu.Unlock()
}

func newQueryRequest(t *testing.T, method, accept string, body any) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	c.Request = httptest.NewRequest(method, "/", rdr)
	c.Request.Header.Set("Content-Type", "application/json")
	if accept != "" {
		c.Request.Header.Set("Accept", accept)
	}
	return w, c
}

func TestQueryBucket_JSONRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newQueryHarness(t, BucketConfig{Name: "data", PublicRead: true})
	seedParquet(t, mock, "metrics/a.parquet",
		"SELECT * FROM (VALUES (1,10.0),(2,20.0),(3,30.0)) AS t(id,latency)")

	w, c := newQueryRequest(t, http.MethodPost, "application/json", map[string]any{
		"prefix": "metrics/",
		"sql":    "SELECT id, latency FROM a ORDER BY id",
	})
	c.Params = gin.Params{{Key: "bucket", Value: "data"}}
	setUserCtx(c, 1, false, nil)

	h.QueryBucket(c)

	if w.Code != http.StatusOK {
		t.Fatalf("QueryBucket: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data queryRowsResp `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.Data.RowCount != 3 {
		t.Fatalf("row_count: got %d want 3 (body=%s)", resp.Data.RowCount, w.Body.String())
	}
}

func TestQueryBucket_Percentiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newQueryHarness(t, BucketConfig{Name: "data", PublicRead: true})
	seedParquet(t, mock, "x.parquet",
		"SELECT * FROM (VALUES (10.0),(20.0),(30.0),(40.0)) AS t(v)")

	w, c := newQueryRequest(t, http.MethodPost, "application/json", map[string]any{
		"keys": []string{"x.parquet"},
		"sql":  "SELECT p50(v) AS p50, p95(v) AS p95 FROM x",
	})
	c.Params = gin.Params{{Key: "bucket", Value: "data"}}
	setUserCtx(c, 1, false, nil)

	h.QueryBucket(c)
	if w.Code != http.StatusOK {
		t.Fatalf("percentile query: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestQueryBucket_RejectsWriteSQL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newQueryHarness(t, BucketConfig{Name: "data", PublicRead: true})

	w, c := newQueryRequest(t, http.MethodPost, "application/json", map[string]any{
		"prefix": "metrics/",
		"sql":    "DELETE FROM a",
	})
	c.Params = gin.Params{{Key: "bucket", Value: "data"}}
	setUserCtx(c, 1, false, nil)

	h.QueryBucket(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("write SQL: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestQueryBucket_ForbiddenWithoutReadRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newQueryHarness(t, BucketConfig{Name: "restricted", ReadRoles: []string{"admin-role"}})

	w, c := newQueryRequest(t, http.MethodPost, "application/json", map[string]any{
		"prefix": "metrics/",
		"sql":    "SELECT 1",
	})
	c.Params = gin.Params{{Key: "bucket", Value: "restricted"}}
	setUserCtx(c, 42, false, nil)

	h.QueryBucket(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no read role: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestQueryBucket_NoParquetObjects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newQueryHarness(t, BucketConfig{Name: "data", PublicRead: true})

	w, c := newQueryRequest(t, http.MethodPost, "application/json", map[string]any{
		"prefix": "empty/",
		"sql":    "SELECT 1",
	})
	c.Params = gin.Params{{Key: "bucket", Value: "data"}}
	setUserCtx(c, 1, false, nil)

	h.QueryBucket(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("no parquet: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestQueryBucket_SchemaDiscovery is the single-endpoint schema-discovery
// path: the agent's list_tables runs an information_schema.columns query
// through POST /query over the per-request views (no separate /schema
// endpoint). It must return the registered view's columns.
func TestQueryBucket_SchemaDiscovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newQueryHarness(t, BucketConfig{Name: "data", PublicRead: true})
	seedParquet(t, mock, "metrics/a.parquet",
		"SELECT * FROM (VALUES (1,10.0),(2,20.0)) AS t(id,latency)")

	w, c := newQueryRequest(t, http.MethodPost, "application/json", map[string]any{
		"prefix": "metrics/",
		"sql":    "SELECT table_name, column_name, data_type FROM information_schema.columns ORDER BY table_name, ordinal_position",
	})
	c.Params = gin.Params{{Key: "bucket", Value: "data"}}
	setUserCtx(c, 1, false, nil)

	h.QueryBucket(c)
	if w.Code != http.StatusOK {
		t.Fatalf("schema discovery: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data queryRowsResp `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	// The view "a" exposes columns id + latency.
	if resp.Data.RowCount != 2 {
		t.Fatalf("row_count: got %d want 2 (body=%s)", resp.Data.RowCount, w.Body.String())
	}
	cols := map[string]bool{}
	table := ""
	for _, r := range resp.Data.Rows {
		if tn, ok := r["table_name"].(string); ok {
			table = tn
		}
		if cn, ok := r["column_name"].(string); ok {
			cols[cn] = true
		}
	}
	if table != "a" {
		t.Fatalf("table_name: got %q want a", table)
	}
	if !cols["id"] || !cols["latency"] {
		t.Fatalf("expected columns id+latency, got %v", cols)
	}
}
