package cmd

import (
	"strings"
	"testing"
)

// TestDryRunGuardOnSupportedCommand — a command explicitly marked as
// --dry-run-capable must NOT be rejected by the guard. We use `schema dump`
// here because it's read-only, marked supported, and needs no auth.
func TestDryRunGuardOnSupportedCommand(t *testing.T) {
	res := runCLI(t, "schema", "dump", "--dry-run")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeSuccess, res.stderr)
	}
}

// TestDryRunGuardOnUnsupportedCommand — invoking --dry-run on a command that
// never consumes the flag must fail fast with ExitCodeUsage and a stderr
// message that mentions --dry-run, so agents notice the silent-no-op risk.
func TestDryRunGuardOnUnsupportedCommand(t *testing.T) {
	res := runCLI(t, "status", "--dry-run")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--dry-run") {
		t.Fatalf("stderr = %q, want --dry-run diagnostic", res.stderr)
	}
}
