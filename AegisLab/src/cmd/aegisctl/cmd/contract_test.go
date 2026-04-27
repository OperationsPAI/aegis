package cmd

import (
	"encoding/json"
	"io"
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
	flagNonInteractive = false
	flagDryRun = false
	output.Quiet = false
	commandStdin = os.Stdin

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
	injectFilesStdin = false
	injectFilesStdinField = ""
	injectDownloadStdin = false
	injectDownloadStdinField = ""

	taskGetStdin = false
	taskGetStdinField = ""
	taskLogsStdin = false
	taskLogsStdinField = ""

	traceGetStdin = false
	traceGetStdinField = ""
	traceWatchStdin = false
	traceWatchStdinField = ""

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
