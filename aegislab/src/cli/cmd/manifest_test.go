package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
		if rc := executeArgs([]string{"manifest", "import", "--dry-run", "--output", "json", "testdata/manifest-valid.yaml"}); rc != 0 {
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

func TestManifestImportDir_MixedTree_ImportsValidAndSkipsRest(t *testing.T) {
	resetManifestTestState(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"upserted":3,"superseded":1,"dry_run":false,"point_ids":["p1","p2","p3"]}}`))
	}))
	defer srv.Close()
	t.Setenv(chaosServerEnv, srv.URL)

	root := t.TempDir()
	validYAML, err := os.ReadFile("testdata/manifest-valid.yaml")
	if err != nil {
		t.Fatalf("read valid manifest: %v", err)
	}
	mustWrite(t, filepath.Join(root, "svc-a", "frontend.yaml"), validYAML)
	mustWrite(t, filepath.Join(root, "svc-b", "checkout.yml"), validYAML)
	mustWrite(t, filepath.Join(root, "notes.txt"), []byte("not yaml"))
	mustWrite(t, filepath.Join(root, "other.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\n"))

	stderr, rc := captureManifestStderr(t, func() int {
		return executeArgs([]string{"manifest", "import-dir", "--concurrency", "2", root})
	})
	if rc != 0 {
		t.Fatalf("import-dir exit=%d stderr=%q", rc, stderr)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 server calls (one per valid PointManifest), got %d", got)
	}
}

func TestManifestImportDir_FailureWithoutKeepGoing_AbortsNonZero(t *testing.T) {
	resetManifestTestState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"message":"boom"}`))
	}))
	defer srv.Close()
	t.Setenv(chaosServerEnv, srv.URL)

	root := t.TempDir()
	validYAML, err := os.ReadFile("testdata/manifest-valid.yaml")
	if err != nil {
		t.Fatalf("read valid manifest: %v", err)
	}
	mustWrite(t, filepath.Join(root, "a.yaml"), validYAML)

	rc := executeArgs([]string{"manifest", "import-dir", root})
	if rc == 0 {
		t.Fatal("expected non-zero exit on server 500")
	}
}

func TestManifestImportDir_KeepGoing_FinishesAllButReportsFailure(t *testing.T) {
	resetManifestTestState(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"message":"transient"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"upserted":1,"superseded":0,"dry_run":false,"point_ids":["p"]}}`))
	}))
	defer srv.Close()
	t.Setenv(chaosServerEnv, srv.URL)

	root := t.TempDir()
	validYAML, err := os.ReadFile("testdata/manifest-valid.yaml")
	if err != nil {
		t.Fatalf("read valid manifest: %v", err)
	}
	mustWrite(t, filepath.Join(root, "a.yaml"), validYAML)
	mustWrite(t, filepath.Join(root, "b.yaml"), validYAML)

	rc := executeArgs([]string{"manifest", "import-dir", "--concurrency", "1", "--keep-going", root})
	if rc == 0 {
		t.Fatal("expected non-zero exit when --keep-going still has at least one failure")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected both files attempted under --keep-going, got %d call(s)", got)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
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
	flagImportDirConcurrency = 0
	flagImportDirKeepGoing = false
	chaosServerDeprecationOnce = sync.Once{}
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
