package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/internal/cli/deprecate"
)

func TestExecuteCreateSpecFlagsAreMutuallyExclusive(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		resetCommandFlags(rootCmd)
		executeCreateInput = ""
		executeCreateSpec = ""
	})
	executeCreateInput = ""
	executeCreateSpec = ""

	if err := executeCreateCmd.Flags().Set("input", "/tmp/input.yaml"); err != nil {
		t.Fatalf("set input: %v", err)
	}
	if err := executeCreateCmd.Flags().Set("spec", "/tmp/spec.yaml"); err != nil {
		t.Fatalf("set spec: %v", err)
	}

	_, err := resolveExecuteSpecPath(executeCreateCmd)
	if err == nil {
		t.Fatal("expected mutually exclusive flags to fail")
	}
	if exitCodeFor(err) != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d", exitCodeFor(err), ExitCodeUsage)
	}
	if !strings.Contains(err.Error(), "at most one of") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteSubmitAndCreateOutputAreByteIdentical(t *testing.T) {
	home := t.TempDir()
	specPath := home + "/execution.yaml"
	writeTestFile(t, specPath, `
specs:
  - algorithm:
      name: random
      version: "1.0.0"
    datapack: sample-datapack
`)
	payload := map[string]any{
		"code":    200,
		"message": "success",
		"data": map[string]any{
			"items": []any{
				map[string]any{
					"id":   7,
					"name": "pair_diagnosis",
				},
			},
			"pagination": map[string]any{
				"page":        1,
				"size":        100,
				"total":       1,
				"total_pages": 1,
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
			_ = json.NewEncoder(w).Encode(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	server := ts.URL
	baseArgs := []string{"--server", server, "--token", "jwt-test-token", "--project", "pair_diagnosis", "--output", "json", "--dry-run"}
	create := runCLI(t, append([]string{"execute", "create", "--input", specPath}, baseArgs...)...)
	submit := runCLI(t, append([]string{"execute", "submit", "--spec", specPath}, baseArgs...)...)

	if create.code != ExitCodeSuccess || submit.code != ExitCodeSuccess {
		t.Fatalf("execute create exited %d, submit exited %d", create.code, submit.code)
	}

	if create.stdout != submit.stdout {
		t.Fatalf("execute submit/stdout mismatch:\ncreate=%q\nsubmit=%q", create.stdout, submit.stdout)
	}
	if create.stderr != "" {
		t.Fatalf("execute create should not emit stderr: %q", create.stderr)
	}
	if !strings.Contains(submit.stderr, deprecate.Message("submit", "create")) {
		t.Fatalf("execute submit stderr missing warning: %q", submit.stderr)
	}
}
