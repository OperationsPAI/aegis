package cmd

import (
	"strings"
	"testing"
)

// TestBlobLsRejectsLocalPath catches the easy mistake of forgetting the
// '<bucket>:' prefix on the argument.
func TestBlobLsRejectsLocalPath(t *testing.T) {
	res := runCLI(t, "blob", "ls", "./local-path")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "expected a remote") {
		t.Fatalf("stderr = %q, want 'expected a remote' diagnostic", res.stderr)
	}
}

// TestBlobLsRejectsDryRun verifies the read-only ls command rejects --dry-run
// rather than silently no-op-ing (one of the loudest classes of "agent ran
// dry but it wasn't dry" bugs).
func TestBlobLsRejectsDryRun(t *testing.T) {
	res := runCLI(t, "blob", "ls", "aegis-pages:test", "--dry-run", "--non-interactive")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--dry-run is not supported") {
		t.Fatalf("stderr = %q, want dry-run rejection diagnostic", res.stderr)
	}
}

// TestBlobRmRejectsPrefixWithoutRecursive ensures `blob rm aegis-pages:` (no
// key) is refused without --recursive — the most catastrophic accidental
// invocation we can prevent.
func TestBlobRmRejectsPrefixWithoutRecursive(t *testing.T) {
	res := runCLI(t, "blob", "rm", "aegis-pages:", "--yes", "--non-interactive",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--recursive") {
		t.Fatalf("stderr = %q, want --recursive diagnostic", res.stderr)
	}
}

// TestBlobCpRejectsLocalToLocal asserts the cross-tool refuse rule.
func TestBlobCpRejectsLocalToLocal(t *testing.T) {
	res := runCLI(t, "blob", "cp", "./a", "./b")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "Local->Local") {
		t.Fatalf("stderr = %q, want Local->Local diagnostic", res.stderr)
	}
}

// TestBlobPresignRejectsBadMethod guards against the next-most-likely typo.
func TestBlobPresignRejectsBadMethod(t *testing.T) {
	res := runCLI(t, "blob", "presign", "aegis-pages:foo", "--method", "post",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--method must be") {
		t.Fatalf("stderr = %q, want --method validation diagnostic", res.stderr)
	}
}

// TestBucketCreateRequiresDriver verifies --driver is required upfront.
func TestBucketCreateRequiresDriver(t *testing.T) {
	res := runCLI(t, "bucket", "create", "my-bucket",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--driver is required") {
		t.Fatalf("stderr = %q, want --driver diagnostic", res.stderr)
	}
}

// TestBucketCreateRejectsBadName uses the blobref regex so the test doubles
// as cross-validation between the two callers of IsValidBucketName.
func TestBucketCreateRejectsBadName(t *testing.T) {
	res := runCLI(t, "bucket", "create", "BadName", "--driver", "localfs",
		"--server", "http://example.test", "--token", "stub")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "invalid bucket name") {
		t.Fatalf("stderr = %q, want invalid-bucket-name diagnostic", res.stderr)
	}
}

// TestSchemaIncludesBlobAndBucket sanity-checks that the new command tree is
// reachable from the schema dump (the contract relied on by SDKs / docs).
func TestSchemaIncludesBlobAndBucket(t *testing.T) {
	res := runCLI(t, "schema", "dump", "--output", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d, want 0; stderr=%q", res.code, res.stderr)
	}
	// Crude substring checks beat full JSON decoding here — the dump is large
	// and we only need the new entries to appear at all.
	wantPaths := []string{
		"aegisctl blob ls", "aegisctl blob stat", "aegisctl blob cat",
		"aegisctl blob cp", "aegisctl blob mv", "aegisctl blob rm",
		"aegisctl blob find", "aegisctl blob mirror", "aegisctl blob presign",
		"aegisctl bucket ls", "aegisctl bucket create", "aegisctl bucket get",
		"aegisctl bucket rm",
	}
	for _, want := range wantPaths {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("schema dump missing %q", want)
		}
	}
}
