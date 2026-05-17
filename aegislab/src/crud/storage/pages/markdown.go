package pages

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"path"
	"sort"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark-highlighting/v2"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// htmlSanitizer is the allow-list applied to user-supplied markdown HTML.
// We enable goldmark's WithUnsafe so raw `<details>`, `<video>`, `<kbd>`,
// inline SVG, etc. survive the renderer; the bluemonday pass then strips
// `<script>`, event handlers, and any other XSS vector before the body is
// injected into the page template.
//
// Built once at package init — bluemonday policies are read-only after
// construction and safe to share across goroutines.
var htmlSanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// UGCPolicy doesn't cover HTML5 media; allow the common video / audio
	// + their <source> children so embedded clips work.
	p.AllowElements("video", "audio", "source", "track")
	p.AllowAttrs("controls", "autoplay", "loop", "muted", "poster", "preload", "playsinline", "width", "height").OnElements("video", "audio")
	p.AllowAttrs("src", "type", "media").OnElements("source")
	p.AllowAttrs("src", "kind", "srclang", "label", "default").OnElements("track")
	// Allow the chroma-highlighted code blocks to keep their inline style
	// attributes — the GFM renderer emits color spans rather than classes,
	// and stripping `style` would flatten every code listing back to mono
	// black text. bluemonday's style sanitizer keeps it safe.
	p.AllowStyles("color", "background-color", "font-weight").Globally()
	// Mermaid loader needs to find `<pre><code class="language-mermaid">`;
	// UGCPolicy already allows `class` on those, but keep it explicit so
	// future policy upgrades don't silently break the loader.
	p.AllowAttrs("class").OnElements("code", "pre", "span", "div")
	// Anchor IDs from goldmark's auto-heading-id extension; needed so
	// footnote backrefs and TOC anchors keep working.
	p.AllowAttrs("id").Globally()
	// UGCPolicy whitelists http/https/mailto. Extend to tel: so phone
	// links from markdown survive (UGCPolicy strips unknown schemes
	// silently).
	p.AllowURLSchemes("http", "https", "mailto", "tel")
	return p
}()

//go:embed assets/*
var assetsFS embed.FS

// pageTemplate is parsed lazily from the embedded layout file.
var pageTemplate = template.Must(template.ParseFS(assetsFS, "assets/page.html.tmpl"))

// NavItem is one entry in the sidebar nav list.
type NavItem struct {
	Label  string
	URL    string
	Active bool
}

// PageView is the variables block fed to the layout template.
type PageView struct {
	Title     string
	SiteTitle string
	Slug      string
	NavItems  []NavItem
	Body      template.HTML
}

// RenderInput is what RenderMarkdown needs from the caller.
type RenderInput struct {
	Slug          string
	SiteTitle     string
	CurrentPath   string   // site-relative .md path (e.g. "docs/foo.md")
	MarkdownPaths []string // site-relative .md paths for the sidebar
	Source        []byte
}

// RenderMarkdown converts the markdown source to a full HTML document
// suitable for serving as text/html.
//
// Behaviours:
//   - frontmatter `title` overrides the DB site title in <title>
//   - relative links + images are rewritten to /p/{slug}/<resolved>
//   - external / absolute / fragment / mailto URLs are untouched
func RenderMarkdown(in RenderInput) ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			extension.DefinitionList,
			extension.Typographer,
			extension.Linkify,
			meta.Meta,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			// Let raw HTML through goldmark — `<details>`, `<video>`,
			// `<kbd>`, inline SVG, etc. The bluemonday pass below is
			// the actual XSS gate.
			html.WithUnsafe(),
		),
	)

	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(in.Source), parser.WithContext(ctx))
	rewriteRelativeLinks(doc, in.Slug, in.CurrentPath)

	var body bytes.Buffer
	if err := md.Renderer().Render(&body, in.Source, doc); err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}
	sanitized := htmlSanitizer.SanitizeBytes(body.Bytes())

	title := in.SiteTitle
	if t := metaTitle(ctx); t != "" {
		title = t
	}
	if title == "" {
		title = in.CurrentPath
	}

	view := PageView{
		Title:     title,
		SiteTitle: firstNonEmpty(in.SiteTitle, in.Slug),
		Slug:      in.Slug,
		NavItems:  buildNav(in.Slug, in.CurrentPath, in.MarkdownPaths),
		Body:      template.HTML(sanitized), //nolint:gosec // bluemonday UGC policy sanitizes the rendered body above
	}
	var out bytes.Buffer
	if err := pageTemplate.Execute(&out, view); err != nil {
		return nil, fmt.Errorf("render layout: %w", err)
	}
	return out.Bytes(), nil
}

// rewriteRelativeLinks walks the AST and rewrites Link / Image destinations.
//
// Rules — see CLAUDE.md / pages-api.md:
//   - leave http://, https://, // (protocol-relative), mailto:, tel:, # (fragment)
//   - leave absolute paths starting with /
//   - everything else is resolved against the current document's directory
//     and prefixed with /p/{slug}/
func rewriteRelativeLinks(doc ast.Node, slug, currentPath string) {
	dir := path.Dir(currentPath)
	if dir == "." {
		dir = ""
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			v.Destination = []byte(rewriteURL(string(v.Destination), slug, dir))
		case *ast.Image:
			v.Destination = []byte(rewriteURL(string(v.Destination), slug, dir))
		}
		return ast.WalkContinue, nil
	})
}

func rewriteURL(raw, slug, dir string) string {
	if raw == "" {
		return raw
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(raw, "//") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(raw, "#") ||
		strings.HasPrefix(raw, "/") {
		return raw
	}
	var resolved string
	if dir != "" {
		resolved = path.Join(dir, raw)
	} else {
		resolved = path.Clean(raw)
	}
	return "/p/" + slug + "/" + resolved
}

func metaTitle(ctx parser.Context) string {
	data := meta.Get(ctx)
	if data == nil {
		return ""
	}
	if v, ok := data["title"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func buildNav(slug, currentPath string, paths []string) []NavItem {
	out := make([]NavItem, 0, len(paths))
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		out = append(out, NavItem{
			Label:  navLabel(p),
			URL:    "/p/" + slug + "/" + p,
			Active: p == currentPath,
		})
	}
	return out
}

func navLabel(p string) string {
	base := path.Base(p)
	base = strings.TrimSuffix(base, path.Ext(base))
	return base
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
