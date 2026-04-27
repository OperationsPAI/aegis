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

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type cliRunResult struct {
	code   int
	stdout string
	stderr string
}

func resetCLIState() {
	flagServer = ""
	flagToken = ""
	flagProject = ""
	flagOutput = ""
	flagRequestTimeout = 0
	flagQuiet = false
	flagVersion = false
	flagNonInteractive = false
	flagDryRun = false
	output.Quiet = false
	commandStdin = os.Stdin

	executeCreateInput = ""
	executeCreateSpec = ""

	authLoginServer = ""
	authLoginKeyID = ""
	authLoginKeySecret = ""
	authLoginContext = ""

	clusterPreflightCheck = ""
	clusterPreflightFix = false
	clusterPreflightConfig = ""
	clusterPreflightTimeout = 0

	guidedCfgPath = ""
	guidedResetConfig = false
	guidedNoSaveConfig = false
	guidedNamespace = ""
	guidedSystem = ""
	guidedSystemType = ""
	guidedApp = ""
	guidedChaosType = ""
	guidedContainer = ""
	guidedTargetService = ""
	guidedDomain = ""
	guidedClass = ""
	guidedMethod = ""
	guidedMutatorConfig = ""
	guidedRoute = ""
	guidedHTTPMethod = ""
	guidedDatabase = ""
	guidedTable = ""
	guidedOperation = ""
	guidedDirection = ""
	guidedReturnType = ""
	guidedReturnOpt = ""
	guidedExceptionOpt = ""
	guidedMemType = ""
	guidedBodyType = ""
	guidedReplaceMethod = ""
	guidedNext = ""
	guidedOutput = ""
	guidedApply = false
	guidedApplyPedestalName = ""
	guidedApplyPedestalTag = ""
	guidedApplyBenchmarkName = ""
	guidedApplyBenchmarkTag = ""
	guidedApplyInterval = 0
	guidedApplyPreDuration = 0

	waitTimeout = 0
	waitInterval = 0
	waitStdin = false
	waitStdinField = ""

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
	waitStdinFailFast = false

	resetCommandFlags(rootCmd)
}

