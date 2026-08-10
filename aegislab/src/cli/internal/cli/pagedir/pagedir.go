// Package pagedir collects pure helpers for `aegisctl page push`:
//   - walking a directory into a slice of relative file paths suitable for
//     a /api/v2/pages multipart upload,
//   - parsing the YAML-ish frontmatter (slug / title) at the top of a markdown
//     file so the CLI can default them when --slug / --title aren't passed.
//
// Everything here is filesystem + string manipulation only — no HTTP and no
// cobra wiring — so it stays unit-testable without spinning up a server.
package pagedir

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Hard client-side limits. The server enforces its own caps; these short-
// circuit egregious uploads with a friendly error before the request is
// built. Keep in sync with the values documented in aegisctl-page.md.
const (
	MaxFileBytes  = 10 * 1024 * 1024 // 10 MiB per file
	MaxTotalBytes = 50 * 1024 * 1024 // 50 MiB total
	MaxFileCount  = 200
)

// Entry is a single file to upload as part of a page site.
type Entry struct {
	// AbsPath is the file's location on disk (used to read bytes).
	AbsPath string
	// RelPath is the site-relative path used as the multipart part filename
	// (and field name). Forward-slashes; no leading slash; no `..`.
	RelPath string
	// Size in bytes (from os.Stat at walk time).
	Size int64
}

// Plan summarises a directory walk for both the dry-run path and the live
// upload path. It is the only thing page push needs to know about the
// filesystem.
type Plan struct {
	Entries    []Entry
	TotalBytes int64
	// SingleFile is true when the user pointed at a `.md` file directly. In
	// that case there is exactly one Entry whose RelPath is the basename.
	SingleFile bool
}

// CollectFromPath examines `path` and returns a Plan describing what would be
// uploaded.
//
//   - If `path` is a regular .md file, Plan.SingleFile is true and Plan has
//     one Entry whose RelPath is filepath.Base(path).
//   - If `path` is a directory, every regular file under it is walked
//     (dotfiles and symlinks are skipped). At least one .md must exist
//     somewhere in the tree.
//
// Path-traversal is rejected at walk time: every RelPath is cleaned and
// re-checked to ensure it starts with neither `/` nor `..`.
func CollectFromPath(path string) (*Plan, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%q is a symlink — page push refuses to follow symlinks", path)
	}

	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%q is not a regular file or directory", path)
		}
		if !isMarkdown(path) {
			return nil, fmt.Errorf("%q is not a .md file; single-file push requires markdown", path)
		}
		size := info.Size()
		if size > MaxFileBytes {
			return nil, fmt.Errorf("%q is %d bytes — exceeds the %d-byte per-file limit",
				path, size, MaxFileBytes)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("abs %q: %w", path, err)
		}
		return &Plan{
			SingleFile: true,
			TotalBytes: size,
			Entries: []Entry{{
				AbsPath: abs,
				RelPath: filepath.Base(path),
				Size:    size,
			}},
		}, nil
	}

	rootAbs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("abs %q: %w", path, err)
	}

	var (
		entries    []Entry
		totalBytes int64
		hasMD      bool
	)
	walkErr := filepath.Walk(rootAbs, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip dotfiles / dot-dirs anywhere in the tree (.git, .DS_Store, …).
		base := filepath.Base(p)
		if base != "." && strings.HasPrefix(base, ".") {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.IsDir() {
			return nil
		}
		// Refuse symlinks; only regular files travel.
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %q encountered under %q — page push refuses to follow symlinks", p, path)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, p)
		if err != nil {
			return fmt.Errorf("rel %q: %w", p, err)
		}
		rel = filepath.ToSlash(rel)
		if err := validateRel(rel); err != nil {
			return err
		}
		size := fi.Size()
		if size > MaxFileBytes {
			return fmt.Errorf("file %q is %d bytes — exceeds the %d-byte per-file limit",
				rel, size, MaxFileBytes)
		}
		entries = append(entries, Entry{AbsPath: p, RelPath: rel, Size: size})
		totalBytes += size
		if isMarkdown(p) {
			hasMD = true
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("directory %q contains no uploadable files", path)
	}
	if !hasMD {
		return nil, fmt.Errorf("directory %q contains no .md file; at least one is required", path)
	}
	if len(entries) > MaxFileCount {
		return nil, fmt.Errorf("directory %q contains %d files — exceeds the %d-file limit",
			path, len(entries), MaxFileCount)
	}
	if totalBytes > MaxTotalBytes {
		return nil, fmt.Errorf("directory %q is %d bytes — exceeds the %d-byte total upload limit",
			path, totalBytes, MaxTotalBytes)
	}
	// Deterministic order keeps dry-run output stable across platforms.
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath })
	return &Plan{Entries: entries, TotalBytes: totalBytes}, nil
}

