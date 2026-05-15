package blob

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
)

// newACLHarness builds a Handler + Repository with the supplied BucketConfig.
// The driver is a fresh localfs tempdir.
func newACLHarness(t *testing.T, cfg BucketConfig) (*Handler, *LocalFSDriver, *Repository) {
	t.Helper()
	drv, _ := newLocalFSHarness(t)
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := NewRepository(db)
	cfg.Driver = "localfs"
	reg := NewTestRegistry(map[string]*Bucket{
		cfg.Name: {Config: cfg, Driver: drv},
	})
	svc := NewService(reg, repo, NewClock())
	auth := NewAuthorizer()
	h := NewHandler(svc, auth, RegistryDeps{SigningKey: []byte("test-key")})
	return h, drv, repo
}

// setUserCtx stashes a user identity on a gin.Context without running
// through middleware. Mirrors what TrustedHeaderAuth would populate.
func setUserCtx(c *gin.Context, userID int, isAdmin bool, roles []string) {
	c.Set(consts.CtxKeyUserID, userID)
	c.Set(consts.CtxKeyIsAdmin, isAdmin)
	c.Set(consts.CtxKeyIsServiceToken, false)
	c.Set(consts.CtxKeyUserRoles, roles)
}

// ---- S1: Stat ACL ----

func TestHandler_Stat_ForbiddenWithoutReadRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := BucketConfig{
		Name:      "restricted",
		ReadRoles: []string{"admin-role"},
	}
	h, drv, repo := newACLHarness(t, cfg)

	seed(t, context.Background(), drv, repo, "restricted", "s1.txt", "hello")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodHead, "/", nil)
	c.Params = gin.Params{
		{Key: "bucket", Value: "restricted"},
		{Key: "key", Value: "/s1.txt"},
	}
	setUserCtx(c, 42, false, []string{})

	h.Stat(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Stat: expected 403 for user without read role, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Stat_AllowedForObjectOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := BucketConfig{
		Name:      "restricted",
		ReadRoles: []string{"admin-role"},
	}
	h, drv, repo := newACLHarness(t, cfg)

	ownerID := 7
	ctx := context.Background()
	_, err := drv.Put(ctx, "owned.txt", strings.NewReader("data"), PutOpts{ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	rec := &ObjectRecord{
		Bucket:      "restricted",
		StorageKey:  "owned.txt",
		SizeBytes:   4,
		ContentType: "text/plain",
		UploadedBy:  &ownerID,
	}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodHead, "/", nil)
	c.Params = gin.Params{
		{Key: "bucket", Value: "restricted"},
		{Key: "key", Value: "/owned.txt"},
	}
	setUserCtx(c, ownerID, false, []string{})

	h.Stat(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Stat: expected 200 for object owner, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- S2: Delete legacy-data admin gate ----

func TestHandler_Delete_LegacyObject_RequiresAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Empty write_roles → CanWrite returns true for any user.
	// The admin gate must still fire for driver-only (legacy) objects.
	cfg := BucketConfig{Name: "open", WriteRoles: []string{}}
	h, drv, _ := newACLHarness(t, cfg)

	// Put a driver-only object (no metadata row) — simulates legacy data.
	if _, err := drv.Put(context.Background(), "legacy.txt", strings.NewReader("old"), PutOpts{}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	c.Params = gin.Params{
		{Key: "bucket", Value: "open"},
		{Key: "key", Value: "/legacy.txt"},
	}
	setUserCtx(c, 99, false, []string{})

	h.Delete(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Delete legacy: expected 403 for non-admin, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Delete_LegacyObject_AllowedForAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := BucketConfig{Name: "open", WriteRoles: []string{}}
	h, drv, _ := newACLHarness(t, cfg)

	if _, err := drv.Put(context.Background(), "legacy2.txt", strings.NewReader("old"), PutOpts{}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	c.Params = gin.Params{
		{Key: "bucket", Value: "open"},
		{Key: "key", Value: "/legacy2.txt"},
	}
	setUserCtx(c, 1, true, []string{})

	h.Delete(c)

	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete legacy: expected 204 for admin, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- S3: Raw PUT content-type / size enforcement ----

func newRawHarness(t *testing.T, cfg BucketConfig) (*Handler, *LocalFSDriver) {
	t.Helper()
	drv, _ := newLocalFSHarness(t)
	cfg.Driver = "localfs"
	reg := NewTestRegistry(map[string]*Bucket{cfg.Name: {Config: cfg, Driver: drv}})
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	svc := NewService(reg, NewRepository(db), NewClock())
	auth := NewAuthorizer()
	h := NewHandler(svc, auth, RegistryDeps{SigningKey: []byte("test-key")})
	return h, drv
}

func mintPutToken(t *testing.T, signingKey []byte, bucket, key string) string {
	t.Helper()
	tok, err := EncodeToken(signingKey, Token{Bucket: bucket, Key: key, Op: OpPut, Exp: 9999999999})
	if err != nil {
		t.Fatalf("EncodeToken: %v", err)
	}
	return tok
}

func TestHandler_Raw_Put_RejectsDisallowedContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signingKey := []byte("test-key")
	h, _ := newRawHarness(t, BucketConfig{
		Name:                "typed",
		AllowedContentTypes: []string{"text/plain"},
	})
	tok := mintPutToken(t, signingKey, "typed", "f.bin")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader("data"))
	c.Request.Header.Set("Content-Type", "application/octet-stream")
	c.Params = gin.Params{{Key: "token", Value: tok}}

	h.Raw(c)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("Raw PUT bad ct: expected 415, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Raw_Put_RejectsOversizedObject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signingKey := []byte("test-key")
	h, _ := newRawHarness(t, BucketConfig{
		Name:                "typed",
		AllowedContentTypes: []string{"text/plain"},
		MaxObjectBytes:      10,
	})
	tok := mintPutToken(t, signingKey, "typed", "big.txt")

	body := "this is way too large for the ten-byte limit"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "text/plain")
	c.Request = req
	c.Params = gin.Params{{Key: "token", Value: tok}}

	h.Raw(c)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Raw PUT oversized: expected 413, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Raw_Put_AcceptsAllowedContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signingKey := []byte("test-key")
	h, drv := newRawHarness(t, BucketConfig{
		Name:                "typed",
		AllowedContentTypes: []string{"text/plain"},
		MaxObjectBytes:      1024,
	})
	tok := mintPutToken(t, signingKey, "typed", "ok.txt")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("hello"))
	req.ContentLength = 5
	req.Header.Set("Content-Type", "text/plain")
	c.Request = req
	c.Params = gin.Params{{Key: "token", Value: tok}}

	h.Raw(c)

	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("Raw PUT ok: expected 204/200 success, got %d: %s", w.Code, w.Body.String())
	}

	rc, _, err := drv.Get(context.Background(), "ok.txt")
	if err != nil {
		t.Fatalf("Get after Raw PUT: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "hello" {
		t.Fatalf("Raw PUT body: got %q want %q", got, "hello")
	}
}