func resetFlagSet(fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

func resetCommandFlags(cmd *cobra.Command) {
	resetFlagSet(cmd.PersistentFlags())
	resetFlagSet(cmd.Flags())
	for _, child := range cmd.Commands() {
		resetCommandFlags(child)
	}
}

func runCLI(t *testing.T, args ...string) cliRunResult {
	t.Helper()

	resetCLIState()

	oldHome := os.Getenv("HOME")
	oldServer := os.Getenv("AEGIS_SERVER")
	oldToken := os.Getenv("AEGIS_TOKEN")
	oldProject := os.Getenv("AEGIS_PROJECT")
	oldOutput := os.Getenv("AEGIS_OUTPUT")
	oldTimeout := os.Getenv("AEGIS_TIMEOUT")
	oldNonInteractive := os.Getenv("AEGIS_NON_INTERACTIVE")
	oldStdout := os.Stdout
	oldStderr := os.Stderr

	home := t.TempDir()
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	for _, key := range []string{
		"AEGIS_SERVER",
		"AEGIS_TOKEN",
		"AEGIS_PROJECT",
		"AEGIS_OUTPUT",
		"AEGIS_TIMEOUT",
		"AEGIS_NON_INTERACTIVE",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

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
	os.Stdout = stdoutW
	os.Stderr = stderrW

	// Drain pipes concurrently. The kernel pipe buffer is ~64KB; commands like
	// `schema dump` write more than that and would deadlock on Write if the
	// reader only ran after executeArgs returned.
	type readResult struct {
		buf []byte
		err error
	}
	stdoutCh := make(chan readResult, 1)
	stderrCh := make(chan readResult, 1)
	go func() {
		b, e := io.ReadAll(stdoutR)
		stdoutCh <- readResult{b, e}
	}()
	go func() {
		b, e := io.ReadAll(stderrR)
		stderrCh <- readResult{b, e}
	}()

	code := executeArgs(args)

	_ = stdoutW.Close()
	_ = stderrW.Close()
	stdoutRes := <-stdoutCh
	stderrRes := <-stderrCh
	if stdoutRes.err != nil {
		t.Fatalf("read stdout: %v", stdoutRes.err)
	}
	if stderrRes.err != nil {
		t.Fatalf("read stderr: %v", stderrRes.err)
	}
	stdoutBytes := stdoutRes.buf
	stderrBytes := stderrRes.buf
	_ = stdoutR.Close()
	_ = stderrR.Close()

	os.Stdout = oldStdout
	os.Stderr = oldStderr
	if err := os.Setenv("HOME", oldHome); err != nil {
		t.Fatalf("restore HOME: %v", err)
	}
	restoreEnv := func(key, value string) {
		var err error
		if value == "" {
			err = os.Unsetenv(key)
		} else {
			err = os.Setenv(key, value)
		}
		if err != nil {
			t.Fatalf("restore %s: %v", key, err)
		}
	}
	restoreEnv("AEGIS_SERVER", oldServer)
	restoreEnv("AEGIS_TOKEN", oldToken)
	restoreEnv("AEGIS_PROJECT", oldProject)
	restoreEnv("AEGIS_OUTPUT", oldOutput)
	restoreEnv("AEGIS_TIMEOUT", oldTimeout)
	restoreEnv("AEGIS_NON_INTERACTIVE", oldNonInteractive)

	return cliRunResult{
		code:   code,
		stdout: string(stdoutBytes),
		stderr: string(stderrBytes),
	}
}

func TestRootPersistentFlagsExposeNonInteractiveMode(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("non-interactive")
	if f == nil {
		t.Fatalf("expected --non-interactive persistent flag to be registered")
	}
}

func TestRootPersistentFlagsExposeNoColorMode(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("no-color")
	if f == nil {
		t.Fatalf("expected --no-color persistent flag to be registered")
	}
}

func TestAuthLoginMissingSecretUsesUsageExitCode(t *testing.T) {
	res := runCLI(t, "auth", "login", "--server", "http://example.test", "--key-id", "pk_test")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--key-secret is required") {
		t.Fatalf("stderr = %q, want missing key-secret diagnostic", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "" {
		t.Fatalf("stdout should be empty on validation failure, got %q", res.stdout)
	}
}

func TestAuthLoginMissingIdentityUsesUsageExitCode(t *testing.T) {
	res := runCLI(t, "auth", "login", "--server", "http://example.test")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "either --username or --key-id is required") {
		t.Fatalf("stderr = %q, want identity diagnostic", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "" {
		t.Fatalf("stdout should be empty on validation failure, got %q", res.stdout)
	}
}

func TestClusterPreflightUnknownCheckUsesUsageExitCode(t *testing.T) {
	res := runCLI(t, "cluster", "preflight", "--check", "does-not-exist")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "unknown --check") {
		t.Fatalf("stderr = %q, want unknown check diagnostic", res.stderr)
	}
}

func TestClusterPreflightFailingCheckUsesMissingEnvExitCode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	res := runCLI(t, "cluster", "preflight", "--config", cfgPath, "--check", "db.mysql")
	if res.code != ExitCodeMissingEnv {
		t.Fatalf("exit code = %d, want %d; stdout=%q stderr=%q", res.code, ExitCodeMissingEnv, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "db.mysql") || !strings.Contains(res.stdout, "[FAIL]") {
		t.Fatalf("stdout = %q, want failing preflight row", res.stdout)
	}
	if strings.TrimSpace(res.stderr) != "" {
		t.Fatalf("stderr should stay empty when preflight emits result table on stdout, got %q", res.stderr)
	}
}

func TestWaitJSONKeepsStdoutCleanAndUsesWorkflowExitCode(t *testing.T) {
	origDetect := waitDetectResourceType
	origPoll := waitPollState
	waitDetectResourceType = func(_ *client.Client, _ string) (string, error) {
		return "trace", nil
	}
	waitPollState = func(_ *client.Client, _, _ string) (string, any, error) {
		return "Failed", map[string]any{"trace_id": "trace-1", "state": "Failed"}, nil
	}
	defer func() {
		waitDetectResourceType = origDetect
		waitPollState = origPoll
	}()

	res := runCLI(t, "wait", "trace-1", "--server", "http://example.test", "--token", "token", "--output", "json", "--interval", "0", "--timeout", "1")
	if res.code != ExitCodeWorkflowFailure {
		t.Fatalf("exit code = %d, want %d; stdout=%q stderr=%q", res.code, ExitCodeWorkflowFailure, res.stdout, res.stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout should stay valid JSON, got %q: %v", res.stdout, err)
	}
	if payload["state"] != "Failed" {
		t.Fatalf("stdout JSON = %v, want state Failed", payload)
	}
	if !strings.Contains(res.stderr, "Waiting for trace-1") {
		t.Fatalf("stderr = %q, want progress diagnostics", res.stderr)
	}
}

func TestTraceGetMissingTokenUsesAuthExitCode(t *testing.T) {
	res := runCLI(t, "trace", "get", "trace-1", "--server", "http://example.test")
	if res.code != ExitCodeAuthFailure {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeAuthFailure, res.stderr)
	}
	if !strings.Contains(res.stderr, "AEGIS_TOKEN") {
		t.Fatalf("stderr = %q, want token diagnostic", res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "" {
		t.Fatalf("stdout should be empty on auth failure, got %q", res.stdout)
	}
}

func TestIntegrationServerAndDecodeErrorsEmitJSONStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %v", r.Method)
		}
		switch r.URL.Path {
		case "/api/v2/projects":
			if r.URL.Query().Get("page") == "2" {
				w.WriteHeader(http.StatusOK)
				// Payload is intentionally schema-incompatible for
				// PaginatedData[projectListItem] (id must be int).
				_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"items":[{"id":"bad-id","name":"broken","description":"","status":"active","created_at":"2026-01-01"}],"pagination":{"page":2,"size":20,"total":1,"total_pages":1}}}`))
				return
			}
			w.Header().Set("X-Request-Id", "req-server-fail")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"message":"An unexpected error occurred","request_id":"should-not-be-in-json"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	serverErr := runCLI(t, "project", "list", "--server", server.URL, "--output", "json")
	if serverErr.code != ExitCodeServer {
		t.Fatalf("exit code = %d, want %d; stderr=%q", serverErr.code, ExitCodeServer, serverErr.stderr)
	}
	var serverPayload map[string]any
	if err := json.Unmarshal([]byte(serverErr.stderr), &serverPayload); err != nil {
		t.Fatalf("stderr should be JSON for --output=json: %v\nstderr=%q", err, serverErr.stderr)
	}
	if got, _ := serverPayload["type"].(string); got != "server" {
		t.Fatalf("server payload type = %v, want server", serverPayload["type"])
	}
	if got, _ := serverPayload["exit_code"].(float64); int(got) != ExitCodeServer {
		t.Fatalf("server payload exit_code = %v, want %d", serverPayload["exit_code"], ExitCodeServer)
	}
	if v := serverPayload["request_id"]; v != "req-server-fail" {
		t.Fatalf("server payload request_id = %v, want req-server-fail", v)
	}
	if strings.Contains(serverErr.stderr, "An unexpected error occurred") {
		t.Fatalf("server payload leaked generic server message: %q", serverErr.stderr)
	}

	decodeErr := runCLI(t, "project", "list", "--page", "2", "--server", server.URL, "--output", "ndjson")
	if decodeErr.code != ExitCodeDecode {
		t.Fatalf("exit code = %d, want %d; stderr=%q", decodeErr.code, ExitCodeDecode, decodeErr.stderr)
	}
	var decodePayload map[string]any
	if err := json.Unmarshal([]byte(decodeErr.stderr), &decodePayload); err != nil {
		t.Fatalf("stderr should be JSON for --output=ndjson: %v\nstderr=%q", err, decodeErr.stderr)
	}
	if got, _ := decodePayload["type"].(string); got != "decode" {
		t.Fatalf("decode payload type = %v, want decode", decodePayload["type"])
	}
	if got, _ := decodePayload["exit_code"].(float64); int(got) != ExitCodeDecode {
		t.Fatalf("decode payload exit_code = %v, want %d", decodePayload["exit_code"], ExitCodeDecode)
	}
	if !strings.Contains(decodeErr.stderr, "field") || !strings.Contains(decodeErr.stderr, "expected") {
		t.Fatalf("decode payload should include path/type details, got %q", decodeErr.stderr)
	}
}
