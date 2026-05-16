package blob

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
)

// deleteBucketHarness wires a localfs driver, a SQLite-backed
// repository, and a DB-aware registry with one bucket seeded both in
// the registry map and in blob_bucket_configs so Drop can find the row.
func deleteBucketHarness(t *testing.T, bucketName string) (*Handler, *Service, Driver) {
	t.Helper()
	drv, _ := newLocalFSHarness(t)

	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}, &BucketConfigRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	cfg := BucketConfig{Name: bucketName, Driver: "localfs", PublicRead: true}
	if err := db.Create(&BucketConfigRecord{Name: bucketName, Driver: "localfs", PublicRead: true}).Error; err != nil {
		t.Fatalf("seed bucket config: %v", err)
	}
	repo := NewRepository(db)
	reg := NewTestRegistryWithDB(map[string]*Bucket{
		bucketName: {Config: cfg, Driver: drv},
	}, db)
	svc := NewService(reg, repo, NewClock())
	auth := NewAuthorizer()
	h := NewHandler(svc, auth, RegistryDeps{SigningKey: []byte("test-key")})
	return h, svc, drv
}

func deleteBucketRequest(t *testing.T, h *Handler, bucket, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	url := "/buckets/" + bucket
	if query != "" {
		url += "?" + query
	}
	c.Request = httptest.NewRequest(http.MethodDelete, url, nil)
	c.Params = gin.Params{{Key: "bucket", Value: bucket}}
	h.DeleteBucket(c)
	return w
}

func TestDeleteBucket_Empty(t *testing.T) {
	h, svc, _ := deleteBucketHarness(t, "empty-bkt")

	w := deleteBucketRequest(t, h, "empty-bkt", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%s", w.Code, w.Body.String())
	}
	if _, err := svc.Registry().Lookup("empty-bkt"); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("registry still has bucket: %v", err)
	}
	// DB row should be gone too.
	var n int64
	if err := svc.repo.DB.Model(&BucketConfigRecord{}).Where("name = ?", "empty-bkt").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows, got %d", n)
	}
}

func TestDeleteBucket_NonEmpty_NoForce(t *testing.T) {
	h, svc, drv := deleteBucketHarness(t, "full-bkt")
	ctx := context.Background()
	for _, k := range []string{"a/1.txt", "b/2.txt"} {
		if _, err := drv.Put(ctx, k, strings.NewReader("x"), PutOpts{}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	w := deleteBucketRequest(t, h, "full-bkt", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status: got %d want 409; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "bucket_not_empty") {
		t.Fatalf("body missing code: %s", body)
	}
	if !strings.Contains(body, "1.txt") && !strings.Contains(body, "2.txt") {
		t.Fatalf("body missing sample keys: %s", body)
	}
	// Bucket should still be present.
	if _, err := svc.Registry().Lookup("full-bkt"); err != nil {
		t.Fatalf("bucket gone after 409: %v", err)
	}
}

func TestDeleteBucket_NonEmpty_Force(t *testing.T) {
	h, svc, drv := deleteBucketHarness(t, "force-bkt")
	ctx := context.Background()
	keys := []string{"a/1.txt", "b/2.txt", "c/3.txt"}
	for _, k := range keys {
		if _, err := drv.Put(ctx, k, strings.NewReader("x"), PutOpts{}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	w := deleteBucketRequest(t, h, "force-bkt", "force=true")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%s", w.Code, w.Body.String())
	}
	if _, err := svc.Registry().Lookup("force-bkt"); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("bucket still present after force: %v", err)
	}
	// Driver-side bytes should also be gone — Get must fail.
	for _, k := range keys {
		rc, _, err := drv.Get(ctx, k)
		if err == nil {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
			t.Fatalf("expected %q to be gone after force-delete, but Get succeeded", k)
		}
	}
}

func TestDeleteBucket_NotFound(t *testing.T) {
	h, _, _ := deleteBucketHarness(t, "real-bkt")
	w := deleteBucketRequest(t, h, "ghost-bkt", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteBucket_StaticConfig(t *testing.T) {
	// Static-config buckets have no BucketConfigRecord row; Drop must
	// refuse so the registry and the TOML stay consistent.
	drv, _ := newLocalFSHarness(t)
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}, &BucketConfigRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	cfg := BucketConfig{Name: "static", Driver: "localfs", PublicRead: true}
	reg := NewTestRegistryWithDB(map[string]*Bucket{
		"static": {Config: cfg, Driver: drv},
	}, db) // db wired but NO row in blob_bucket_configs
	svc := NewService(reg, NewRepository(db), NewClock())
	h := NewHandler(svc, NewAuthorizer(), RegistryDeps{SigningKey: []byte("k")})

	w := deleteBucketRequest(t, h, "static", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not DB-backed") {
		t.Fatalf("body missing reason: %s", w.Body.String())
	}
}
