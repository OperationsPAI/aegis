package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContainerBuildNonInteractiveDoesNotTriggerBuildWithoutForce(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/containers/build" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data":    map[string]any{"status": "triggered"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res := runCLI(t, "container", "build", "worker", "--non-interactive", "--server", srv.URL, "--token", "tok")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want %d", res.code, ExitCodeSuccess)
	}
	if requestCount != 0 {
		t.Fatalf("expected no request, got %d", requestCount)
	}
	if !strings.Contains(res.stderr, "Refusing to build without --force in non-interactive mode") {
		t.Fatalf("stderr missing non-interactive refusal; got %q", res.stderr)
	}
}
