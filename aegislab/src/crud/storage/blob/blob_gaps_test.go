package blob

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
)

// ---- helpers ----

func newLocalFSHarness(t *testing.T) (*LocalFSDriver, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := BucketConfig{Name: "local", Driver: "localfs", Root: dir}
	drv, err := NewLocalFSDriver(cfg, []byte("test-signing-key"))
	if err != nil {
		t.Fatalf("NewLocalFSDriver: %v", err)
	}
	return drv, func() { _ = os.RemoveAll(dir) }
}

func newServiceHarness(t *testing.T, drv Driver, driverName string) (*Service, *Repository) {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := NewRepository(db)
	// PublicRead=true so handler tests work without a real JWT user.
	cfg := BucketConfig{Name: "local", Driver: driverName, PublicRead: true}
	reg := NewTestRegistry(map[string]*Bucket{
		"local": {Config: cfg, Driver: drv},
	})
	svc := NewService(reg, repo, NewClock())
	return svc, repo
}

// seed writes content under key and inserts a metadata row.
func seed(t *testing.T, ctx context.Context, drv Driver, repo *Repository, bucket, key, content string) {
	t.Helper()
	_, err := drv.Put(ctx, key, strings.NewReader(content), PutOpts{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("seed put %q: %v", key, err)
	}
	size := int64(len(content))
	rec := &ObjectRecord{Bucket: bucket, StorageKey: key, SizeBytes: size, ContentType: "text/plain"}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("seed create %q: %v", key, err)
	}
}

// ---- G2: Copy (localfs) ----

func TestLocalFSDriver_Copy_NestedKeys(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	ctx := context.Background()

	src := "dir/sub/file.txt"
	dst := "other/deep/copy.txt"
	payload := "hello-copy"

	if _, err := drv.Put(ctx, src, strings.NewReader(payload), PutOpts{}); err != nil {
		t.Fatalf("Put src: %v", err)
	}
	meta, err := drv.Copy(ctx, src, dst)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if meta.Key != dst {
		t.Fatalf("meta.Key: got %q want %q", meta.Key, dst)
	}
	rc, _, err := drv.Get(ctx, dst)
	if err != nil {
		t.Fatalf("Get dst: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != payload {
		t.Fatalf("body mismatch: got %q want %q", got, payload)
	}
}

func TestLocalFSDriver_Copy_SrcMissing(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	_, err := drv.Copy(context.Background(), "missing/key.txt", "dst/key.txt")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}

// ---- G2: BatchDelete ----

func TestService_BatchDelete_MixedKeys(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	svc, repo := newServiceHarness(t, drv, "localfs")
	ctx := context.Background()

	seed(t, ctx, drv, repo, "local", "a/1.txt", "aaa")
	seed(t, ctx, drv, repo, "local", "b/2.txt", "bbb")

	res, err := svc.BatchDelete(ctx, "local", []string{
		"a/1.txt",
		"b/2.txt",
		"nonexistent/x.txt",
	})
	if err != nil {
		t.Fatalf("BatchDelete: %v", err)
	}
	if len(res.Deleted) != 3 {
		t.Fatalf("deleted count: got %d want 3 (%v)", len(res.Deleted), res.Deleted)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %v", res.Failed)
	}
}

func TestService_BatchDelete_CapExceeded(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	svc, _ := newServiceHarness(t, drv, "localfs")

	keys := make([]string, batchDeleteCap+1)
	for i := range keys {
		keys[i] = "k"
	}
	_, err := svc.BatchDelete(context.Background(), "local", keys)
	if err == nil || !strings.Contains(err.Error(), "too many keys") {
		t.Fatalf("expected cap error, got %v", err)
	}
}

// ---- G3: Zip endpoint ----

func newHandlerHarness(t *testing.T, drv Driver, driverName string) (*Handler, *Repository) {
	t.Helper()
	svc, repo := newServiceHarness(t, drv, driverName)
	auth := NewAuthorizer()
	h := NewHandler(svc, auth, RegistryDeps{SigningKey: []byte("test-key")})
	return h, repo
}

func zipRequest(t *testing.T, h *Handler, bucket string, keys []string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	body := `{"keys":[`
	for i, k := range keys {
		if i > 0 {
			body += ","
		}
		body += `"` + k + `"`
	}
	body += `]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "bucket", Value: bucket}}
	h.ZipObjects(c)
	return w
}

func TestHandler_ZipObjects_ThreeKeys(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	h, repo := newHandlerHarness(t, drv, "localfs")
	ctx := context.Background()

	keys := []string{"a/1.txt", "b/2.txt", "c/3.txt"}
	for _, k := range keys {
		seed(t, ctx, drv, repo, "local", k, "content-"+k)
	}

	w := zipRequest(t, h, "local", keys)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type: %q", ct)
	}

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if len(zr.File) != len(keys) {
		t.Fatalf("zip entries: got %d want %d", len(zr.File), len(keys))
	}
	gotNames := map[string]bool{}
	for _, f := range zr.File {
		gotNames[f.Name] = true
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		want := "content-" + f.Name
		if string(data) != want {
			t.Fatalf("zip entry %q: got %q want %q", f.Name, data, want)
		}
	}
	for _, k := range keys {
		if !gotNames[k] {
			t.Fatalf("zip missing key %q", k)
		}
	}
}

func TestHandler_ZipObjects_CapExceeded(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	h, _ := newHandlerHarness(t, drv, "localfs")

	keys := make([]string, zipKeyCap+1)
	for i := range keys {
		keys[i] = "k"
	}
	w := zipRequest(t, h, "local", keys)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- G1: Nested-key routing ----

func TestHandler_NestedKey_GetHeadDelete(t *testing.T) {
	drv, done := newLocalFSHarness(t)
	defer done()
	h, repo := newHandlerHarness(t, drv, "localfs")
	ctx := context.Background()

	key := "a/b/c.txt"
	seed(t, ctx, drv, repo, "local", key, "nested-content")

	gin.SetMode(gin.TestMode)

	// GET
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Params = gin.Params{
			{Key: "bucket", Value: "local"},
			{Key: "key", Value: "/" + key}, // Gin sets leading "/" for *key params
		}
		h.InlineGet(c)
		if w.Code != http.StatusOK {
			t.Fatalf("GET status: %d; %s", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != "nested-content" {
			t.Fatalf("GET body: %q", got)
		}
	}

	// HEAD
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodHead, "/", nil)
		c.Params = gin.Params{
			{Key: "bucket", Value: "local"},
			{Key: "key", Value: "/" + key},
		}
		h.Stat(c)
		if w.Code != http.StatusOK {
			t.Fatalf("HEAD status: %d", w.Code)
		}
	}

	// DELETE
	{
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
		c.Params = gin.Params{
			{Key: "bucket", Value: "local"},
			{Key: "key", Value: "/" + key},
		}
		h.Delete(c)
		if w.Code != http.StatusNoContent {
			t.Fatalf("DELETE status: %d; %s", w.Code, w.Body.String())
		}
		// verify gone
		if _, err := os.Stat(filepath.Join(drv.cfg.Root, key)); !os.IsNotExist(err) {
			t.Fatalf("file should be deleted")
		}
	}
}
