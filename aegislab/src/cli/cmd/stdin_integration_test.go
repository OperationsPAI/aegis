package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegis/cli/output"
)

func TestInjectDownloadFromStdinPipe(t *testing.T) {
	resetCLIState()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":1,"name":"proj"}],"pagination":{"page":1,"size":100,"total":1,"total_pages":1}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/1/injections":
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":11,"name":"inject-a","state":"build_success","fault_type":"cpu","start_time":"2026-04-27T00:00:00Z","labels":[]},{"id":12,"name":"inject-b","state":"build_success","fault_type":"cpu","start_time":"2026-04-27T00:00:00Z","labels":[]}],"pagination":{"page":1,"size":100,"total":2,"total_pages":1}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/injections/11/files":
			_, _ = w.Write([]byte(`{"code":0,"data":{"files":[{"path":"converted/result.txt"}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/injections/12/files":
			_, _ = w.Write([]byte(`{"code":0,"data":{"files":[{"path":"converted/result.txt"}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/injections/11/files/download":
			_, _ = w.Write([]byte("alpha"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/injections/12/files/download":
			_, _ = w.Write([]byte("beta"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	flagServer = srv.URL
	flagToken = "tok"
	flagProject = "proj"
	flagOutput = string(output.FormatNDJSON)
	injectListSize = 100

	stdout, stderr, err := captureStdIO(t, func() error {
		return injectListCmd.RunE(injectListCmd, nil)
	})
	if err != nil {
		t.Fatalf("injectListCmd.RunE: %v", err)
	}
	_ = stderr
	if !strings.Contains(stdout, `"name":"inject-a"`) || !strings.Contains(stdout, `"name":"inject-b"`) {
		t.Fatalf("list stdout = %q, want NDJSON items", stdout)
	}

	resetCLIState()
	flagServer = srv.URL
	flagToken = "tok"
	flagProject = "proj"
	injectDownloadDir = t.TempDir()
	injectDownloadStdin = true
	commandStdin = strings.NewReader(stdout)
	t.Cleanup(func() { commandStdin = os.Stdin })

	_, downloadStderr, err := captureStdIO(t, func() error {
		return injectDownloadCmd.RunE(injectDownloadCmd, nil)
	})
	if err != nil {
		t.Fatalf("injectDownloadCmd.RunE: %v", err)
	}
	if !strings.Contains(downloadStderr, "[1/2] download inject-a: ok") || !strings.Contains(downloadStderr, "[2/2] download inject-b: ok") {
		t.Fatalf("download stderr = %q, want batch progress lines", downloadStderr)
	}

	assertFile := func(rel, want string) {
		t.Helper()
		got, readErr := os.ReadFile(filepath.Join(injectDownloadDir, rel))
		if readErr != nil {
			t.Fatalf("read %s: %v", rel, readErr)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", rel, string(got), want)
		}
	}
	assertFile(filepath.Join("inject-a", "result.txt"), "alpha")
	assertFile(filepath.Join("inject-b", "result.txt"), "beta")
}
