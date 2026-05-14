package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegis/cli/config"
	"aegis/cli/output"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
)

func resetCLIStateForTest() {
	flagServer = ""
	flagToken = ""
	flagProject = ""
	flagOutput = ""
	flagRequestTimeout = 0
	flagQuiet = false
	output.Quiet = false
	flagDryRun = false
	cfg = nil
	commandStdin = os.Stdin

	waitStdin = false
	waitStdinField = ""
	waitStdinFailFast = false
	injectGetStdin = false
	injectGetStdinField = ""
	injectGetStdinFailFast = false
	injectFilesStdin = false
	injectFilesStdinField = ""
	injectFilesStdinFailFast = false
	injectDownloadStdin = false
	injectDownloadStdinField = ""
	injectDownloadStdinFailFast = false
	taskGetStdin = false
	taskGetStdinField = ""
	taskGetStdinFailFast = false
	taskLogsStdin = false
	taskLogsStdinField = ""
	taskLogsStdinFailFast = false
	traceGetStdin = false
	traceGetStdinField = ""
	traceGetStdinFailFast = false
	traceWatchStdin = false
	traceWatchStdinField = ""
	traceWatchStdinFailFast = false
}

func captureStdIO(t *testing.T, fn func() error) (stdout string, stderr string, err error) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		t.Fatalf("stderr pipe: %v", err)
	}
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	os.Stdout = stdoutW
	os.Stderr = stderrW

	stdoutCh := make(chan []byte, 1)
	stderrCh := make(chan []byte, 1)
	readErrCh := make(chan error, 2)
	go func() {
		b, readErr := io.ReadAll(stdoutR)
		if readErr != nil {
			readErrCh <- readErr
			return
		}
		stdoutCh <- b
	}()
	go func() {
		b, readErr := io.ReadAll(stderrR)
		if readErr != nil {
			readErrCh <- readErr
			return
		}
		stderrCh <- b
	}()

	err = fn()
	if closeErr := stdoutW.Close(); closeErr != nil {
		t.Fatalf("close stdout writer: %v", closeErr)
	}
	if closeErr := stderrW.Close(); closeErr != nil {
		t.Fatalf("close stderr writer: %v", closeErr)
	}

	var stdoutBytes, stderrBytes []byte
	for i := 0; i < 2; i++ {
		select {
		case readErr := <-readErrCh:
			t.Fatalf("read captured output: %v", readErr)
		case stdoutBytes = <-stdoutCh:
			stdoutCh = nil
		case stderrBytes = <-stderrCh:
			stderrCh = nil
		}
	}
	if closeErr := stdoutR.Close(); closeErr != nil {
		t.Fatalf("close stdout reader: %v", closeErr)
	}
	if closeErr := stderrR.Close(); closeErr != nil {
		t.Fatalf("close stderr reader: %v", closeErr)
	}
	return string(stdoutBytes), string(stderrBytes), err
}

