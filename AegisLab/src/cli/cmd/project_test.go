package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// projectTestServer returns an httptest server that serves a two-project list
// and by-id lookups. It also records any non-GET request paths in `writes`.
func projectTestServer(t *testing.T, writes *[]string) *httptest.Server {
	t.Helper()
	listResp := map[string]any{
		"code":    200,
		"message": "success",
		"data": map[string]any{
			"items": []map[string]any{
				{"id": 42, "name": "pair_diagnosis", "description": "d1", "status": "active", "created_at": "2026-01-01"},
				{"id": 7, "name": "other", "description": "d2", "status": "active", "created_at": "2026-01-02"},
			},
			"pagination": map[string]any{"page": 1, "size": 100, "total": 2, "total_pages": 1},
		},
	}
	byID := map[string]map[string]any{
		"/api/v2/projects/42": {
			"code": 200, "message": "success",
			"data": map[string]any{
				"id": 42, "name": "pair_diagnosis", "description": "d1", "status": "active",
				"created_at": "2026-01-01", "updated_at": "2026-01-02",
			},
		},
		"/api/v2/projects/7": {
			"code": 200, "message": "success",
			"data": map[string]any{
				"id": 7, "name": "other", "description": "d2", "status": "active",
				"created_at": "2026-01-02", "updated_at": "2026-01-03",
			},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && writes != nil {
			*writes = append(*writes, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(listResp)
		case r.Method == http.MethodGet:
			if body, ok := byID[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(body)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": map[string]any{}})
		}
	}))
}

func TestProjectResolveByName(t *testing.T) {
	ts := projectTestServer(t, nil)
	defer ts.Close()

	res := runCLI(t, "project", "resolve", "pair_diagnosis",
		"--server", ts.URL, "--token", "tok", "--output", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout should be JSON: %v; got %q", err, res.stdout)
	}
	// JSON numbers decode as float64.
	if id, _ := payload["id"].(float64); int(id) != 42 {
		t.Fatalf("id = %v, want 42", payload["id"])
	}
	if payload["name"] != "pair_diagnosis" {
		t.Fatalf("name = %v, want pair_diagnosis", payload["name"])
	}
}

func TestProjectResolveById(t *testing.T) {
	ts := projectTestServer(t, nil)
	defer ts.Close()

	res := runCLI(t, "project", "resolve", "42",
		"--server", ts.URL, "--token", "tok", "--output", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout should be JSON: %v; got %q", err, res.stdout)
	}
	if payload["name"] != "pair_diagnosis" {
		t.Fatalf("name = %v, want pair_diagnosis", payload["name"])
	}
	if id, _ := payload["id"].(float64); int(id) != 42 {
		t.Fatalf("id = %v, want 42", payload["id"])
	}
}

func TestProjectResolveNotFound(t *testing.T) {
	ts := projectTestServer(t, nil)
	defer ts.Close()

	res := runCLI(t, "project", "resolve", "does-not-exist",
		"--server", ts.URL, "--token", "tok", "--output", "json")
	if res.code != ExitCodeNotFound {
		t.Fatalf("exit = %d, want %d; stderr=%q", res.code, ExitCodeNotFound, res.stderr)
	}
}

func TestProjectDeleteRequiresYesInNonInteractive(t *testing.T) {
	var writes []string
	ts := projectTestServer(t, &writes)
	defer ts.Close()

	res := runCLI(t, "project", "delete", "pair_diagnosis",
		"--server", ts.URL, "--token", "tok", "--non-interactive")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--yes") {
		t.Fatalf("stderr = %q, want --yes diagnostic", res.stderr)
	}
	for _, w := range writes {
		if strings.HasPrefix(w, "DELETE ") {
			t.Fatalf("no DELETE should be issued, got: %v", writes)
		}
	}
}

func TestProjectUpdateDryRun(t *testing.T) {
	var patchCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			atomic.AddInt32(&patchCount, 1)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{
					"items": []map[string]any{
						{"id": 42, "name": "pair_diagnosis", "description": "old", "status": "active"},
					},
					"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/42":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "message": "success",
				"data": map[string]any{
					"id": 42, "name": "pair_diagnosis", "description": "old", "status": "active",
				},
			})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": map[string]any{}})
		}
	}))
	defer ts.Close()

	res := runCLI(t, "project", "update", "pair_diagnosis",
		"--description", "new-desc",
		"--server", ts.URL, "--token", "tok", "--output", "json", "--dry-run")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if got := atomic.LoadInt32(&patchCount); got != 0 {
		t.Fatalf("PATCH should not be issued in dry-run mode, got %d calls", got)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout should be JSON: %v; got %q", err, res.stdout)
	}
	if payload["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", payload["dry_run"])
	}
}
