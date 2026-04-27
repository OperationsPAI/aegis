package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRateLimiterGCDryRunBehaviorWithoutForce(t *testing.T) {
	requested := make([]string, 0, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v2/rate-limiters":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"bucket":   "restart_service",
							"key":      "token_bucket:restart_service",
							"capacity": 8,
							"held":     2,
							"holders": []map[string]any{
								{"task_id": "t1", "task_state": "terminal", "is_terminal": true},
							},
						},
					},
				},
			})
		case "/api/v2/rate-limiters/gc":
			t.Fatalf("unexpected mutation request: %s %s", r.Method, r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cases := [][]string{
		{"rate-limiter", "gc", "--server", srv.URL, "--token", "tok"},
		{"rate-limiter", "gc", "--dry-run", "--server", srv.URL, "--token", "tok"},
	}

	for _, args := range cases {
		requested = requested[:0]
		res := runCLI(t, args...)
		if res.code != ExitCodeSuccess {
			t.Fatalf("exit = %d, want %d", res.code, ExitCodeSuccess)
		}
		for _, req := range requested {
			if req == "POST /api/v2/rate-limiters/gc" {
				t.Fatalf("mutation request observed without --force")
			}
		}
		if !strings.Contains(res.stdout, "restart_service") {
			t.Fatalf("stdout missing bucket preview; got %q", res.stdout)
		}
		if !strings.Contains(res.stderr, "Use --force to execute cleanup") {
			t.Fatalf("stderr missing plan hint; got %q", res.stderr)
		}
	}
}
