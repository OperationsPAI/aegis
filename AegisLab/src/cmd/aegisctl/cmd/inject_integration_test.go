package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectGetByNameAndIdAndDownloadAtomicFailure(t *testing.T) {
	const (
		projectName   = "pair_diagnosis"
		projectID     = 7
		injectionID   = 744
		injectionName = "otel-demo23-recommendation-pod-failure-4t2mpb"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /api/v2/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{"id": projectID, "name": projectName},
					},
					"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
				},
			})
		case http.MethodGet + " /api/v2/projects/7/injections":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{"id": injectionID, "name": injectionName},
					},
					"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
				},
			})
		case http.MethodGet + " /api/v2/injections/744":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data": map[string]any{
					"id":    injectionID,
					"name":  injectionName,
					"state": "build_success",
				},
			})
		case http.MethodGet + " /api/v2/injections/744/files":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data": []map[string]any{
					{"path": "raw/demo.log", "size": "10", "type": "raw"},
				},
			})
		case http.MethodGet + " /api/v2/injections/744/download":
			// Intentionally signal a shorter body than declared to mimic a broken
			// transport and exercise partial-output cleanup logic.
			w.Header().Set("Content-Length", "32")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("broken transfer"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "message": "not found"})
		}
	}))
	defer server.Close()

	commonArgs := []string{
		"--server", server.URL, "--token", "token", "--project", projectName, "--output", "json",
	}

	getByName := runCLI(t, append([]string{"inject", "get", injectionName}, commonArgs...)...)
	if getByName.code != ExitCodeSuccess {
		t.Fatalf("inject get by name = %d, want %d; stderr=%q stdout=%q", getByName.code, ExitCodeSuccess, getByName.stderr, getByName.stdout)
	}
	var namePayload map[string]any
	if err := json.Unmarshal([]byte(getByName.stdout), &namePayload); err != nil {
		t.Fatalf("invalid JSON from inject get name: %v; stdout=%q", err, getByName.stdout)
	}
	if got, _ := namePayload["id"].(float64); int(got) != injectionID {
		t.Fatalf("id by name = %v, want %d", namePayload["id"], injectionID)
	}

	getByID := runCLI(t, append([]string{"inject", "get", "744"}, commonArgs...)...)
	if getByID.code != ExitCodeSuccess {
		t.Fatalf("inject get by id = %d, want %d; stderr=%q stdout=%q", getByID.code, ExitCodeSuccess, getByID.stderr, getByID.stdout)
	}

	files := runCLI(t, append([]string{"inject", "files", injectionName, "--output", "json"}, commonArgs...)...)
	if files.code != ExitCodeSuccess {
		t.Fatalf("inject files = %d, want %d; stderr=%q stdout=%q", files.code, ExitCodeSuccess, files.stderr, files.stdout)
	}
	var filesPayload []map[string]any
	if err := json.Unmarshal([]byte(files.stdout), &filesPayload); err != nil {
		t.Fatalf("invalid JSON from inject files: %v; stdout=%q", err, files.stdout)
	}
	if len(filesPayload) != 1 {
		t.Fatalf("inject files length=%d, want 1", len(filesPayload))
	}

	outputPath := filepath.Join(t.TempDir(), "download.tar.gz")
	download := runCLI(t, append([]string{"inject", "download", injectionName, "--output-file", outputPath}, commonArgs[:len(commonArgs)-2]...)...)
	if download.code == ExitCodeSuccess {
		t.Fatalf("expected download failure on partial stream, got success; stdout=%q stderr=%q", download.stdout, download.stderr)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected output file %s to be cleaned up, err=%v", outputPath, err)
	}
	if _, err := os.Stat(outputPath + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected temp file %s to be removed, err=%v", outputPath+".tmp", err)
	}

	missing := runCLI(t, append([]string{"inject", "get", "does-not-exist"}, commonArgs...)...)
	if missing.code != ExitCodeNotFound {
		t.Fatalf("inject get missing = %d, want %d; stderr=%q stdout=%q", missing.code, ExitCodeNotFound, missing.stderr, missing.stdout)
	}
	if !strings.Contains(missing.stderr, "\"type\":\"not_found\"") {
		t.Fatalf("missing resolver output should be structured; stderr=%q", missing.stderr)
	}
	if !strings.Contains(missing.stderr, "\"suggestions\"") {
		t.Fatalf("missing resolver output should include suggestions; stderr=%q", missing.stderr)
	}
}
