package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExitCodeContractMatrix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v2/projects":
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode(map[string]any{
					"code":    200,
					"message": "ok",
					"data": map[string]any{
						"items": []any{map[string]any{"id": 7, "name": "demo"}},
						"pagination": map[string]any{
							"page": 1, "size": 100, "total": 1, "total_pages": 1,
						},
					},
				})
				return
			}
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]any{"code": 409, "message": "project exists", "data": nil})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case "/api/v2/containers":
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "unauthorized", "data": nil})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case "/api/v2/projects/7/injections":
			if r.Method == http.MethodGet {
				if r.URL.Query().Get("size") == "500" {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]any{"code": 400, "message": "invalid size", "data": nil})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"code":    200,
					"message": "ok",
					"data": map[string]any{
						"items": []any{map[string]any{"id": 42, "name": "bogus"}},
						"pagination": map[string]any{
							"page": 1, "size": 100, "total": 1, "total_pages": 1,
						},
					},
				})
				return
			}
		case "/api/v2/projects/7/injections/search":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"code": 500, "message": "temporary fail", "data": nil})
				return
			}
		case "/api/v2/injections/42":
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]any{"code": 404, "message": "not found", "data": nil})
				return
			}
		case "/api/v2/projects/7/executions":
			if r.Method == http.MethodGet {
				// Return a successful but non-decoder-valid envelope to exercise 11.
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("not-json"))
				return
			}
		}

		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"code": 404, "message": "not found", "data": nil})
	}))
	defer ts.Close()

	baseArgs := []string{"--server", ts.URL, "--token", "token"}
	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "inject list oversized page size",
			args: append(baseArgs, "inject", "list", "--project", "demo", "--size", "500"),
			want: ExitCodeUsage,
		},
		{
			name: "containers require auth",
			args: append(baseArgs, "container", "list"),
			want: ExitCodeAuthFailure,
		},
		{
			name: "inject get resolves name then maps 404",
			args: append(baseArgs, "inject", "get", "bogus", "--project", "demo"),
			want: ExitCodeNotFound,
		},
		{
			name: "project create conflict",
			args: append(baseArgs, "project", "create", "--name", "demo"),
			want: ExitCodeConflict,
		},
		{
			name: "inject search internal server error",
			args: append(baseArgs, "inject", "search", "--project", "demo"),
			want: ExitCodeServerError,
		},
		{
			name: "execute list payload decode failure",
			args: append(baseArgs, "execute", "list", "--project", "demo"),
			want: ExitCodeDecodeFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runCLI(t, tt.args...)
			if res.code != tt.want {
				t.Fatalf("exit code = %d, want %d; stderr=%q stdout=%q", res.code, tt.want, res.stderr, res.stdout)
			}
		})
	}
}
