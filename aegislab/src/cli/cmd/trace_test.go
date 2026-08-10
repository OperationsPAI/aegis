package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	runErr := fn()

	w.Close()
	os.Stdout = oldStdout

	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	return string(out), runErr
}

func TestParseTraceColumns_Valid(t *testing.T) {
	cols, err := parseTraceColumns("id,state,last_event")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 3 || cols[0] != "id" || cols[1] != "state" || cols[2] != "last_event" {
		t.Fatalf("unexpected columns: %v", cols)
	}
}

func TestParseTraceColumns_Invalid(t *testing.T) {
	_, err := parseTraceColumns("id,bogus,state")
	if err == nil {
		t.Fatal("expected error for invalid column")
	}
	msg := err.Error()
	if !strings.Contains(msg, `invalid column "bogus"`) {
		t.Fatalf("error missing invalid column name: %v", err)
	}
	// Must enumerate at least a couple of valid columns so users can self-serve.
	for _, must := range []string{"id", "state", "last_event", "final_event", "created_at", "project"} {
		if !strings.Contains(msg, must) {
			t.Fatalf("error message missing valid column %q: %v", must, err)
		}
	}
}

func TestTraceListCmd_TSVFormat(t *testing.T) {
	oldServer, oldToken, oldProject, oldOutput := flagServer, flagToken, flagProject, flagOutput
	oldFormat, oldColumns := traceListFormat, traceListColumns
	defer func() {
		flagServer, flagToken, flagProject, flagOutput = oldServer, oldToken, oldProject, oldOutput
		traceListFormat, traceListColumns = oldFormat, oldColumns
	}()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/traces" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"items": []map[string]any{
					{"id": "trace-1", "state": "Running", "last_event": "fault.injection.started"},
					{"id": "trace-2", "state": "Completed", "last_event": "datapack.no_anomaly"},
				},
				"pagination": map[string]any{"page": 1, "size": 20, "total": 2, "total_pages": 1},
			},
		})
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "test-token"
	flagProject = ""
	flagOutput = ""
	traceListFormat = "tsv"
	traceListColumns = "id,state"

	out, err := captureStdout(t, func() error { return traceListCmd.RunE(traceListCmd, nil) })
	if err != nil {
		t.Fatalf("traceListCmd: %v", err)
	}

	want := "ID\tSTATE\ntrace-1\tRunning\ntrace-2\tCompleted\n"
	if out != want {
		t.Fatalf("TSV output mismatch.\nwant: %q\ngot:  %q", want, out)
	}
}

func TestTraceListCmd_InvalidColumns(t *testing.T) {
	oldServer, oldToken, oldOutput := flagServer, flagToken, flagOutput
	oldFormat, oldColumns := traceListFormat, traceListColumns
	defer func() {
		flagServer, flagToken, flagOutput = oldServer, oldToken, oldOutput
		traceListFormat, traceListColumns = oldFormat, oldColumns
	}()

	// The HTTP call must NEVER be made when columns are invalid.
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.NotFound(w, r)
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "test-token"
	flagOutput = ""
	traceListFormat = "tsv"
	traceListColumns = "id,bogus"

	err := traceListCmd.RunE(traceListCmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid column")
	}
	if !strings.Contains(err.Error(), `invalid column "bogus"`) {
		t.Fatalf("expected clear invalid-column error, got %v", err)
	}
	if called {
		t.Fatal("HTTP endpoint must not be called when column validation fails")
	}
}

func TestTraceCancelCmd_PostsAndReports(t *testing.T) {
	oldServer, oldToken, oldOutput := flagServer, flagToken, flagOutput
	oldForce, oldStdout := traceCancelForce, traceCancelStdout
	defer func() {
		flagServer, flagToken, flagOutput = oldServer, oldToken, oldOutput
		traceCancelForce, traceCancelStdout = oldForce, oldStdout
	}()

	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"trace_id":         "trace-xyz",
				"state":            "Cancelled",
				"cancelled_tasks":  []string{"task-a", "task-b"},
				"deleted_podchaos": []string{"podchaos/trace-xyz-0"},
			},
		})
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "test-token"
	flagOutput = "table"
	traceCancelForce = true // skip confirmation

	// Capture the human-readable output the command writes.
	var buf bytes.Buffer
	traceCancelStdout = &buf
	defer func() { traceCancelStdout = os.Stdout }()

	if err := traceCancelCmd.RunE(traceCancelCmd, []string{"trace-xyz"}); err != nil {
		t.Fatalf("traceCancelCmd: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v2/traces/trace-xyz/cancel" {
		t.Fatalf("unexpected cancel URL: %s", gotPath)
	}

	got := buf.String()
	for _, want := range []string{
		"Trace trace-xyz: Cancelled",
		"cancelled tasks: task-a, task-b",
		"deleted PodChaos: podchaos/trace-xyz-0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("cancel output missing %q; got:\n%s", want, got)
		}
	}
}

func TestTraceCancelCmd_EndpointMissing(t *testing.T) {
	oldServer, oldToken, oldForce := flagServer, flagToken, traceCancelForce
	oldStdout := traceCancelStdout
	defer func() {
		flagServer, flagToken, traceCancelForce = oldServer, oldToken, oldForce
		traceCancelStdout = oldStdout
	}()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    404,
			"message": "not found",
		})
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "test-token"
	traceCancelForce = true
	traceCancelStdout = new(bytes.Buffer)

	err := traceCancelCmd.RunE(traceCancelCmd, []string{"trace-xyz"})
	if err == nil {
		t.Fatal("expected error when endpoint returns 404")
	}
	if !strings.Contains(err.Error(), "cancel endpoint not implemented yet") {
		t.Fatalf("expected endpoint-missing hint, got %v", err)
	}
}
