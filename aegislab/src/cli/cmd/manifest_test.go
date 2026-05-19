package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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
