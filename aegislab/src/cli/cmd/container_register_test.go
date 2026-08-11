package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// containerRegisterServer spins up an httptest server that accepts
// POST /api/v2/containers/register and returns either a success envelope
// (with a register_id) or a stage-tagged failure envelope.
func containerRegisterServer(t *testing.T, failStage string) (*httptest.Server, *[]byte) {
	t.Helper()

	var captured []byte
	captureRef := &captured

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/containers/register" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		*captureRef = body

		if failStage != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    409,
				"message": "register failed at stage=" + failStage + " (register_id=reg-abc123def456): simulated",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    201,
			"message": "Container registered successfully",
			"data": map[string]any{
				"register_id":       "reg-abc123def456",
				"container_id":      11,
				"container_name":    "ob",
				"container_type":    "pedestal",
				"version_id":        22,
				"version_name":      "0.1.1",
				"image_ref":         "",
				"helm_config_id":    33,
				"chart_name":        "onlineboutique-aegis",
				"chart_version":     "0.1.1",
				"container_existed": false,
			},
		})
	}))

	return srv, &captured
}

func TestContainerRegisterPedestalSuccess(t *testing.T) {
	srv, captured := containerRegisterServer(t, "")
	defer srv.Close()

	res := runCLI(t, "container", "register",
		"--pedestal",
		"--name", "ob",
		"--registry", "docker.io",
		"--repo", "opspai",
		"--tag", "0.1.1",
		"--chart-name", "onlineboutique-aegis",
		"--chart-version", "0.1.1",
		"--repo-url", "oci://registry-1.docker.io/opspai",
		"--repo-name", "opspai",
		"--server", srv.URL, "--token", "tok",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, "register_id: reg-abc123def456") {
		t.Fatalf("expected register_id in stdout; got %q", res.stdout)
	}
	if !strings.Contains(res.stdout, "helm config: id=33") {
		t.Fatalf("expected helm config line; got %q", res.stdout)
	}

	// Spot-check the payload: form=pedestal, chart fields propagated.
	var payload map[string]any
	if err := json.Unmarshal(*captured, &payload); err != nil {
		t.Fatalf("decode captured body: %v (body=%s)", err, string(*captured))
	}
	if payload["form"] != "pedestal" {
		t.Errorf("form = %v, want pedestal", payload["form"])
	}
	if payload["chart_name"] != "onlineboutique-aegis" {
		t.Errorf("chart_name = %v, want onlineboutique-aegis", payload["chart_name"])
	}
}

func TestContainerRegisterBenchmarkSuccessWithEnv(t *testing.T) {
	srv, captured := containerRegisterServer(t, "")
	defer srv.Close()

	res := runCLI(t, "container", "register",
		"--benchmark",
		"--name", "ob-bench",
		"--registry", "docker.io",
		"--repo", "opspai/clickhouse_dataset",
		"--tag", "e2e-X",
		"--command", "bash /entrypoint.sh",
		"--env", "FOO=bar",
		"--env", "BAZ=qux",
		"--server", srv.URL, "--token", "tok",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit=%d stderr=%q", res.code, res.stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal(*captured, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["form"] != "benchmark" {
		t.Errorf("form = %v, want benchmark", payload["form"])
	}
	envList, ok := payload["env"].([]any)
	if !ok || len(envList) != 2 {
		t.Fatalf("env = %#v, want 2 entries", payload["env"])
	}
}

func TestContainerRegisterFailurePropagatesRegisterID(t *testing.T) {
	srv, _ := containerRegisterServer(t, "insert_version")
	defer srv.Close()

	res := runCLI(t, "container", "register",
		"--benchmark",
		"--name", "ob-bench",
		"--registry", "docker.io",
		"--repo", "opspai/clickhouse_dataset",
		"--tag", "e2e-X",
		"--command", "bash /entrypoint.sh",
		"--server", srv.URL, "--token", "tok",
	)
	if res.code == ExitCodeSuccess {
		t.Fatalf("expected non-zero exit; stdout=%q stderr=%q", res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "register_id=reg-abc123def456") {
		t.Fatalf("stderr missing register_id: %q", res.stderr)
	}
	if !strings.Contains(res.stderr, "stage=insert_version") {
		t.Fatalf("stderr missing stage tag: %q", res.stderr)
	}
}

func TestContainerRegisterRequiresFormFlag(t *testing.T) {
	res := runCLI(t, "container", "register", "--name", "x",
		"--server", "http://127.0.0.1:1", "--token", "tok",
	)
	if res.code == ExitCodeSuccess {
		t.Fatalf("expected failure when neither --pedestal nor --benchmark set")
	}
	if !strings.Contains(res.stderr, "exactly one of --pedestal") {
		t.Fatalf("unexpected stderr: %q", res.stderr)
	}
}
