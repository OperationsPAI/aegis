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

// TestAuthorizer_CanRead_EmptyRolesDeniedOnRoleRestrictedBucket guards the
// silent-ACL-bypass regression: before subjectFromContext was fixed to read
// roles off the gin context, every caller saw Subject.Roles == nil and this
// check returned the wrong answer in two directions (over-grant + under-grant).
// Keep this test on the real Authorizer so it survives handler refactors.
func TestAuthorizer_CanRead_EmptyRolesDeniedOnRoleRestrictedBucket(t *testing.T) {
	auth := NewAuthorizer()
	cfg := &BucketConfig{Name: "restricted", ReadRoles: []string{"admin-role"}}
	sub := Subject{UserID: 42, Roles: nil}
	if auth.CanRead(cfg, sub, nil) {
		t.Fatalf("CanRead: expected false for empty-roles subject on role-restricted bucket")
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

// ---- S2b: per-key ACL on batch / copy paths ----

// TestHandler_CopyObject_DeniesUnreadableSource is the regression guard for the
// cross-tenant copy bypass: CopyObject checked only bucket-level CanWrite, so a
// user who can write (empty write_roles → CanWrite true for everyone) but cannot
// read (role-restricted, not the owner) could copy out — i.e. read — another
// tenant's object. The per-key read ACL on the source must now deny it.
func TestHandler_CopyObject_DeniesUnreadableSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// write_roles empty -> any authenticated user CanWrite.
	// read_roles restricted -> only admin-role / the owner CanRead.
	cfg := BucketConfig{Name: "mixed", WriteRoles: []string{}, ReadRoles: []string{"admin-role"}}
	h, drv, repo := newACLHarness(t, cfg)

	ownerID := 7
	ctx := context.Background()
	if _, err := drv.Put(ctx, "secret.txt", strings.NewReader("classified"), PutOpts{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &ObjectRecord{
		Bucket: "mixed", StorageKey: "secret.txt", SizeBytes: 10,
		ContentType: "text/plain", UploadedBy: &ownerID,
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"src":"secret.txt","dst":"stolen.txt"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "bucket", Value: "mixed"}}
	setUserCtx(c, 42, false, []string{}) // can write (empty roles), cannot read

	h.CopyObject(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("CopyObject unreadable src: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandler_CopyObject_AllowsOwnerReadableSource is the companion: the object
// owner (CanRead via uploader match) can still copy their own object.
func TestHandler_CopyObject_AllowsOwnerReadableSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := BucketConfig{Name: "mixed2", WriteRoles: []string{}, ReadRoles: []string{"admin-role"}}
	h, drv, repo := newACLHarness(t, cfg)

	ownerID := 7
	ctx := context.Background()
	if _, err := drv.Put(ctx, "mine.txt", strings.NewReader("data"), PutOpts{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &ObjectRecord{
		Bucket: "mixed2", StorageKey: "mine.txt", SizeBytes: 4,
		ContentType: "text/plain", UploadedBy: &ownerID,
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"src":"mine.txt","dst":"copy.txt"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "bucket", Value: "mixed2"}}
	setUserCtx(c, ownerID, false, []string{}) // owner: CanWrite + CanRead(own)

	h.CopyObject(c)

	if w.Code != http.StatusOK {
		t.Fatalf("CopyObject own object: expected 200, got %d: %s", w.Code, w.Body.String())
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