func executeCLIForTest(t *testing.T, home string, args ...string) (stdout string, stderr string, err error) {
	t.Helper()
	resetCLIStateForTest()
	t.Setenv("HOME", home)

	rootCmd.SetArgs(args)
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true

	return captureStdIO(t, func() error {
		_, execErr := rootCmd.ExecuteC()
		return execErr
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func decodeJSONMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode json %q: %v", raw, err)
	}
	return decoded
}

func TestValidationWorkflowCommandHelpMentionsMachineReadableFlags(t *testing.T) {
	home := t.TempDir()
	cases := [][]string{
		{"auth", "login", "--help"},
		{"status", "--help"},
		{"pedestal", "helm", "verify", "--help"},
		{"execute", "submit", "--help"},
	}

	for _, args := range cases {
		t.Run(strings.Join(args[:len(args)-1], "_"), func(t *testing.T) {
			stdout, stderr, err := executeCLIForTest(t, home, args...)
			if err != nil {
				t.Fatalf("help command failed: %v", err)
			}
			got := stdout + stderr
			if !strings.Contains(got, "--dry-run") {
				t.Fatalf("expected --dry-run in help output, got:\n%s", got)
			}
			if !strings.Contains(got, "--output") && !strings.Contains(got, "-o") {
				t.Fatalf("expected output flag in help output, got:\n%s", got)
			}
		})
	}
}

func TestValidationWorkflowSequenceJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	execSpec := filepath.Join(home, "execution.yaml")
	writeTestFile(t, execSpec, `
specs:
  - algorithm:
      name: random
      version: "1.0.0"
    datapack: sample-datapack
labels:
  - key: suite
    value: validation-contract
`)

	var requestedPaths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/auth/api-key/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"token":      "jwt-test-token",
					"token_type": "Bearer",
					"expires_at": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
					"auth_type":  "api_key",
					"key_id":     "pk_test",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/auth/profile":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"id":       1,
					"username": "admin",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items": []any{},
					"pagination": map[string]any{
						"page":        1,
						"size":        100,
						"total":       0,
						"total_pages": 0,
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/traces":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items": []any{
						map[string]any{
							"trace_id":     "trace-1",
							"state":        "Running",
							"type":         "FaultInjection",
							"project_name": "pair_diagnosis",
							"project_id":   7,
						},
					},
					"pagination": map[string]any{
						"page":        1,
						"size":        10,
						"total":       1,
						"total_pages": 1,
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/system/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"status":  "healthy",
					"version": "1.0.0",
					"uptime":  "15m",
					"services": map[string]any{
						"database": map[string]any{"status": "healthy", "response_time": "2ms"},
						"redis":    map[string]any{"status": "healthy", "response_time": "1ms"},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items": []any{
						map[string]any{"id": 7, "name": "pair_diagnosis"},
					},
					"pagination": map[string]any{
						"page":        1,
						"size":        100,
						"total":       1,
						"total_pages": 1,
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pedestal/helm/42/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"ok": true,
					"checks": []any{
						map[string]any{"name": "helm pull", "ok": true},
						map[string]any{"name": "values parse", "ok": true},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	resetCLIStateForTest()
	cfg = &config.Config{Contexts: map[string]config.Context{}}
	authLoginServer = ts.URL
	authLoginKeyID = "pk_test"
	authLoginKeySecret = "ks_test"
	authLoginContext = ""
	flagOutput = "json"

	stdout, _, err := captureStdIO(t, func() error {
		return authLoginCmd.RunE(nil, nil)
	})
	if err != nil {
		t.Fatalf("auth login failed: %v", err)
	}
	login := decodeJSONMap(t, stdout)
	if login["context"] != "default" {
		t.Fatalf("expected default context, got %v", login["context"])
	}

	loadedCfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config after login: %v", err)
	}
	cfg = loadedCfg
	currentCtx, _, err := config.GetCurrentContext(cfg)
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}

	resetCLIStateForTest()
	cfg = loadedCfg
	flagOutput = "json"

	stdout, _, err = captureStdIO(t, func() error {
		return authStatusCmd.RunE(nil, nil)
	})
	if err != nil {
		t.Fatalf("auth status failed: %v", err)
	}
	authStatus := decodeJSONMap(t, stdout)
	if authStatus["status"] != "valid" {
		t.Fatalf("expected auth status=valid, got %v", authStatus["status"])
	}

	resetCLIStateForTest()
	cfg = loadedCfg
	flagServer = currentCtx.Server
	flagToken = currentCtx.Token
	flagOutput = "json"

	stdout, _, err = captureStdIO(t, func() error {
		return statusCmd.RunE(nil, nil)
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	status := decodeJSONMap(t, stdout)
	if status["connected"] != true {
		t.Fatalf("expected connected=true, got %v", status["connected"])
	}
	health := status["health"].(map[string]any)
	if health["status"] != "healthy" {
		t.Fatalf("expected healthy status, got %v", health["status"])
	}

	resetCLIStateForTest()
	cfg = loadedCfg
	flagServer = currentCtx.Server
	flagToken = currentCtx.Token
	flagOutput = "json"
	pedestalHelmVersionID = 42

	stdout, _, err = captureStdIO(t, func() error {
		return pedestalHelmVerifyCmd.RunE(nil, nil)
	})
	if err != nil {
		t.Fatalf("pedestal helm verify failed: %v", err)
	}
	verify := decodeJSONMap(t, stdout)
	if verify["ok"] != true {
		t.Fatalf("expected verify ok=true, got %v", verify["ok"])
	}

	resetCLIStateForTest()
	cfg = loadedCfg
	flagServer = currentCtx.Server
	flagToken = currentCtx.Token
	flagProject = "pair_diagnosis"
	flagOutput = "json"
	flagDryRun = true
	executeCreateInput = execSpec

	stdout, _, err = captureStdIO(t, func() error {
		return executeCreateCmd.RunE(nil, nil)
	})
	if err != nil {
		t.Fatalf("execute create dry-run failed: %v", err)
	}
	executePlan := decodeJSONMap(t, stdout)
	if executePlan["dry_run"] != true {
		t.Fatalf("expected dry_run=true, got %v", executePlan["dry_run"])
	}
	if executePlan["project_id"] != float64(7) {
		t.Fatalf("expected project_id=7, got %v", executePlan["project_id"])
	}

	for _, path := range requestedPaths {
		if path == "POST /api/v2/projects/7/executions/execute" {
			t.Fatalf("execute create --dry-run should not hit submit endpoint")
		}
	}
}

func TestSubmitGuidedApplyDryRunJSON(t *testing.T) {
	resetCLIStateForTest()
	oldPedestalName := guidedApplyPedestalName
	oldPedestalTag := guidedApplyPedestalTag
	oldBenchmarkName := guidedApplyBenchmarkName
	oldBenchmarkTag := guidedApplyBenchmarkTag
	t.Cleanup(func() {
		guidedApplyPedestalName = oldPedestalName
		guidedApplyPedestalTag = oldPedestalTag
		guidedApplyBenchmarkName = oldBenchmarkName
		guidedApplyBenchmarkTag = oldBenchmarkTag
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"items": []any{
						map[string]any{"id": 9, "name": "pair_diagnosis"},
					},
					"pagination": map[string]any{
						"page":        1,
						"size":        100,
						"total":       1,
						"total_pages": 1,
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "jwt-test-token"
	flagProject = "pair_diagnosis"
	flagOutput = "json"
	flagRequestTimeout = 5
	flagDryRun = true
	guidedApplyPedestalName = "ts"
	guidedApplyPedestalTag = "1.0.0"
	guidedApplyBenchmarkName = "otel-demo-bench"
	guidedApplyBenchmarkTag = "1.0.0"

	stdout, _, err := captureStdIO(t, func() error {
		return submitGuidedApply(guidedcli.GuidedConfig{
			System:    "otel-demo0",
			Namespace: "exp",
			App:       "frontend",
		})
	})
	if err != nil {
		t.Fatalf("submitGuidedApply dry-run failed: %v", err)
	}

	plan := decodeJSONMap(t, stdout)
	if plan["dry_run"] != true {
		t.Fatalf("expected dry_run=true, got %v", plan["dry_run"])
	}
	if plan["project_id"] != float64(9) {
		t.Fatalf("expected project_id=9, got %v", plan["project_id"])
	}
	spec := plan["spec"].(map[string]any)
	if _, ok := spec["interval"]; ok {
		t.Fatalf("interval must not appear in dry-run envelope (it is pinned server-side)")
	}
	if _, ok := spec["pre_duration"]; ok {
		t.Fatalf("pre_duration must not appear in dry-run envelope (it is pinned server-side)")
	}
}
