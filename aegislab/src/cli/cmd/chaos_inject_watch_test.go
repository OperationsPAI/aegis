package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aegis/cli/config"
)

// TestChaosInjectWatch_PrintsAllEventsAndExitsZero stubs an SSE source
// that emits three events (pending → running → succeeded[terminal]) and
// asserts the watch command prints them and returns nil (exit 0).
func TestChaosInjectWatch_PrintsAllEventsAndExitsZero(t *testing.T) {
	oldChaos, oldToken, oldOutput, oldQuiet, oldTimeout, oldCfg :=
		flagChaosServer, flagToken, flagOutput, flagQuiet, chaosInjectWatchTimeout, cfg
	defer func() {
		flagChaosServer, flagToken, flagOutput, flagQuiet, chaosInjectWatchTimeout, cfg =
			oldChaos, oldToken, oldOutput, oldQuiet, oldTimeout, oldCfg
	}()
	cfg = &config.Config{Contexts: map[string]config.Context{}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		ts := "2026-05-20T00:00:00Z"
		fmt.Fprintf(w, "data: {\"injection_id\":\"inj1\",\"status\":\"pending\",\"emitted_at\":\"%s\",\"attempt\":1}\n\n", ts)
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"injection_id\":\"inj1\",\"status\":\"running\",\"emitted_at\":\"%s\",\"attempt\":2}\n\n", ts)
		flusher.Flush()
		fmt.Fprintf(w, "event: terminal\ndata: {\"injection_id\":\"inj1\",\"status\":\"succeeded\",\"emitted_at\":\"%s\",\"attempt\":3}\n\n", ts)
		flusher.Flush()
	}))
	defer srv.Close()

	flagChaosServer = srv.URL
	flagToken = "test-token"
	flagOutput = "table"
	flagQuiet = false
	chaosInjectWatchTimeout = 10 * time.Second

	got, err := captureStdout(t, func() error {
		return runChaosInjectWatch(nil, []string{"inj1"})
	})
	if err != nil {
		t.Fatalf("watch returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 printed lines, got %d:\n%s", len(lines), got)
	}
	for i, want := range []string{"status=pending", "status=running", "status=succeeded"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d: missing %q in %q", i, want, lines[i])
		}
	}
	if !strings.Contains(lines[2], "[terminal]") {
		t.Errorf("terminal line should be tagged; got %q", lines[2])
	}
}

