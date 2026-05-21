package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManifestValidate_Valid_ExitsZero(t *testing.T) {
	resetManifestTestState(t)
	rc := executeArgs([]string{"manifest", "validate", "testdata/manifest-valid.yaml"})
	if rc != 0 {
		t.Fatalf("expected exit 0, got %d", rc)
	}
}

func TestManifestValidate_MissingCapability_ExitsNonZeroAndMentionsField(t *testing.T) {
	resetManifestTestState(t)
	flagQuiet = false

	out, rc := captureManifestStderr(t, func() int {
		return executeArgs([]string{"manifest", "validate", "testdata/manifest-invalid-missing-capability.yaml"})
	})

	if rc == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", out)
	}
	if !strings.Contains(out, "capability") {
		t.Fatalf("expected stderr to mention the missing 'capability' field, got: %q", out)
	}
}

func TestManifestImport_DryRun_SendsCorrectPathBodyAndQuery(t *testing.T) {
	resetManifestTestState(t)

	var (
		gotMethod string
		gotPath   string
		gotQuery  url.Values
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"upserted":2,"superseded":0,"dry_run":true,"point_ids":["abc","def"]}}`))
	}))
	defer srv.Close()

	t.Setenv("AEGIS_CHAOS_SERVER", srv.URL)

	stdout, err := captureStdout(t, func() error {
		if rc := executeArgs([]string{"manifest", "import", "--dry-run", "testdata/manifest-valid.yaml"}); rc != 0 {
			return errFromInt(rc)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("import returned non-zero: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotPath != "/v1beta/systems/ts/points/import" {
		t.Errorf("path: got %q want /v1beta/systems/ts/points/import", gotPath)
	}
	if gotQuery.Get("dry_run") != "true" {
		t.Errorf("query dry_run: got %q want true (full query: %v)", gotQuery.Get("dry_run"), gotQuery)
	}
	var sentManifest map[string]any
	if err := json.Unmarshal(gotBody, &sentManifest); err != nil {
		t.Fatalf("body not valid JSON: %v (body=%s)", err, string(gotBody))
	}
	md, ok := sentManifest["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing from forwarded body: %v", sentManifest)
	}
	if md["system"] != "ts" || md["service"] != "frontend" {
		t.Errorf("forwarded metadata mismatch: %v", md)
	}
	if !strings.Contains(stdout, "abc") {
		t.Errorf("expected stdout to include forwarded point id 'abc', got: %q", stdout)
	}
}

// TestResolveChaosServer_DefaultsToGatewayUnderServer verifies the new
// fallback path: when --chaos-server / AEGIS_CHAOS_SERVER are both
// unset, the resolver derives the URL from --server (which every other
// aegisctl command already requires), appending /v1beta/chaos to land
// on the gateway-federated prefix.
func TestResolveChaosServer_DefaultsToGatewayUnderServer(t *testing.T) {
	resetManifestTestState(t)
	t.Setenv(chaosServerEnv, "")
	prev := flagServer
	t.Cleanup(func() { flagServer = prev })
	flagServer = "https://aegis.example.com/"

	got, err := resolveChaosServer()
	if err != nil {
		t.Fatalf("resolveChaosServer: %v", err)
	}
	const want = "https://aegis.example.com/v1beta/chaos"
	if got != want {
		t.Fatalf("default chaos-server URL: got %q want %q", got, want)
	}
}

// TestManifestImport_ThroughGatewayPrefix simulates the federated path:
// chaos-server points at <gateway>/v1beta/chaos, the gateway middleware
// rewrites /v1beta/chaos/* → /v1beta/*, and the chaos handler must
// receive the un-prefixed path. Catches CLI regressions that would
// break the federation contract.
func TestManifestImport_ThroughGatewayPrefix(t *testing.T) {
	resetManifestTestState(t)

	var gotUpstreamPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mimic the gateway's strip_prefix on "/v1beta/chaos": the
		// /chaos segment is gateway-side only, so it's removed before
		// the chaos service sees the request.
		const prefix = "/v1beta/chaos"
		gotUpstreamPath = strings.TrimPrefix(r.URL.Path, prefix)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"upserted":2,"superseded":0,"dry_run":true,"point_ids":["abc"]}}`))
	}))
	defer srv.Close()

	t.Setenv("AEGIS_CHAOS_SERVER", srv.URL+"/v1beta/chaos")

	if rc := executeArgs([]string{"manifest", "import", "--dry-run", "testdata/manifest-valid.yaml"}); rc != 0 {
		t.Fatalf("import returned non-zero: %d", rc)
	}
	const want = "/v1beta/systems/ts/points/import"
	if gotUpstreamPath != want {
		t.Fatalf("upstream path after gateway rewrite: got %q want %q", gotUpstreamPath, want)
	}
}

// TestChaosDoJSON_RetriesOn5xx exercises the anti-hang retry: a server
// that fails the first POST with 503 must be retried once and the
// second-attempt success must be returned to the caller.
func TestChaosDoJSON_RetriesOn5xx(t *testing.T) {
	resetManifestTestState(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"upserted":1,"superseded":0,"dry_run":false,"point_ids":["x"]}}`))
	}))
	defer srv.Close()
	t.Setenv(chaosServerEnv, srv.URL)

	start := time.Now()
	body, status, err := chaosDoJSON(http.MethodPost, "/v1beta/systems/ts/points/import", []byte(`{}`))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("chaosDoJSON: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status: got %d want 200", status)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", calls.Load())
	}
	if !strings.Contains(string(body), `"upserted":1`) {
		t.Fatalf("body from successful retry not returned: %s", string(body))
	}
	// Backoff is 1s between attempts; allow generous upper bound for CI.
	if elapsed < 1*time.Second {
		t.Fatalf("expected ≥1s sleep between retries, got %s", elapsed)
	}
}

func errFromInt(rc int) error { return &intErr{rc} }

type intErr struct{ rc int }

func (e *intErr) Error() string { return "exit code" }

// resetManifestTestState clears the package-level flag globals between table
// entries since cobra mutates them in place via PersistentFlags.
func resetManifestTestState(t *testing.T) {
	t.Helper()
	flagOutput = "table"
	flagDryRun = false
	flagFetchSchema = false
	flagChaosServer = ""
	flagListSystem = ""
	flagListService = ""
	flagListInstance = ""
	flagListChartVer = ""
	flagToken = ""
	flagQuiet = true
}

func captureManifestStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	rc := fn()
	_ = w.Close()
	os.Stderr = orig
	out, _ := io.ReadAll(r)
	return string(out), rc
}
