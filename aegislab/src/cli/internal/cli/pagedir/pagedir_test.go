package pagedir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectFromPath_SingleMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.md")
	if err := os.WriteFile(p, []byte("# Hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err := CollectFromPath(p)
	if err != nil {
		t.Fatalf("CollectFromPath: %v", err)
	}
	if !plan.SingleFile {
		t.Fatalf("SingleFile = false, want true")
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(plan.Entries))
	}
	if plan.Entries[0].RelPath != "hello.md" {
		t.Fatalf("rel = %q, want hello.md", plan.Entries[0].RelPath)
	}
}

func TestCollectFromPath_SingleNonMarkdownRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "image.png")
	if err := os.WriteFile(p, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CollectFromPath(p); err == nil {
		t.Fatalf("expected error for non-md single-file push")
	}
}

func TestCollectFromPath_DirectoryWithAssets(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.md"), "# Root\n")
	mustWrite(t, filepath.Join(dir, "about.md"), "# About\n")
	mustWrite(t, filepath.Join(dir, "assets", "logo.png"), "fakepng")
	plan, err := CollectFromPath(dir)
	if err != nil {
		t.Fatalf("CollectFromPath: %v", err)
	}
	if plan.SingleFile {
		t.Fatalf("SingleFile = true, want false")
	}
	if len(plan.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(plan.Entries))
	}
	// Check the relative path of the asset uses forward-slash.
	var found bool
	for _, e := range plan.Entries {
		if e.RelPath == "assets/logo.png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("assets/logo.png not found in plan: %+v", plan.Entries)
	}
}

func TestCollectFromPath_DirectoryWithoutMarkdownRejected(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "style.css"), "body{}")
	if _, err := CollectFromPath(dir); err == nil {
		t.Fatalf("expected error: directory has no .md")
	}
}

func TestCollectFromPath_SkipsDotfiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.md"), "# Root\n")
	mustWrite(t, filepath.Join(dir, ".DS_Store"), "junk")
	mustWrite(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main")
	plan, err := CollectFromPath(dir)
	if err != nil {
		t.Fatalf("CollectFromPath: %v", err)
	}
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.RelPath, ".") || strings.Contains(e.RelPath, "/.") {
			t.Fatalf("dotfile leaked: %q", e.RelPath)
		}
	}
}

func TestCollectFromPath_RefusesSymlinkRoot(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "index.md"), "# Root\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := CollectFromPath(link); err == nil {
		t.Fatalf("expected refusal of symlink root")
	}
}

func TestValidateRel(t *testing.T) {
	good := []string{"index.md", "assets/logo.png", "a/b/c.md"}
	for _, g := range good {
		if err := validateRel(g); err != nil {
			t.Errorf("validateRel(%q) errored: %v", g, err)
		}
	}
	bad := []string{"", ".", "/abs", "../escape", "a/../b", "./trailing"}
	for _, b := range bad {
		if err := validateRel(b); err == nil {
			t.Errorf("validateRel(%q) accepted; want rejection", b)
		}
	}
}

func TestParseFrontmatter_BothKeys(t *testing.T) {
	src := "---\nslug: my-page\ntitle: \"Hello, World\"\nfoo: bar\n---\n# Body\n"
	def, err := ParseFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if def.Slug != "my-page" {
		t.Errorf("slug = %q", def.Slug)
	}
	if def.Title != "Hello, World" {
		t.Errorf("title = %q", def.Title)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	def, err := ParseFrontmatter(strings.NewReader("# Just a heading\nbody\n"))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if def.Slug != "" || def.Title != "" {
		t.Errorf("expected empty defaults, got %+v", def)
	}
}

func TestParseFrontmatter_SingleQuoted(t *testing.T) {
	src := "---\nslug: 'sluggy'\ntitle: 'Some Title'\n---\n"
	def, err := ParseFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if def.Slug != "sluggy" || def.Title != "Some Title" {
		t.Errorf("got %+v", def)
	}
}

func TestParseFrontmatter_UnclosedBlock(t *testing.T) {
	src := "---\nslug: orphan\n"
	def, err := ParseFrontmatter(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if def.Slug != "orphan" {
		t.Errorf("slug = %q", def.Slug)
	}
}

func TestDefaultSlugFromPath(t *testing.T) {
	cases := map[string]string{
		"hello.md":               "hello",
		"Hello World.md":         "hello-world",
		"weird___name.MD":        "weird___name",
		"./path/to/my-site.md":   "my-site",
		"trailing-spaces  .md":   "trailing-spaces",
		"___---___":              "___-___",
		"some/Über Geräte.md":    "ber-ger-te",
		"":                       "",
	}
	for in, want := range cases {
		got := DefaultSlugFromPath(in)
		if got != want {
			t.Errorf("DefaultSlugFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstH1_PicksFirstHeading(t *testing.T) {
	src := "Some preamble\n## H2\n# Real Title\n# Second Title\n"
	got := FirstH1(strings.NewReader(src))
	if got != "Real Title" {
		t.Fatalf("FirstH1 = %q", got)
	}
}

func TestFirstH1_SkipsFrontmatter(t *testing.T) {
	src := "---\nslug: x\n---\n\n# The Title\n"
	got := FirstH1(strings.NewReader(src))
	if got != "The Title" {
		t.Fatalf("FirstH1 = %q", got)
	}
}

func TestFirstH1_None(t *testing.T) {
	got := FirstH1(strings.NewReader("just body text"))
	if got != "" {
		t.Fatalf("FirstH1 = %q, want empty", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
