package pages

import (
	"strings"
	"testing"
)

// renderHelper drives RenderMarkdown with one source and returns the HTML
// body bytes as a string for inspection.
func renderHelper(t *testing.T, src, currentPath string) string {
	t.Helper()
	out, err := RenderMarkdown(RenderInput{
		Slug:          "demo",
		SiteTitle:     "Demo Site",
		CurrentPath:   currentPath,
		MarkdownPaths: []string{currentPath},
		Source:        []byte(src),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(out)
}

func TestRelativeMarkdownLinkRewritten(t *testing.T) {
	html := renderHelper(t, `See [other](other.md) and [nested](sub/x.md).`, "index.md")
	if !strings.Contains(html, `href="/p/demo/other.md"`) {
		t.Fatalf("relative .md link not rewritten:\n%s", html)
	}
	if !strings.Contains(html, `href="/p/demo/sub/x.md"`) {
		t.Fatalf("relative sub/.md link not rewritten:\n%s", html)
	}
}

func TestRelativeMarkdownLinkResolvesAgainstCurrentDir(t *testing.T) {
	html := renderHelper(t, `[neighbor](other.md)`, "docs/index.md")
	if !strings.Contains(html, `href="/p/demo/docs/other.md"`) {
		t.Fatalf("link not resolved against docs/:\n%s", html)
	}
}

// TestRelativeImageResolvesAgainstCurrentDir documents the dir-of-current
// rule for images: `![x](../up.md)` from `sub/dir/here.md` must resolve to
// `/p/{slug}/sub/up.md`, not `/p/{slug}/sub/dir/up.md`.
func TestRelativeImageResolvesAgainstCurrentDir(t *testing.T) {
	html := renderHelper(t, `![logo](../up.png)`, "sub/dir/here.md")
	if !strings.Contains(html, `src="/p/demo/sub/up.png"`) {
		t.Fatalf("image not resolved relative to dir:\n%s", html)
	}
}

func TestRelativeImageRewritten(t *testing.T) {
	html := renderHelper(t, `![logo](assets/logo.png)`, "index.md")
	if !strings.Contains(html, `src="/p/demo/assets/logo.png"`) {
		t.Fatalf("image not rewritten:\n%s", html)
	}
}

func TestAbsoluteURLUntouched(t *testing.T) {
	html := renderHelper(t, `[ext](https://example.com/x) [abs](/already)`, "index.md")
	if !strings.Contains(html, `href="https://example.com/x"`) {
		t.Fatalf("absolute URL rewritten:\n%s", html)
	}
	if !strings.Contains(html, `href="/already"`) {
		t.Fatalf("absolute path rewritten:\n%s", html)
	}
	if strings.Contains(html, `/p/demo/https://`) {
		t.Fatalf("external URL was wrongly prefixed:\n%s", html)
	}
}

// TestProtocolRelativeURLUntouched: `//evil.com/path` is a protocol-relative
// URL — must pass through unchanged, never prefixed with /p/{slug}/.
func TestProtocolRelativeURLUntouched(t *testing.T) {
	html := renderHelper(t, `[ext](//evil.com/path)`, "index.md")
	if !strings.Contains(html, `href="//evil.com/path"`) {
		t.Fatalf("protocol-relative URL altered:\n%s", html)
	}
	if strings.Contains(html, `/p/demo//evil.com`) {
		t.Fatalf("protocol-relative URL was wrongly prefixed:\n%s", html)
	}
}

func TestFragmentOnlyUntouched(t *testing.T) {
	html := renderHelper(t, `[jump](#section)`, "index.md")
	if !strings.Contains(html, `href="#section"`) {
		t.Fatalf("fragment-only link rewritten:\n%s", html)
	}
}

func TestMailtoAndTelUntouched(t *testing.T) {
	html := renderHelper(t, `[me](mailto:hi@example.com) [call](tel:+15551234)`, "index.md")
	if !strings.Contains(html, `href="mailto:hi@example.com"`) {
		t.Fatalf("mailto rewritten:\n%s", html)
	}
	if !strings.Contains(html, `href="tel:+15551234"`) {
		t.Fatalf("tel rewritten:\n%s", html)
	}
}

func TestFrontmatterTitleOverridesSiteTitle(t *testing.T) {
	src := "---\ntitle: Custom Page Title\n---\n# body"
	out, err := RenderMarkdown(RenderInput{
		Slug:          "demo",
		SiteTitle:     "Site Wide Title",
		CurrentPath:   "index.md",
		MarkdownPaths: []string{"index.md"},
		Source:        []byte(src),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(out), "<title>Custom Page Title · demo</title>") {
		t.Fatalf("frontmatter title not honored:\n%s", string(out))
	}
}

// TestXSS_RawScriptTagEscaped: a `<script>` block in markdown source must
// never appear in the rendered output. goldmark without WithUnsafe drops
// raw HTML to `<!-- raw HTML omitted -->` comments.
func TestXSS_RawScriptTagEscaped(t *testing.T) {
	html := renderHelper(t, `<script>alert(1)</script>`, "index.md")
	lower := strings.ToLower(html)
	if strings.Contains(lower, "<script") {
		t.Fatalf("raw <script> leaked into output:\n%s", html)
	}
	if strings.Contains(html, "alert(1)") {
		t.Fatalf("script body leaked into output:\n%s", html)
	}
}

// TestXSS_JavaScriptHrefInRawHTML: a raw `<a href="javascript:…">` must
// not pass through as a clickable javascript: link.
func TestXSS_JavaScriptHrefInRawHTML(t *testing.T) {
	html := renderHelper(t, `<a href="javascript:alert(1)">x</a>`, "index.md")
	lower := strings.ToLower(html)
	if strings.Contains(lower, `href="javascript:`) {
		t.Fatalf("javascript: href leaked from raw HTML:\n%s", html)
	}
}

// TestXSS_JavaScriptHrefInMarkdownLink: a markdown-syntax link with a
// javascript: destination must be rewritten so the rendered href is not a
// live javascript: URI. The walker prefixes the (non-special) destination
// with /p/{slug}/, neutralising it.
func TestXSS_JavaScriptHrefInMarkdownLink(t *testing.T) {
	html := renderHelper(t, `[x](javascript:alert(1))`, "index.md")
	lower := strings.ToLower(html)
	if strings.Contains(lower, `href="javascript:`) {
		t.Fatalf("javascript: href survived rewrite:\n%s", html)
	}
}

func TestCleanRenderPath_DefaultsToIndex(t *testing.T) {
	cases := map[string]string{
		"":           "index.md",
		"/":          "index.md",
		"docs/":      "docs/index.md",
		"docs":       "docs",
		"a/b.md":     "a/b.md",
		"%2Findex.md": "index.md", // percent-decoded leading slash
	}
	for in, want := range cases {
		got, err := cleanRenderPath(in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestCleanRenderPath_RejectsTraversal(t *testing.T) {
	cases := []string{"../etc/passwd", "a/../../b"}
	for _, in := range cases {
		if _, err := cleanRenderPath(in); err == nil {
			t.Fatalf("%q: expected error", in)
		}
	}
}
