package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// traceDetailFixture builds the minimum `GET /api/v2/traces/{id}` response
// shape the next-task resolver cares about: a `tasks` array with id/state and
// optional execute_time for ordering. Matches module/trace/api_types.go
// TraceDetailResp.Tasks = []task.TaskResp.
func traceDetailFixture(tasks []map[string]any) map[string]any {
	return map[string]any{
		"code":    200,
		"message": "success",
		"data": map[string]any{
			"id":    "trace-xyz",
			"state": "Running",
			"tasks": tasks,
		},
	}
}

func withServer(t *testing.T, handler http.HandlerFunc) (restore func()) {
	t.Helper()
	ts := httptest.NewServer(handler)

	oldServer, oldToken, oldOutput := flagServer, flagToken, flagOutput
	flagServer = ts.URL
	flagToken = "test-token"
	flagOutput = ""

	return func() {
		ts.Close()
		flagServer, flagToken, flagOutput = oldServer, oldToken, oldOutput
	}
}

func TestResolveNextPendingTask_PicksEarliestPending(t *testing.T) {
	restore := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/traces/trace-xyz" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(traceDetailFixture([]map[string]any{
			{"id": "task-done", "type": "FaultInjection", "state": "Completed", "execute_time": 100},
			{"id": "task-late", "type": "BuildDatapack", "state": "Pending", "execute_time": 300},
			{"id": "task-early", "type": "BuildDatapack", "state": "Pending", "execute_time": 200},
		}))
	})
	defer restore()

	task, err := resolveNextPendingTask("trace-xyz")
	if err != nil {
		t.Fatalf("resolveNextPendingTask: %v", err)
	}
	if task.ID != "task-early" {
		t.Fatalf("expected earliest pending task-early, got %q", task.ID)
	}
}

func TestResolveNextPendingTask_NoPendingReturnsNotFoundExit(t *testing.T) {
	restore := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(traceDetailFixture([]map[string]any{
			{"id": "task-a", "state": "Completed"},
			{"id": "task-b", "state": "Running"},
		}))
	})
	defer restore()

	_, err := resolveNextPendingTask("trace-xyz")
	if err == nil {
		t.Fatal("expected error when no pending task")
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != ExitCodeNotFound {
		t.Fatalf("expected ExitCodeNotFound, got err=%v", err)
	}
}

func TestTraceNextTaskCmd_StdoutScriptable(t *testing.T) {
	restore := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(traceDetailFixture([]map[string]any{
			{"id": "task-run", "type": "BuildDatapack", "state": "Running"},
			{"id": "task-next", "type": "RunAlgorithm", "state": "Pending", "execute_time": 500},
		}))
	})
	defer restore()

	var stdout, stderr bytes.Buffer
	oldOut, oldErr := traceNextTaskStdout, traceNextTaskStderr
	traceNextTaskStdout = &stdout
	traceNextTaskStderr = &stderr
	defer func() { traceNextTaskStdout = oldOut; traceNextTaskStderr = oldErr }()

	if err := traceNextTaskCmd.RunE(traceNextTaskCmd, []string{"trace-xyz"}); err != nil {
		t.Fatalf("traceNextTaskCmd: %v", err)
	}

	// stdout MUST contain just the bare task id followed by a newline —
	// anything else breaks `ID=$(aegisctl trace next-task ...)` scripting.
	if got := stdout.String(); got != "task-next\n" {
		t.Fatalf("stdout = %q, want %q", got, "task-next\n")
	}
	if !strings.Contains(stderr.String(), "task-next") {
		t.Fatalf("stderr should carry info message, got %q", stderr.String())
	}
}

func TestTraceNextTaskCmd_NoPendingExitCode(t *testing.T) {
	restore := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(traceDetailFixture([]map[string]any{
			{"id": "task-a", "state": "Completed"},
		}))
	})
	defer restore()

	err := traceNextTaskCmd.RunE(traceNextTaskCmd, []string{"trace-xyz"})
	if err == nil {
		t.Fatal("expected error when no pending task")
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != ExitCodeNotFound {
		t.Fatalf("expected ExitCodeNotFound, got err=%v", err)
	}
}

func TestTraceExpediteCmd_ResolvesThenPostsExpedite(t *testing.T) {
	var gotMethod, gotPath string
	restore := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/traces/trace-xyz":
			_ = json.NewEncoder(w).Encode(traceDetailFixture([]map[string]any{
				{"id": "task-pend", "type": "RunAlgorithm", "state": "Pending", "execute_time": 123},
			}))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v2/tasks/"):
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data":    map[string]any{"task_id": "task-pend"},
			})
		default:
			http.NotFound(w, r)
		}
	})
	defer restore()

	if err := traceExpediteCmd.RunE(traceExpediteCmd, []string{"trace-xyz"}); err != nil {
		t.Fatalf("traceExpediteCmd: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST to task expedite, got method=%q", gotMethod)
	}
	if gotPath != "/api/v2/tasks/task-pend/expedite" {
		t.Fatalf("unexpected expedite URL: %s", gotPath)
	}
}

func TestTraceExpediteCmd_NoPendingDoesNotPost(t *testing.T) {
	posted := false
	restore := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		if r.URL.Path == "/api/v2/traces/trace-xyz" {
			_ = json.NewEncoder(w).Encode(traceDetailFixture([]map[string]any{
				{"id": "task-a", "state": "Completed"},
			}))
			return
		}
		http.NotFound(w, r)
	})
	defer restore()

	err := traceExpediteCmd.RunE(traceExpediteCmd, []string{"trace-xyz"})
	if err == nil {
		t.Fatal("expected error when no pending task")
	}
	if posted {
		t.Fatal("expedite MUST NOT POST when there is no pending task")
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != ExitCodeNotFound {
		t.Fatalf("expected ExitCodeNotFound, got err=%v", err)
	}
}
