package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFormatWait(t *testing.T) {
	cases := []struct {
		name     string
		delta    int64
		expected string
	}{
		{"due_now", 0, "+00:00"},
		{"future_small", 83, "+01:23"},
		{"overdue_small", -5, "-00:05"},
		{"future_large", 3661, "+61:01"},
		{"overdue_large", -125, "-02:05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatWait(tc.delta)
			if got != tc.expected {
				t.Fatalf("formatWait(%d) = %q, want %q", tc.delta, got, tc.expected)
			}
		})
	}
}

func TestExecTimeField(t *testing.T) {
	if got := execTimeField(map[string]any{"execute_time": float64(1234567890)}); got != 1234567890 {
		t.Fatalf("float64 path: got %d", got)
	}
	if got := execTimeField(map[string]any{"execute_time": int64(42)}); got != 42 {
		t.Fatalf("int64 path: got %d", got)
	}
	if got := execTimeField(map[string]any{"execute_time": 7}); got != 7 {
		t.Fatalf("int path: got %d", got)
	}
	if got := execTimeField(map[string]any{}); got != 0 {
		t.Fatalf("missing key: got %d", got)
	}
	if got := execTimeField(map[string]any{"execute_time": nil}); got != 0 {
		t.Fatalf("nil value: got %d", got)
	}
	if got := execTimeField(map[string]any{"execute_time": "nope"}); got != 0 {
		t.Fatalf("unknown type: got %d", got)
	}
}

func TestTaskListJSONEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/tasks" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"items": []map[string]any{
					{"id": "task-1", "state": "Running", "type": "FaultInjection"},
					{"id": "task-2", "state": "Error", "type": "RestartPedestal"},
				},
				"pagination": map[string]any{"page": 1, "size": 100, "total": 2, "total_pages": 1},
			},
		})
	}))
	defer ts.Close()

	res := runCLI(t, "task", "list",
		"--server", ts.URL, "--token", "tok", "--output", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want %d; stderr=%q stdout=%q", res.code, ExitCodeSuccess, res.stderr, res.stdout)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout should be JSON object: %v; got %q", err, res.stdout)
	}
	if _, ok := payload["items"]; !ok {
		t.Fatalf("missing items key in payload: %v", payload)
	}
	if _, ok := payload["pagination"]; !ok {
		t.Fatalf("missing pagination key in payload: %v", payload)
	}
	items, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("items is %T, want []any", payload["items"])
	}
	if len(items) != 2 {
		t.Fatalf("items length = %d, want 2", len(items))
	}
}
