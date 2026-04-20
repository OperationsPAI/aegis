package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRegressionCaseByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.yaml")
	if err := os.WriteFile(path, []byte(`name: sample
project_name: pair_diagnosis
submit:
  specs:
    - - chaos_type: PodKill
        duration: 1
validation:
  expected_final_event: datapack.result.collection
  required_task_chain:
    - RestartPedestal
`), 0o644); err != nil {
		t.Fatalf("write case: %v", err)
	}

	rc, gotPath, err := loadRegressionCaseByName(dir, "sample")
	if err != nil {
		t.Fatalf("loadRegressionCaseByName: %v", err)
	}
	if rc.Name != "sample" {
		t.Fatalf("expected case name sample, got %q", rc.Name)
	}
	if filepath.Base(gotPath) != "sample.yaml" {
		t.Fatalf("expected resolved file path, got %q", gotPath)
	}
}

func TestLoadRegressionCaseParseFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("name: broken\nsubmit: [\n"), 0o644); err != nil {
		t.Fatalf("write case: %v", err)
	}

	_, _, err := loadRegressionCaseFile(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse regression case") {
		t.Fatalf("expected clear parse error, got %v", err)
	}
}

func TestLoadRegressionCaseValidationFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(path, []byte(`name: invalid
project_name: pair_diagnosis
submit:
  specs: []
validation:
  required_task_chain: []
`), 0o644); err != nil {
		t.Fatalf("write case: %v", err)
	}

	_, _, err := loadRegressionCaseFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "validation.expected_final_event") {
		t.Fatalf("expected clear validation error, got %v", err)
	}
}

func TestRegressionRunCommandLoadsAndExecutesNamedCase(t *testing.T) {
	oldServer := flagServer
	oldToken := flagToken
	oldProject := flagProject
	oldOutput := flagOutput
	oldCasesDir := regressionCasesDir
	oldCaseFile := regressionCaseFile
	defer func() {
		flagServer = oldServer
		flagToken = oldToken
		flagProject = oldProject
		flagOutput = oldOutput
		regressionCasesDir = oldCasesDir
		regressionCaseFile = oldCaseFile
	}()

	traceID := "trace-123"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/projects" && r.URL.RawQuery == "page=1&size=100":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items":      []map[string]any{{"id": 7, "name": "pair_diagnosis"}},
					"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
				},
			})
		case r.URL.Path == "/api/v2/projects/7/injections/inject":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"group_id": "group-1",
					"items":    []map[string]any{{"index": 0, "trace_id": traceID, "task_id": "task-1"}},
				},
			})
		case r.URL.Path == "/api/v2/traces/"+traceID+"/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer is not a flusher")
			}
			for _, evt := range []string{
				"restart.pedestal.started",
				"fault.injection.started",
				"datapack.build.started",
				"algorithm.run.started",
				"algorithm.run.succeed",
				"datapack.no_anomaly",
			} {
				_, _ = fmt.Fprintf(w, "event: update\ndata: {\"event_name\":%q,\"payload\":\"ok\"}\n\n", evt)
				flusher.Flush()
			}
			_, _ = fmt.Fprint(w, "event: end\ndata: done\n\n")
			flusher.Flush()
		case r.URL.Path == "/api/v2/traces/"+traceID:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"trace_id": traceID,
					"tasks": []map[string]any{
						{"type": "RestartPedestal"},
						{"type": "FaultInjection"},
						{"type": "BuildDatapack"},
						{"type": "RunAlgorithm"},
						{"type": "CollectResult"},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	casesDir := t.TempDir()
	casePath := filepath.Join(casesDir, "smoke.yaml")
	if err := os.WriteFile(casePath, []byte(`name: smoke
project_name: pair_diagnosis
submit:
  pedestal:
    name: otel-demo
    version: "1.0.0"
  benchmark:
    name: clickhouse
    version: "1.0.0"
  interval: 2
  pre_duration: 1
  specs:
    - - system: otel-demo
        system_type: otel-demo
        namespace: otel-demo
        app: frontend
        chaos_type: PodKill
        duration: 1
validation:
  timeout_seconds: 5
  min_events: 6
  expected_final_event: datapack.no_anomaly
  required_events:
    - restart.pedestal.started
    - fault.injection.started
    - datapack.build.started
    - algorithm.run.started
    - datapack.no_anomaly
  required_task_chain:
    - RestartPedestal
    - FaultInjection
    - BuildDatapack
    - RunAlgorithm
    - CollectResult
`), 0o644); err != nil {
		t.Fatalf("write case: %v", err)
	}

	flagServer = ts.URL
	flagToken = ""
	flagProject = ""
	flagOutput = "json"
	regressionCasesDir = casesDir
	regressionCaseFile = ""

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := regressionRunCmd.RunE(regressionRunCmd, []string{"smoke"})

	w.Close()
	os.Stdout = oldStdout
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read command output: %v", readErr)
	}
	if err != nil {
		t.Fatalf("regressionRunCmd.RunE: %v", err)
	}

	var summary regressionSummary
	if err := json.Unmarshal(out, &summary); err != nil {
		t.Fatalf("expected JSON summary, got %q (%v)", string(out), err)
	}
	if summary.CaseName != "smoke" {
		t.Fatalf("expected case name smoke, got %q", summary.CaseName)
	}
	if summary.TraceID != traceID {
		t.Fatalf("expected trace id %q, got %q", traceID, summary.TraceID)
	}
	if summary.FinalEvent != "datapack.no_anomaly" {
		t.Fatalf("expected final event datapack.no_anomaly, got %q", summary.FinalEvent)
	}
}
