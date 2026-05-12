package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInjectList_NDJSONOutput(t *testing.T) {
	var requestedPaths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path+"?"+r.URL.RawQuery)

		switch r.URL.Path {
		case "/api/v2/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items": []map[string]any{
						{"id": 7, "name": "pair_diagnosis"},
					},
					"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
				},
			})
		case "/api/v2/projects/7/injections":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"id":         11,
							"name":       "inj-1",
							"state":      "Created",
							"fault_type": "cpu",
							"category":   "ts",
						},
						{
							"id":         12,
							"name":       "batch-hybrid-1",
							"state":      "Created",
							"fault_type": "hybrid",
							"category":   "ts",
							"engine_config_summary": []map[string]any{
								{"chaos_type": "JVMException", "app": "ts-cancel-service", "method": "calculateRefund"},
								{"chaos_type": "NetworkLoss", "app": "ts-user-service"},
							},
						},
					},
					"pagination": map[string]any{"page": 1, "size": 20, "total": 2, "total_pages": 1},
				},
			})
		default:
			t.Fatalf("unexpected request path=%q query=%q", r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	res := runCLI(t, "inject", "list", "--project", "pair_diagnosis", "--output", "ndjson",
		"--server", ts.URL, "--token", "tok")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want %d; stdout=%q stderr=%q", res.code, ExitCodeSuccess, res.stdout, res.stderr)
	}

	rawLines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(rawLines) != 2 {
		t.Fatalf("line count = %d, want 2; stdout=%q", len(rawLines), res.stdout)
	}
	for _, line := range rawLines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid JSON line: %q (%v)", line, err)
		}
		if _, ok := obj["id"]; !ok {
			t.Fatalf("missing id in line: %v", obj)
		}
	}

	var metaPayload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.stderr)), &metaPayload); err != nil {
		t.Fatalf("stderr metadata is not json: %q", res.stderr)
	}
	rawMeta, ok := metaPayload["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("stderr missing _meta envelope: %v", metaPayload)
	}
	metaTotal, ok := rawMeta["total"].(float64)
	if !ok || int(metaTotal) != 2 {
		t.Fatalf("stderr _meta.total = %v (want 2), raw=%v", rawMeta["total"], metaPayload)
	}

	if strings.Contains(res.stdout, "\"_meta\"") {
		t.Fatalf("stdout should not include _meta envelope: %q", res.stdout)
	}

	requestedPaths = nil
	res = runCLI(t, "inject", "list", "--project", "pair_diagnosis", "--output", "invalid-format",
		"--server", ts.URL, "--token", "tok")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit = %d, want %d for invalid output", res.code, ExitCodeUsage)
	}
	if len(requestedPaths) != 0 {
		t.Fatalf("invalid --output should not send requests; got %v", requestedPaths)
	}

	// --system maps to the backend's `category` query param, and the hybrid
	// row's engine_config_summary survives end-to-end NDJSON marshaling.
	requestedPaths = nil
	res = runCLI(t, "inject", "list", "--project", "pair_diagnosis", "--system", "ts",
		"--output", "ndjson", "--server", ts.URL, "--token", "tok")
	if res.code != ExitCodeSuccess {
		t.Fatalf("--system: exit = %d, want %d; stderr=%q", res.code, ExitCodeSuccess, res.stderr)
	}
	var sawCategoryParam bool
	for _, p := range requestedPaths {
		if strings.HasPrefix(p, "/api/v2/projects/7/injections") && strings.Contains(p, "category=ts") {
			sawCategoryParam = true
		}
	}
	if !sawCategoryParam {
		t.Fatalf("--system=ts should send category=ts; got requests=%v", requestedPaths)
	}

	var sawHybridLeaves bool
	for _, line := range strings.Split(strings.TrimSpace(res.stdout), "\n") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid JSON line: %q (%v)", line, err)
		}
		if obj["fault_type"] != "hybrid" {
			continue
		}
		leaves, ok := obj["engine_config_summary"].([]any)
		if !ok || len(leaves) != 2 {
			t.Fatalf("hybrid row missing engine_config_summary: %v", obj)
		}
		first, _ := leaves[0].(map[string]any)
		if first["chaos_type"] != "JVMException" || first["app"] != "ts-cancel-service" {
			t.Fatalf("hybrid leaf 0 wrong shape: %v", first)
		}
		sawHybridLeaves = true
	}
	if !sawHybridLeaves {
		t.Fatalf("expected at least one hybrid row with engine_config_summary; stdout=%q", res.stdout)
	}
}
