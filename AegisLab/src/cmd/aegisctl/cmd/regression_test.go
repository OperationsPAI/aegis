package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunRegressionCaseWaitSuccess(t *testing.T) {
	restore := snapshotRegressionGlobals()
	defer restore()

	var traceGets int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
			require.Equal(t, "1", r.URL.Query().Get("page"))
			require.Equal(t, "100", r.URL.Query().Get("size"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items":      []map[string]any{{"id": 42, "name": "pair_diagnosis"}},
					"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/42/injections/inject":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, float64(4), body["interval"])
			require.Equal(t, float64(1), body["pre_duration"])
			specs := body["specs"].([]any)
			firstBatch := specs[0].([]any)
			firstSpec := firstBatch[0].(map[string]any)
			require.Equal(t, "NetworkDelay", firstSpec["chaos_type"])
			require.Equal(t, "cart", firstSpec["app"])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"group_id": "group-1",
					"items": []map[string]any{{
						"index":    0,
						"trace_id": "trace-123",
						"task_id":  "task-root",
					}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/traces/trace-123":
			call := atomic.AddInt32(&traceGets, 1)
			if call == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    200,
					"message": "success",
					"data": map[string]any{
						"id":         "trace-123",
						"state":      "Running",
						"last_event": "fault.injection.started",
						"tasks":      []any{},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"id":         "trace-123",
					"state":      "Completed",
					"last_event": "datapack.no_anomaly",
					"tasks": []map[string]any{
						{"task_id": "t1", "type": "RestartPedestal", "state": "Completed"},
						{"task_id": "t2", "type": "FaultInjection", "state": "Completed"},
						{"task_id": "t3", "type": "BuildDatapack", "state": "Completed"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "token"
	flagProject = ""
	flagOutput = "json"
	flagRequestTimeout = 1

	summary, exitCode, err := runRegressionCase(context.Background(), "otel-demo-guided", regressionRunOptions{
		Wait:         true,
		WaitTimeout:  2 * time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	require.NoError(t, err)
	require.Equal(t, 0, exitCode)
	require.Equal(t, regressionOutcomePass, summary.Outcome)
	require.Equal(t, "trace-123", summary.TraceID)
	require.Equal(t, "Completed", summary.TraceState)
	require.Equal(t, "datapack.no_anomaly", summary.FinalEvent)
	require.Len(t, summary.TaskStates, 3)
	require.Equal(t, "pair_diagnosis", regressionCases["otel-demo-guided"].DefaultProject)
}

func TestRunRegressionCaseAuthFailure(t *testing.T) {
	restore := snapshotRegressionGlobals()
	defer restore()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    401,
			"message": "unauthorized",
		})
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "bad-token"
	flagProject = ""
	flagOutput = "json"
	flagRequestTimeout = 1

	summary, exitCode, err := runRegressionCase(context.Background(), "otel-demo-guided", regressionRunOptions{})
	require.NoError(t, err)
	require.Equal(t, regressionExitAuthFailure, exitCode)
	require.Equal(t, regressionOutcomeFail, summary.Outcome)
	require.Equal(t, regressionErrCategoryAuth, summary.ErrorCategory)
	require.Contains(t, summary.Message, "401")
}

func snapshotRegressionGlobals() func() {
	oldServer := flagServer
	oldToken := flagToken
	oldProject := flagProject
	oldOutput := flagOutput
	oldTimeout := flagRequestTimeout
	return func() {
		flagServer = oldServer
		flagToken = oldToken
		flagProject = oldProject
		flagOutput = oldOutput
		flagRequestTimeout = oldTimeout
	}
}
