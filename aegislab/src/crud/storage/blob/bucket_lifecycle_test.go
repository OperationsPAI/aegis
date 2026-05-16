package blob

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
)

func TestBucketLifecycle_Validate_HappyPath(t *testing.T) {
	lc := &BucketLifecycle{
		Rules: []BucketLifecycleRule{
			{Name: "expire-tmp", MatchPrefix: "tmp/", ExpireAfterDays: 7, Action: "delete"},
			{Name: "expire-logs", MatchPrefix: "logs/", ExpireAfterDays: 30, Action: "delete"},
		},
	}
	if err := lc.Validate(); err != nil {
		t.Fatalf("expected valid policy, got: %v", err)
	}
}

func TestBucketLifecycle_Validate_NilPolicy(t *testing.T) {
	var lc *BucketLifecycle
	if err := lc.Validate(); err != nil {
		t.Fatalf("nil policy should be valid (no policy), got: %v", err)
	}
}

func TestBucketLifecycle_Validate_RejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name string
		lc   BucketLifecycle
		want string
	}{
		{
			name: "missing rule name",
			lc:   BucketLifecycle{Rules: []BucketLifecycleRule{{ExpireAfterDays: 1, Action: "delete"}}},
			want: "name is required",
		},
		{
			name: "duplicate name",
			lc: BucketLifecycle{Rules: []BucketLifecycleRule{
				{Name: "dup", ExpireAfterDays: 1, Action: "delete"},
				{Name: "dup", ExpireAfterDays: 2, Action: "delete"},
			}},
			want: "duplicate rule name",
		},
		{
			name: "expire days too low",
			lc:   BucketLifecycle{Rules: []BucketLifecycleRule{{Name: "r", ExpireAfterDays: 0, Action: "delete"}}},
			want: "out of range",
		},
		{
			name: "expire days too high",
			lc:   BucketLifecycle{Rules: []BucketLifecycleRule{{Name: "r", ExpireAfterDays: 4000, Action: "delete"}}},
			want: "out of range",
		},
		{
			name: "unknown action",
			lc:   BucketLifecycle{Rules: []BucketLifecycleRule{{Name: "r", ExpireAfterDays: 1, Action: "archive"}}},
			want: "action",
		},
		{
			name: "match_prefix too long",
			lc: BucketLifecycle{Rules: []BucketLifecycleRule{{
				Name: "r", MatchPrefix: strings.Repeat("a", BucketLifecycleMaxPrefixLen+1),
				ExpireAfterDays: 1, Action: "delete",
			}}},
			want: "match_prefix exceeds",
		},
		{
			name: "too many rules",
			lc:   tooManyRules(),
			want: "rules exceeds cap",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.lc.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrBucketLifecycleInvalid) {
				t.Fatalf("expected ErrBucketLifecycleInvalid, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func tooManyRules() BucketLifecycle {
	rules := make([]BucketLifecycleRule, BucketLifecycleMaxRules+1)
	for i := range rules {
		rules[i] = BucketLifecycleRule{
			Name:            "r" + itoa(i),
			ExpireAfterDays: 1,
			Action:          "delete",
		}
	}
	return BucketLifecycle{Rules: rules}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

func TestBucketLifecycle_RoundTrip(t *testing.T) {
	in := &BucketLifecycle{
		Rules: []BucketLifecycleRule{
			{Name: "r1", MatchPrefix: "a/", ExpireAfterDays: 3, Action: "delete"},
		},
	}
	enc, err := encodeBucketLifecycle(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodeBucketLifecycle(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out == nil || len(out.Rules) != 1 || out.Rules[0].Name != "r1" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestEncodeBucketLifecycle_NilAndEmpty(t *testing.T) {
	if got, err := encodeBucketLifecycle(nil); err != nil || got != "" {
		t.Fatalf("nil policy: got (%q, %v), want (\"\", nil)", got, err)
	}
	if got, err := encodeBucketLifecycle(&BucketLifecycle{}); err != nil || got != "" {
		t.Fatalf("empty policy: got (%q, %v), want (\"\", nil)", got, err)
	}
	if got, err := decodeBucketLifecycle(""); err != nil || got != nil {
		t.Fatalf("empty decode: got (%+v, %v), want (nil, nil)", got, err)
	}
}

// --- Handler tests ---

func newLifecycleHandlerHarness(t *testing.T) (*Handler, *Service) {
	t.Helper()
	drv, _ := newLocalFSHarness(t)
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}, &BucketConfigRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// Seed a DB row so SetLifecycle has something to UPDATE.
	if err := db.Create(&BucketConfigRecord{Name: "lc-bkt", Driver: "localfs"}).Error; err != nil {
		t.Fatalf("seed bucket row: %v", err)
	}
	cfg := BucketConfig{Name: "lc-bkt", Driver: "localfs"}
	reg := NewTestRegistryWithDB(map[string]*Bucket{
		"lc-bkt": {Config: cfg, Driver: drv},
	}, db)
	svc := NewService(reg, NewRepository(db), NewClock())
	h := NewHandler(svc, NewAuthorizer(), RegistryDeps{SigningKey: []byte("k")})
	return h, svc
}

func doLifecycleRequest(t *testing.T, h *Handler, method, bucket string, body []byte, handler func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/buckets/"+bucket+"/lifecycle", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "bucket", Value: bucket}}
	handler(c)
	_ = h // silence unused for direct-handler callers
	return w
}

func TestHandler_PutAndGetBucketLifecycle_HappyPath(t *testing.T) {
	h, svc := newLifecycleHandlerHarness(t)

	in := BucketLifecycle{Rules: []BucketLifecycleRule{
		{Name: "r1", MatchPrefix: "tmp/", ExpireAfterDays: 7, Action: "delete"},
	}}
	body, _ := json.Marshal(in)

	w := doLifecycleRequest(t, h, http.MethodPut, "lc-bkt", body, h.PutBucketLifecycle)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: got %d want 200; body=%s", w.Code, w.Body.String())
	}

	// In-memory bucket reflects the new policy.
	b, _ := svc.Registry().Lookup("lc-bkt")
	if b.Config.Lifecycle == nil || len(b.Config.Lifecycle.Rules) != 1 {
		t.Fatalf("registry not updated: %+v", b.Config.Lifecycle)
	}

	// GET round-trips the same shape.
	w = doLifecycleRequest(t, h, http.MethodGet, "lc-bkt", nil, h.GetBucketLifecycle)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"name":"r1"`) {
		t.Fatalf("GET body missing rule: %s", w.Body.String())
	}
}

func TestHandler_PutBucketLifecycle_RejectsInvalid(t *testing.T) {
	h, _ := newLifecycleHandlerHarness(t)
	in := BucketLifecycle{Rules: []BucketLifecycleRule{
		{Name: "bad", ExpireAfterDays: 0, Action: "delete"},
	}}
	body, _ := json.Marshal(in)
	w := doLifecycleRequest(t, h, http.MethodPut, "lc-bkt", body, h.PutBucketLifecycle)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestHandler_GetBucketLifecycle_NoPolicyReturnsEmptyRules(t *testing.T) {
	h, _ := newLifecycleHandlerHarness(t)
	w := doLifecycleRequest(t, h, http.MethodGet, "lc-bkt", nil, h.GetBucketLifecycle)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"rules":[]`) {
		t.Fatalf("expected empty rules array, got: %s", w.Body.String())
	}
}

func TestHandler_GetBucketLifecycle_BucketNotFound(t *testing.T) {
	h, _ := newLifecycleHandlerHarness(t)
	w := doLifecycleRequest(t, h, http.MethodGet, "ghost", nil, h.GetBucketLifecycle)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestHandler_PutBucketLifecycle_NonOwnerDenied seeds a bucket whose
// WriteRoles is non-empty so the default subject (no roles) cannot
// modify the policy.
func TestHandler_PutBucketLifecycle_NonOwnerDenied(t *testing.T) {
	drv, _ := newLocalFSHarness(t)
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}, &BucketConfigRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := db.Create(&BucketConfigRecord{Name: "restricted", Driver: "localfs", WriteRoles: "admin-role"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := BucketConfig{Name: "restricted", Driver: "localfs", WriteRoles: []string{"admin-role"}}
	reg := NewTestRegistryWithDB(map[string]*Bucket{"restricted": {Config: cfg, Driver: drv}}, db)
	svc := NewService(reg, NewRepository(db), NewClock())
	h := NewHandler(svc, NewAuthorizer(), RegistryDeps{SigningKey: []byte("k")})

	in := BucketLifecycle{Rules: []BucketLifecycleRule{
		{Name: "r1", ExpireAfterDays: 1, Action: "delete"},
	}}
	body, _ := json.Marshal(in)
	w := doLifecycleRequest(t, h, http.MethodPut, "restricted", body, h.PutBucketLifecycle)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d want 403; body=%s", w.Code, w.Body.String())
	}
}

// newCreateBucketHarness builds a Service + Handler whose registry has
// the localfs signing key wired so Registry.Create can build the
// driver. Returns a tempdir caller should use as the bucket Root.
func newCreateBucketHarness(t *testing.T) (*Handler, *Service, string) {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&ObjectRecord{}, &BucketConfigRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	reg := NewTestRegistryWithDeps(map[string]*Bucket{}, db, RegistryDeps{SigningKey: []byte("k")})
	svc := NewService(reg, NewRepository(db), NewClock())
	h := NewHandler(svc, NewAuthorizer(), RegistryDeps{SigningKey: []byte("k")})
	return h, svc, t.TempDir()
}

// TestHandler_CreateBucket_WithLifecycle wires the new field through
// the CreateBucket handler and asserts the policy round-trips into the
// registry.
func TestHandler_CreateBucket_WithLifecycle(t *testing.T) {
	h, svc, tmp := newCreateBucketHarness(t)

	body := CreateBucketReq{
		Name:   "new-bkt",
		Driver: "localfs",
		Root:   tmp,
		Lifecycle: &BucketLifecycle{Rules: []BucketLifecycleRule{
			{Name: "r1", MatchPrefix: "tmp/", ExpireAfterDays: 7, Action: "delete"},
		}},
	}
	buf, _ := json.Marshal(body)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/buckets", bytes.NewReader(buf))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateBucket(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("got %d want 201; body=%s", w.Code, w.Body.String())
	}
	b, err := svc.Registry().Lookup("new-bkt")
	if err != nil {
		t.Fatalf("lookup after create: %v", err)
	}
	if b.Config.Lifecycle == nil || len(b.Config.Lifecycle.Rules) != 1 {
		t.Fatalf("lifecycle not persisted on create: %+v", b.Config.Lifecycle)
	}
}

func TestHandler_CreateBucket_RejectsInvalidLifecycle(t *testing.T) {
	h, _, tmp := newCreateBucketHarness(t)

	body := CreateBucketReq{
		Name:   "bad-bkt",
		Driver: "localfs",
		Root:   tmp,
		Lifecycle: &BucketLifecycle{Rules: []BucketLifecycleRule{
			{Name: "r1", ExpireAfterDays: 0, Action: "delete"},
		}},
	}
	buf, _ := json.Marshal(body)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/buckets", bytes.NewReader(buf))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateBucket(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400; body=%s", w.Code, w.Body.String())
	}
}
