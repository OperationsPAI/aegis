package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// page push without an argument must exit ExitCodeUsage and not crash.
func TestPagePushRequiresArgument(t *testing.T) {
	res := runCLI(t, "page", "push")
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want %d; stderr=%q", res.code, ExitCodeUsage, res.stderr)
	}
}

// page push with --dry-run on a single .md file emits the file in the plan
// and never touches the network.
func TestPagePushDryRunSingleFile(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(md, []byte("---\nslug: from-fm\ntitle: From FM\n---\n# Notes\nbody\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	res := runCLI(t,
		"page", "push", md,
		"--dry-run",
		"--server", "http://example.invalid",
		"--token", "fake",
		"--output", "json",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d (want 0); stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("decode stdout: %v: %s", err, res.stdout)
	}
	if got["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", got["dry_run"])
	}
	if got["slug"] != "from-fm" {
		t.Errorf("slug = %v, want from-fm (frontmatter default)", got["slug"])
	}
	if got["title"] != "From FM" {
		t.Errorf("title = %v, want \"From FM\"", got["title"])
	}
	files, ok := got["files"].([]any)
	if !ok || len(files) != 1 || files[0] != "notes.md" {
		t.Errorf("files = %v, want [notes.md]", got["files"])
	}
}

// --slug overrides the frontmatter value.
func TestPagePushDryRunSlugFlagOverridesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(md, []byte("---\nslug: from-fm\n---\n# T\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := runCLI(t,
		"page", "push", md,
		"--dry-run",
		"--slug", "from-flag",
		"--server", "http://example.invalid",
		"--token", "fake",
		"--output", "json",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d; stderr=%q", res.code, res.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["slug"] != "from-flag" {
		t.Errorf("slug = %v, want from-flag", got["slug"])
	}
}

// Directory push lists every file (preserving relative paths) and never hits
// the network in --dry-run.
func TestPagePushDryRunDirectory(t *testing.T) {
	dir := t.TempDir()
	must := func(name, content string) {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must("index.md", "---\nslug: from-fm\n---\n# Root\n")
	must("about.md", "# About\n")
	must("assets/logo.css", "body{}")

	res := runCLI(t,
		"page", "push", dir,
		"--dry-run",
		"--server", "http://example.invalid",
		"--token", "fake",
		"--output", "json",
	)
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit code = %d; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	files, _ := got["files"].([]any)
	if len(files) != 3 {
		t.Fatalf("files = %v, want 3 entries", files)
	}
	want := map[string]bool{"about.md": false, "assets/logo.css": false, "index.md": false}
	for _, f := range files {
		s, _ := f.(string)
		if _, ok := want[s]; ok {
			want[s] = true
		} else {
			t.Errorf("unexpected file in plan: %q", s)
		}
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("missing file in plan: %q", f)
		}
	}
}

// Invalid --visibility is rejected with ExitCodeUsage and never touches the
// network.
func TestPagePushRejectsInvalidVisibility(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "x.md")
	if err := os.WriteFile(md, []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runCLI(t,
		"page", "push", md,
		"--visibility", "nope",
		"--dry-run",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want usage(2); stderr=%q", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "visibility") {
		t.Errorf("stderr = %q, want a `visibility` diagnostic", res.stderr)
	}
}

// page open under --non-interactive must refuse rather than spawning a
// browser.
func TestPageOpenRefusesNonInteractive(t *testing.T) {
	res := runCLI(t,
		"page", "open", "any-slug",
		"--non-interactive",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want usage(2); stderr=%q", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "non-interactive") {
		t.Errorf("stderr = %q, want a non-interactive diagnostic", res.stderr)
	}
}

// page ls --mine --public must be rejected as mutually exclusive.
func TestPageListMineAndPublicMutuallyExclusive(t *testing.T) {
	res := runCLI(t,
		"page", "ls", "--mine", "--public",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code != ExitCodeUsage {
		t.Fatalf("exit code = %d, want usage(2); stderr=%q", res.code, res.stderr)
	}
}

// page push pointed at a path that doesn't exist exits non-zero with a stat
// error (exact code falls back to ExitCodeUnexpected since it isn't an API
// failure).
func TestPagePushMissingPath(t *testing.T) {
	res := runCLI(t,
		"page", "push", filepath.Join(t.TempDir(), "does-not-exist"),
		"--dry-run",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code == ExitCodeSuccess {
		t.Fatalf("expected non-zero exit; got 0 stdout=%q", res.stdout)
	}
}

// Directory with no .md is rejected before any network IO.
func TestPagePushDirectoryNeedsMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runCLI(t,
		"page", "push", dir,
		"--dry-run",
		"--server", "http://example.invalid",
		"--token", "fake",
	)
	if res.code == ExitCodeSuccess {
		t.Fatalf("expected non-zero exit for asset-only directory; stderr=%q", res.stderr)
	}
}

// schema dump must surface all five page subcommands so external schema
// consumers (autoharness graders, agent tooling) can discover them.
func TestPageCommandsAppearInSchemaDump(t *testing.T) {
	res := runCLI(t, "schema", "dump", "--output", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("schema dump failed: code=%d stderr=%q", res.code, res.stderr)
	}
	want := []string{
		"aegisctl page push",
		"aegisctl page ls",
		"aegisctl page get",
		"aegisctl page rm",
		"aegisctl page open",
	}
	for _, p := range want {
		if !strings.Contains(res.stdout, "\""+p+"\"") {
			t.Errorf("schema dump missing %q", p)
		}
	}
}