// validateRel rejects any path that escapes the upload root. It is run after
// filepath.Rel has produced a forward-slash path.
func validateRel(rel string) error {
	if rel == "" || rel == "." {
		return errors.New("empty relative path produced by walker")
	}
	if strings.HasPrefix(rel, "/") {
		return fmt.Errorf("relative path %q is absolute", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean != rel {
		return fmt.Errorf("relative path %q is not normalised (got %q after Clean)", rel, clean)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("relative path %q escapes the upload root", rel)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return fmt.Errorf("relative path %q contains a `..` segment", rel)
		}
	}
	return nil
}

func isMarkdown(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".md" || ext == ".markdown"
}

// FrontmatterDefaults are the fields page push pulls from the top of a
// markdown file when the user didn't pass --slug / --title explicitly. The
// caller is in charge of choosing which file to inspect (the single .md, or
// `index.md` in a directory walk).
type FrontmatterDefaults struct {
	Slug  string
	Title string
}

// ParseFrontmatter scans the head of `r` for a `---`-delimited YAML-ish block
// and returns whatever `slug:` and `title:` keys it finds. Anything outside
// the very first such block is ignored; this is intentionally _not_ a full
// YAML parser — it handles only the conventional Markdown frontmatter shape:
//
//	---
//	slug: my-site
//	title: My Site
//	---
//
// If `r` does not begin with `---` on its own line, an empty Defaults is
// returned (no error). The caller passes the file as-is.
//
// Quoting: single- and double-quoted values are stripped of their surrounding
// quotes; bare values are taken verbatim with surrounding whitespace trimmed.
func ParseFrontmatter(r io.Reader) (FrontmatterDefaults, error) {
	var def FrontmatterDefaults
	scanner := bufio.NewScanner(r)
	// Generous buffer for long frontmatter lines (titles can be longish).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return def, err
		}
		return def, nil
	}
	first := strings.TrimRight(scanner.Text(), "\r")
	if strings.TrimSpace(first) != "---" {
		return def, nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "---" {
			return def, nil
		}
		if k, v, ok := splitKV(line); ok {
			v = unquote(v)
			switch strings.ToLower(k) {
			case "slug":
				def.Slug = v
			case "title":
				def.Title = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return def, err
	}
	// EOF before closing `---`: not fatal — return whatever we collected.
	return def, nil
}

// ParseFrontmatterFile is a convenience wrapper around ParseFrontmatter that
// opens `path` and closes it before returning.
func ParseFrontmatterFile(path string) (FrontmatterDefaults, error) {
	f, err := os.Open(path) //nolint:gosec // caller-supplied path; CLI runs as the invoking user
	if err != nil {
		return FrontmatterDefaults{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return ParseFrontmatter(f)
}

func splitKV(line string) (string, string, bool) {
	// `key: value` — note the colon must be followed by space or EOL to avoid
	// catching URLs like `http://...` inside a key-less line.
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:idx])
	if k == "" || strings.ContainsAny(k, " \t") {
		return "", "", false
	}
	rest := line[idx+1:]
	return k, strings.TrimSpace(rest), true
}

func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// DefaultSlugFromPath produces a slug from a filesystem path by:
//
//   - taking the basename,
//   - stripping the .md/.markdown extension if present,
//   - lower-casing and replacing non `[a-z0-9_-]` runs with `-`,
//   - trimming leading/trailing `-`.
//
// Returns "" if the result would be empty. The server enforces its own slug
// regex; this is a best-effort default that the user can override with --slug.
func DefaultSlugFromPath(path string) string {
	base := filepath.Base(path)
	if isMarkdown(base) {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	base = strings.ToLower(base)
	var b strings.Builder
	b.Grow(len(base))
	lastDash := true
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '-':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

// FirstH1 scans `r` for the first markdown heading at H1 level (`# Title`)
// and returns its text, or "" if none was found in the first 200 lines. This
// is used as a fallback title when frontmatter doesn't carry one.
func FirstH1(r io.Reader) string {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inFrontmatter := false
	frontmatterSeen := false
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > 200 {
			return ""
		}
		line := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if lines == 1 && trimmed == "---" {
			inFrontmatter = true
			frontmatterSeen = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
			}
			continue
		}
		_ = frontmatterSeen
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}
