package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionJSONIncludesRequiredFields(t *testing.T) {
	srcDir := aegisctlSourceRoot(t)
	binPath := filepath.Join(t.TempDir(), "aegisctl")

	build := exec.Command(
		"go", "build",
		"-ldflags", strings.Join([]string{
			"-X aegis/cli/cmd.version=v0.0.0-test",
			"-X aegis/cli/cmd.commit=abc1234",
			"-X aegis/cli/cmd.buildTime=2026-04-27T10:00:00Z",
			"-X aegis/cli/cmd.minServerAPIVersion=2",
		}, " "),
		"-o", binPath,
		"./cli",
	)
	build.Dir = srcDir
	build.Env = os.Environ()
	buildOutput, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build aegisctl: %v\n%s", err, buildOutput)
	}

	t.Run("json_payload_has_non_empty_required_fields", func(t *testing.T) {
		stdout, stderr, err := runBuiltAegisctl(t, binPath, "version", "-o", "json")
		if err != nil {
			t.Fatalf("run version: %v\nstderr=%s", err, stderr)
		}

		var payload map[string]string
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, stdout)
		}

		for _, key := range []string{"version", "commit", "build_time", "min_server_api"} {
			if strings.TrimSpace(payload[key]) == "" {
				t.Fatalf("required field %q missing or empty in payload: %#v", key, payload)
			}
		}
	})

	t.Run("version_flag_alias_matches_version_command", func(t *testing.T) {
		versionStdout, versionStderr, err := runBuiltAegisctl(t, binPath, "version", "-o", "json")
		if err != nil {
			t.Fatalf("run version: %v\nstderr=%s", err, versionStderr)
		}

		flagStdout, flagStderr, err := runBuiltAegisctl(t, binPath, "--version", "-o", "json")
		if err != nil {
			t.Fatalf("run --version: %v\nstderr=%s", err, flagStderr)
		}

		if versionStdout != flagStdout {
			t.Fatalf("stdout mismatch\nversion=%q\n--version=%q", versionStdout, flagStdout)
		}
	})

	t.Run("version_flag_alias_matches_env_selected_json_output", func(t *testing.T) {
		versionStdout, versionStderr, err := runBuiltAegisctl(t, binPath, "version")
		if err != nil {
			t.Fatalf("run version with env output: %v\nstderr=%s", err, versionStderr)
		}

		flagStdout, flagStderr, err := runBuiltAegisctl(t, binPath, "--version")
		if err != nil {
			t.Fatalf("run --version with env output: %v\nstderr=%s", err, flagStderr)
		}

		if versionStdout != flagStdout {
			t.Fatalf("stdout mismatch with env output\nversion=%q\n--version=%q", versionStdout, flagStdout)
		}

		var payload map[string]string
		if err := json.Unmarshal([]byte(flagStdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON under env output: %v\nstdout=%s", err, flagStdout)
		}
	})

	t.Run("missing_output_argument_reports_usage_error", func(t *testing.T) {
		_, stderr, err := runBuiltAegisctl(t, binPath, "version", "--output")
		if err == nil {
			t.Fatal("expected missing --output argument to fail")
		}

		var exitErr *exec.ExitError
		if !strings.Contains(stderr, "flag needs an argument: --output") {
			t.Fatalf("stderr missing expected parse error: %q", stderr)
		}
		if ok := errorAs(err, &exitErr); !ok {
			t.Fatalf("expected exit error, got %T: %v", err, err)
		}
		if exitErr.ExitCode() != ExitCodeUsage {
			t.Fatalf("exit code = %d, want %d; stderr=%q", exitErr.ExitCode(), ExitCodeUsage, stderr)
		}
	})
}

func aegisctlSourceRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func runBuiltAegisctl(t *testing.T, binPath string, args ...string) (string, string, error) {
	t.Helper()

	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "AEGIS_OUTPUT=json")

	stdout, err := cmd.Output()
	if err == nil {
		return string(stdout), "", nil
	}

	var stderr string
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = string(exitErr.Stderr)
	}
	return string(stdout), stderr, err
}

func errorAs(err error, target any) bool {
	return err != nil && execErrAs(err, target)
}

func execErrAs(err error, target any) bool {
	switch t := target.(type) {
	case **exec.ExitError:
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			return false
		}
		*t = exitErr
		return true
	default:
		return false
	}
}
