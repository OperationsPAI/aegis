// Package blobref parses path arguments that may refer to either a local
// filesystem path or a remote object in a blob bucket. The canonical form for
// remote paths is `<bucket>:<key>` (mc-style), where the bucket name must
// match a strict regex; anything else is treated as Local.
package blobref

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Ref is a parsed reference to either a local path or a remote object.
type Ref struct {
	// Bucket is empty when Local is true.
	Bucket string
	// Key is the object key for remote refs (no leading slash), or the
	// filesystem path for local refs.
	Key string
	// Local is true when the ref points at a filesystem path or stdin/stdout.
	Local bool
	// Stdin is true when the user passed "-" as the path.
	Stdin bool
}

// String renders the ref back to its canonical input form.
func (r Ref) String() string {
	if r.Stdin {
		return "-"
	}
	if r.Local {
		return r.Key
	}
	return r.Bucket + ":" + r.Key
}

// bucketRegex matches the same identifier shape the backend enforces for
// bucket names: lowercase letters, digits, dots, dashes, and underscores;
// must start with a letter or digit; 2..63 chars total.
var bucketRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,62}$`)

// IsValidBucketName reports whether s is a syntactically valid bucket name.
func IsValidBucketName(s string) bool {
	return bucketRegex.MatchString(s)
}

// Parse resolves s into either a Local or remote Ref.
//
// Disambiguation rules (the order matters):
//   - "-" -> Local stdin marker.
//   - Strings beginning with "./", "/", "~", or "../" are always Local.
//     This covers absolute paths, explicit-relative paths, and tilde paths.
//   - If s contains no ':', it is Local.
//   - If the part before the first ':' is a valid bucket name, the ref is
//     remote (everything after the first ':' is the key, including extra
//     colons). Empty key means "bucket root" — used by `ls` and `rm -r`.
//   - Otherwise (prefix not a valid bucket name): if a local file or dir
//     exists at s, treat as Local; if not, fall through to Local and let the
//     caller produce a "file not found" error from os.Stat.
//
// Edge cases:
//   - Windows-style "C:\path" parses as Local because "C" fails the bucket
//     name length check (regex requires >= 2 chars).
//   - Bucket-root with a stray trailing slash is normalized: "b:" stays
//     "b:" (empty key) but "b:/" is rejected (use "b:" or "b:foo").
func Parse(s string) (Ref, error) {
	if s == "" {
		return Ref{}, fmt.Errorf("empty path")
	}
	if s == "-" {
		return Ref{Local: true, Stdin: true, Key: "-"}, nil
	}

	// Explicit-local prefixes short-circuit colon detection. We expand "~"
	// here so downstream code doesn't have to.
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "/") || s == "." || s == ".." {
		return Ref{Local: true, Key: s}, nil
	}
	if strings.HasPrefix(s, "~") {
		expanded, err := expandTilde(s)
		if err != nil {
			return Ref{}, err
		}
		return Ref{Local: true, Key: expanded}, nil
	}

	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return Ref{Local: true, Key: s}, nil
	}

	prefix, rest := s[:idx], s[idx+1:]
	if IsValidBucketName(prefix) {
		// Reject "bucket:/foo" — leading slash on the key is ambiguous and
		// not a shape the backend accepts.
		if strings.HasPrefix(rest, "/") {
			return Ref{}, fmt.Errorf("invalid remote ref %q: object key must not start with '/'", s)
		}
		return Ref{Bucket: prefix, Key: rest}, nil
	}

	// Prefix isn't a valid bucket name. If something exists locally, prefer
	// the Local interpretation; otherwise still return Local so the caller's
	// os.Stat surfaces the "not found" diagnostic instead of a vague parser
	// error.
	if _, err := os.Stat(s); err == nil {
		return Ref{Local: true, Key: s}, nil
	}
	return Ref{Local: true, Key: s}, nil
}

// MustParse panics on error. Intended for tests and constants.
func MustParse(s string) Ref {
	r, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return r
}

func expandTilde(p string) (string, error) {
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return filepath.Join(home, p[2:]), nil
	}
	// "~user/..." is not supported on purpose — Go has no portable lookup.
	return p, nil
}
