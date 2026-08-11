package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLifecycleFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// `bucket lifecycle get` with a bad bucket name fails before any network IO.
func TestBucketLifecycleGetRejectsBadName(t *testing.T) {
	res := runCLI(t, "bucket", "lifecycle", "get", "BadName",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "invalid bucket name") {
		t.Fatalf("stderr = %q, want invalid-bucket-name", res.stderr)
	}
}

// `bucket lifecycle set` requires --file.
func TestBucketLifecycleSetRequiresFile(t *testing.T) {
	res := runCLI(t, "bucket", "lifecycle", "set", "good-name",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--file is required") {
		t.Fatalf("stderr = %q, want --file required diagnostic", res.stderr)
	}
}

// `bucket lifecycle set` rejects invalid JSON before talking to the server.
func TestBucketLifecycleSetRejectsInvalidJSON(t *testing.T) {
	path := writeLifecycleFixture(t, "{not json")
	res := runCLI(t, "bucket", "lifecycle", "set", "good-name",
		"-f", path,
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "invalid lifecycle JSON") {
		t.Fatalf("stderr = %q, want JSON diagnostic", res.stderr)
	}
}

// `bucket lifecycle clear` refuses to run without --yes (and --dry-run).
func TestBucketLifecycleClearRequiresYes(t *testing.T) {
	res := runCLI(t, "bucket", "lifecycle", "clear", "good-name",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--yes is required") {
		t.Fatalf("stderr = %q, want --yes diagnostic", res.stderr)
	}
}

// `bucket lifecycle set --dry-run` emits the planned PUT shape and does
// not hit the network.
func TestBucketLifecycleSetDryRunJSON(t *testing.T) {
	path := writeLifecycleFixture(t, `{"rules":[{"name":"r1","match_prefix":"tmp/","expire_after_days":7,"action":"delete"}]}`)
	res := runCLI(t, "bucket", "lifecycle", "set", "scratch",
		"-f", path,
		"--dry-run",
		"--output", "json",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, `"dry_run": true`) {
		t.Fatalf("stdout missing dry_run flag: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, `"would_put": "/api/v2/blob/buckets/scratch/lifecycle"`) {
		t.Fatalf("stdout missing would_put line: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, `"rule_count": 1`) {
		t.Fatalf("stdout missing rule_count: %q", res.stdout)
	}
}

// `bucket lifecycle clear --dry-run` emits the empty-rules PUT plan.
func TestBucketLifecycleClearDryRunJSON(t *testing.T) {
	res := runCLI(t, "bucket", "lifecycle", "clear", "scratch",
		"--dry-run",
		"--output", "json",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, `"dry_run": true`) {
		t.Fatalf("stdout missing dry_run flag: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, `"would_put": "/api/v2/blob/buckets/scratch/lifecycle"`) {
		t.Fatalf("stdout missing would_put line: %q", res.stdout)
	}
}

// `bucket create --dry-run --lifecycle path.json` includes the lifecycle
// block in the planned payload so the user can spot mistakes before
// hitting the server.
func TestBucketCreateDryRunIncludesLifecycle(t *testing.T) {
	path := writeLifecycleFixture(t, `{"rules":[{"name":"r1","match_prefix":"tmp/","expire_after_days":3,"action":"delete"}]}`)
	res := runCLI(t, "bucket", "create", "scratch",
		"--driver", "localfs",
		"--root", "/tmp/scratch",
		"--lifecycle", path,
		"--dry-run",
		"--output", "json",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, `"dry_run": true`) {
		t.Fatalf("stdout missing dry_run flag: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, `"lifecycle"`) {
		t.Fatalf("stdout missing lifecycle key: %q", res.stdout)
	}
	if !strings.Contains(res.stdout, `"name": "r1"`) {
		t.Fatalf("stdout missing rule name: %q", res.stdout)
	}
}

// schema dump must surface the three lifecycle subcommands so external
// schema consumers can discover them.
func TestBucketLifecycleCommandsAppearInSchemaDump(t *testing.T) {
	res := runCLI(t, "schema", "dump", "--output", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("schema dump failed: code=%d stderr=%q", res.code, res.stderr)
	}
	want := []string{
		"aegisctl bucket lifecycle get",
		"aegisctl bucket lifecycle set",
		"aegisctl bucket lifecycle clear",
	}
	for _, p := range want {
		if !strings.Contains(res.stdout, "\""+p+"\"") {
			t.Errorf("schema dump missing %q", p)
		}
	}
}
