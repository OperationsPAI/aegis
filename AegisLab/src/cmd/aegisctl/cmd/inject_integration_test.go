package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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
	similarNames := []string{
		injectionName,
		"otel-demo23-recommendation-pod-failure-4t2mpc",
		"otel-demo23-recommendation-pod-failure-4t2mpd",
		"otel-demo23-recommendation-pod-failure-4t2mqb",
	}

	var downloadCalls atomic.Int32
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
			items := make([]map[string]any, 0, len(similarNames))
			for i, name := range similarNames {
				items = append(items, map[string]any{
					"id":   injectionID + i,
					"name": name,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "ok",
				"data": map[string]any{
					"items":      items,
					"pagination": map[string]any{"page": 1, "size": 100, "total": len(items), "total_pages": 1},
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
			if downloadCalls.Add(1) == 1 {
				_, _ = w.Write([]byte("zip payload"))
				return
			}
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

	successPath := filepath.Join(t.TempDir(), "download-success.tar.gz")
	success := runCLI(t, append([]string{
		"inject", "download", "744", "--output-file", successPath, "--output", "json",
	}, commonArgs[:len(commonArgs)-2]...)...)
	if success.code != ExitCodeSuccess {
		t.Fatalf("inject download success = %d, want %d; stderr=%q stdout=%q", success.code, ExitCodeSuccess, success.stderr, success.stdout)
	}
	var successPayload map[string]any
	if err := json.Unmarshal([]byte(success.stdout), &successPayload); err != nil {
		t.Fatalf("invalid JSON from inject download success: %v; stdout=%q", err, success.stdout)
	}
	if got, _ := successPayload["path"].(string); got != successPath {
		t.Fatalf("download path = %q, want %q", got, successPath)
	}
	if got, _ := successPayload["size"].(float64); int64(got) != int64(len("zip payload")) {
		t.Fatalf("download size = %v, want %d", successPayload["size"], len("zip payload"))
	}
	wantSHA := fmt.Sprintf("%x", sha256.Sum256([]byte("zip payload")))
	if got, _ := successPayload["sha256"].(string); got != wantSHA {
		t.Fatalf("download sha256 = %q, want %q", got, wantSHA)
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

	missing := runCLI(t, append([]string{"inject", "get", "otel-demo23-recommendation-pod-failure-4t2mpx"}, commonArgs...)...)
	if missing.code != ExitCodeNotFound {
		t.Fatalf("inject get missing = %d, want %d; stderr=%q stdout=%q", missing.code, ExitCodeNotFound, missing.stderr, missing.stdout)
	}
	payloadText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(missing.stderr), "Error:"))
	var missingPayload map[string]any
	if err := json.Unmarshal([]byte(payloadText), &missingPayload); err != nil {
		t.Fatalf("missing resolver output should contain JSON: %v; stderr=%q", err, missing.stderr)
	}
	if got, _ := missingPayload["type"].(string); got != "not_found" {
		t.Fatalf("missing type = %q, want not_found", got)
	}
	suggestions, ok := missingPayload["suggestions"].([]any)
	if !ok {
		t.Fatalf("missing suggestions should be an array; payload=%v", missingPayload)
	}
	if len(suggestions) != 3 {
		t.Fatalf("suggestions length = %d, want 3; payload=%v", len(suggestions), missingPayload)
	}
	if got, _ := suggestions[0].(string); got != injectionName {
		t.Fatalf("first suggestion = %q, want %q", got, injectionName)
	}
}
