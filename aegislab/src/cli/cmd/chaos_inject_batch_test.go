package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeChildrenFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "children.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write children file: %v", err)
	}
	return p
}

// TestChaosInjectBatchSubmit_ShapeMatches asserts the SDK serialises
// children + batch_idempotency_key + batch_caller_metadata into the wire
// envelope the server-side handler binds against, and that the response's
// child list round-trips through the renderer without panic.
func TestChaosInjectBatchSubmit_ShapeMatches(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"code":202,"message":"Batch accepted","data":{` +
			`"id":"01H_BATCH","idempotency_key":"b-1","aggregated_status":"running",` +
			`"ts":"2026-05-19T00:00:00Z","started_at":"2026-05-19T00:00:00Z",` +
			`"children":[` +
			`{"id":"01H_C1","point_id":"p1","status":"running","executor_name":"chaos-mesh","executor_handle":"h1","idempotency_key":"c1"},` +
			`{"id":"01H_C2","point_id":"p2","status":"running","executor_name":"chaos-mesh","executor_handle":"h2","idempotency_key":"c2"}` +
			`]}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	chaosBatchChildrenFile = writeChildrenFile(t, `{"children":[
		{"point_id":"p1","idempotency_key":"c1","params":{"duration_s":5}},
		{"point_id":"p2","idempotency_key":"c2"}
	]}`)
	chaosBatchIdemKey = "b-1"
	chaosBatchCallerMeta = `{"task_id":"t-1"}`

	if err := runChaosBatchSubmit(nil, nil); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", gotMethod)
	}
	if gotPath != "/v1beta/injection-batches" {
		t.Errorf("path = %q; want /v1beta/injection-batches", gotPath)
	}
	if gotBody["batch_idempotency_key"] != "b-1" {
		t.Errorf("batch_idempotency_key = %v; want b-1", gotBody["batch_idempotency_key"])
	}
	bcm, _ := gotBody["batch_caller_metadata"].(map[string]any)
	if bcm["task_id"] != "t-1" {
		t.Errorf("batch_caller_metadata.task_id = %v; want t-1", bcm["task_id"])
	}
	children, _ := gotBody["children"].([]any)
	if len(children) != 2 {
		t.Fatalf("children len = %d; want 2", len(children))
	}
	c0, _ := children[0].(map[string]any)
	if c0["point_id"] != "p1" || c0["idempotency_key"] != "c1" {
		t.Errorf("child[0] missing fields: %v", c0)
	}
	if p, _ := c0["params"].(map[string]any); p["duration_s"] != float64(5) {
		t.Errorf("child[0].params.duration_s = %v; want 5", p["duration_s"])
	}
}

// TestChaosInjectBatchSubmit_RequiresChildrenFile guards the precondition
// check — empty --children-file must fail before the SDK call so we don't
// silently POST an empty envelope.
func TestChaosInjectBatchSubmit_RequiresChildrenFile(t *testing.T) {
	chaosTestSetup(t, "http://127.0.0.1:1")
	defer resetChaosFlags()
	chaosBatchIdemKey = "b-1"
	if err := runChaosBatchSubmit(nil, nil); err == nil {
		t.Fatal("expected error when --children-file missing")
	}
}

// TestChaosInjectBatchGet_ResponseShape asserts GET /injection-batches/{id}
// is the path and the renderer parses the batch + children list.
func TestChaosInjectBatchGet_ResponseShape(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{` +
			`"id":"01H_BATCH","idempotency_key":"b-1","aggregated_status":"succeeded",` +
			`"ts":"2026-05-19T00:00:00Z","finished_at":"2026-05-19T00:01:00Z",` +
			`"children":[{"id":"01H_C1","point_id":"p1","status":"succeeded","executor_name":"chaos-mesh","executor_handle":"h1","idempotency_key":"c1"}]}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	if err := runChaosBatchGet(nil, []string{"01H_BATCH"}); err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q; want GET", gotMethod)
	}
	if gotPath != "/v1beta/injection-batches/01H_BATCH" {
		t.Errorf("path = %q; want /v1beta/injection-batches/01H_BATCH", gotPath)
	}
}

// TestChaosInjectBatchDestroy_HappyPath asserts DELETE on
// /injection-batches/{id}.
func TestChaosInjectBatchDestroy_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{` +
			`"id":"01H_BATCH","idempotency_key":"b-1","aggregated_status":"cancelled",` +
			`"ts":"2026-05-19T00:00:00Z","finished_at":"2026-05-19T00:01:00Z",` +
			`"children":[]}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	if err := runChaosBatchDestroy(nil, []string{"01H_BATCH"}); err != nil {
		t.Fatalf("destroy failed: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q; want DELETE", gotMethod)
	}
	if gotPath != "/v1beta/injection-batches/01H_BATCH" {
		t.Errorf("path = %q; want /v1beta/injection-batches/01H_BATCH", gotPath)
	}
}
