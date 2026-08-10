package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// chartValuesServer serves GET /api/v2/systems/by-name/{name}/chart with a
// fixed merged-values map, mimicking the live DB effective helm values.
func chartValuesServer(t *testing.T, values map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "ok",
			"data": map[string]any{
				"system_name": "ts",
				"chart_name":  "trainticket",
				"version":     "0.2.1",
				"values":      values,
			},
		})
	}))
}

func writeDiffSeedFixture(t *testing.T, helmValues string, overlay string) string {
	t.Helper()
	dir := t.TempDir()
	data := `containers:
  - type: 2
    name: ts
    is_public: true
    status: 1
    versions:
      - name: 1.0.6
        status: 1
        helm_config:
          version: 0.2.1
          chart_name: trainticket
          repo_name: train-ticket
          repo_url: https://operationspai.github.io/train-ticket
          values:
` + helmValues
	if err := os.WriteFile(filepath.Join(dir, "data.yaml"), []byte(data), 0o600); err != nil {
		t.Fatalf("write data.yaml: %v", err)
	}
	if overlay != "" {
		if err := os.WriteFile(filepath.Join(dir, "ts.yaml"), []byte(overlay), 0o600); err != nil {
			t.Fatalf("write ts.yaml: %v", err)
		}
	}
	return dir
}

// TestDiffSeedDetectsImageDrift is the issue #478 acceptance test: when the
// live DB effective helm values diverge from the git seed, diff-seed must
// exit non-zero (workflow-failure); when they agree it must exit 0. The
// fixture reproduces the exact ts@1.0.6 drift the issue describes
// (repository registry + loadgenerator tag).
func TestDiffSeedDetectsImageDrift(t *testing.T) {
	const seedValues = `            - key: global.image.repository
              value_type: 0
              default_value: pair-diag-cn-guangzhou.cr.volces.com/pair
            - key: loadgenerator.image.tag
              value_type: 0
              default_value: "023"
`

	t.Run("drift exits workflow-failure", func(t *testing.T) {
		dir := writeDiffSeedFixture(t, seedValues, "")
		srv := chartValuesServer(t, map[string]any{
			"global": map[string]any{
				"image": map[string]any{"repository": "pair-cn-shanghai.cr.volces.com/opspai"},
			},
			"loadgenerator": map[string]any{
				"image": map[string]any{"tag": "024"},
			},
		})
		defer srv.Close()

		res := runCLI(t, "--server", srv.URL, "--token", "token",
			"system", "diff-seed", "--name", "ts", "--from-seed", filepath.Join(dir, "data.yaml"))
		if res.code != ExitCodeWorkflowFailure {
			t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeWorkflowFailure, res.stderr)
		}
	})

	t.Run("match exits zero", func(t *testing.T) {
		dir := writeDiffSeedFixture(t, seedValues, "")
		srv := chartValuesServer(t, map[string]any{
			"global": map[string]any{
				"image": map[string]any{"repository": "pair-diag-cn-guangzhou.cr.volces.com/pair"},
			},
			"loadgenerator": map[string]any{
				"image": map[string]any{"tag": "023"},
			},
		})
		defer srv.Close()

		res := runCLI(t, "--server", srv.URL, "--token", "token",
			"system", "diff-seed", "--name", "ts", "--from-seed", filepath.Join(dir, "data.yaml"))
		if res.code != ExitCodeSuccess {
			t.Fatalf("exit code = %d, want %d; stderr=%q stdout=%q", res.code, ExitCodeSuccess, res.stderr, res.stdout)
		}
	})

	// The overlay file is a real runtime input (value_file). A key carried
	// only by the overlay must participate in the diff: if the DB has it the
	// effective values match, so no drift.
	t.Run("overlay key counts toward effective values", func(t *testing.T) {
		overlay := "global:\n  security:\n    allowInsecureImages: true\n"
		dir := writeDiffSeedFixture(t, seedValues, overlay)
		srv := chartValuesServer(t, map[string]any{
			"global": map[string]any{
				"image":    map[string]any{"repository": "pair-diag-cn-guangzhou.cr.volces.com/pair"},
				"security": map[string]any{"allowInsecureImages": true},
			},
			"loadgenerator": map[string]any{
				"image": map[string]any{"tag": "023"},
			},
		})
		defer srv.Close()

		res := runCLI(t, "--server", srv.URL, "--token", "token",
			"system", "diff-seed", "--name", "ts", "--from-seed", filepath.Join(dir, "data.yaml"))
		if res.code != ExitCodeSuccess {
			t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeSuccess, res.stderr)
		}
	})
}
