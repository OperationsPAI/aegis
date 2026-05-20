package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/cli/config"
)

func TestServiceAccountIssue_PostsCorrectRequest(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotCT     string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			_ = json.Unmarshal(b, &gotBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"Success","data":{` +
			`"token":"eyJhbGciOiJSUzI1NiJ9.payload.sig",` +
			`"expires_at":"2027-05-20T10:22:01Z"}}`))
	}))
	defer srv.Close()

	serviceAccountTestSetup(t, srv.URL)
	defer resetServiceAccountFlags()

	saIssueLifetimeDays = 30
	if err := runServiceAccountIssue(nil, []string{"chaos-webhook"}); err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", gotMethod)
	}
	if gotPath != "/v1/service-accounts/chaos-webhook/issue" {
		t.Errorf("path = %q; want /v1/service-accounts/chaos-webhook/issue", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q; want Bearer prefix", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", gotCT)
	}
	if v, ok := gotBody["lifetime_days"].(float64); !ok || int(v) != 30 {
		t.Errorf("lifetime_days = %v; want 30", gotBody["lifetime_days"])
	}
}

func TestServiceAccountIssue_OmitsBodyWhenLifetimeUnset(t *testing.T) {
	var gotCT string
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotLen = r.ContentLength
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"Success","data":{"token":"t","expires_at":"2027-01-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	serviceAccountTestSetup(t, srv.URL)
	defer resetServiceAccountFlags()

	if err := runServiceAccountIssue(nil, []string{"sa1"}); err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if gotCT != "" {
		t.Errorf("Content-Type = %q; want empty (no body sent)", gotCT)
	}
	if gotLen > 0 {
		t.Errorf("ContentLength = %d; want 0 (no body)", gotLen)
	}
}

func TestServiceAccountRevoke_PostsCorrectRequest(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	serviceAccountTestSetup(t, srv.URL)
	defer resetServiceAccountFlags()

	if err := runServiceAccountRevoke(nil, []string{"chaos-webhook"}); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", gotMethod)
	}
	if gotPath != "/v1/service-accounts/chaos-webhook/revoke" {
		t.Errorf("path = %q; want /v1/service-accounts/chaos-webhook/revoke", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q; want Bearer prefix", gotAuth)
	}
}

func TestServiceAccountRevoke_PropagatesNonSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"service account not found"}`))
	}))
	defer srv.Close()

	serviceAccountTestSetup(t, srv.URL)
	defer resetServiceAccountFlags()

	err := runServiceAccountRevoke(nil, []string{"missing"})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func serviceAccountTestSetup(t *testing.T, srvURL string) {
	t.Helper()
	cfg = &config.Config{}
	flagSSOServer = srvURL
	flagToken = "test-bearer"
	flagOutput = "json"
	flagQuiet = true
}

func resetServiceAccountFlags() {
	flagSSOServer = ""
	flagToken = ""
	flagOutput = ""
	flagQuiet = false
	saIssueLifetimeDays = 0
}
