package cmd

import (
	"strings"
	"testing"
)

// TestShareUploadHelpMentionsPresignFlow guards the user-facing
// description of the three-step presigned-PUT upload so it doesn't
// regress to the deprecated multipart wording.
func TestShareUploadHelpMentionsPresignFlow(t *testing.T) {
	res := runCLI(t, "share", "upload", "--help")
	if res.code != 0 {
		t.Fatalf("share upload --help exit=%d stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{
		"presigned-PUT",
		"share/init",
		"share/<code>/commit",
		"sha256",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("share upload --help missing %q\nstdout=%s", want, res.stdout)
		}
	}
}

// TestShareUploadNoSHAFlagParses mirrors the above for --no-sha256.
func TestShareUploadNoSHAFlagParses(t *testing.T) {
	res := runCLI(t, "share", "upload", "/nonexistent/path", "--no-sha256")
	if strings.Contains(res.stderr, "unknown flag") {
		t.Fatalf("--no-sha256 not registered: %s", res.stderr)
	}
}
