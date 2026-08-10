package blobref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_Remote(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantBucket string
		wantKey    string
	}{
		{"simple", "aegis-pages:my-site/index.md", "aegis-pages", "my-site/index.md"},
		{"empty key root", "aegis-pages:", "aegis-pages", ""},
		{"key with colons", "bucket:key:with:colons", "bucket", "key:with:colons"},
		{"dots and underscores", "a1.b_c-d:foo/bar.txt", "a1.b_c-d", "foo/bar.txt"},
		{"min length bucket", "ab:k", "ab", "k"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.in, err)
			}
			if r.Local {
				t.Fatalf("Parse(%q) returned Local; want remote", tc.in)
			}
			if r.Bucket != tc.wantBucket {
				t.Errorf("bucket = %q, want %q", r.Bucket, tc.wantBucket)
			}
			if r.Key != tc.wantKey {
				t.Errorf("key = %q, want %q", r.Key, tc.wantKey)
			}
		})
	}
}

func TestParse_Local(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(existing, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"explicit relative", "./foo.md", "./foo.md"},
		{"parent", "../foo.md", "../foo.md"},
		{"absolute", "/abs/path.md", "/abs/path.md"},
		{"bare filename", "foo.md", "foo.md"},
		{"nested relative", "relative/path.md", "relative/path.md"},
		{"windows-style C colon", `C:\Users\me.txt`, `C:\Users\me.txt`},
		{"existing file via stat", existing, existing},
		{"dot", ".", "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.in, err)
			}
			if !r.Local {
				t.Fatalf("Parse(%q) returned remote (bucket=%q); want Local", tc.in, r.Bucket)
			}
			if r.Key != tc.want {
				t.Errorf("key = %q, want %q", r.Key, tc.want)
			}
		})
	}
}

func TestParse_Stdin(t *testing.T) {
	r, err := Parse("-")
	if err != nil {
		t.Fatalf("Parse(\"-\") returned error: %v", err)
	}
	if !r.Local || !r.Stdin {
		t.Fatalf("Parse(\"-\") = %+v; want Local && Stdin", r)
	}
}

func TestParse_Tilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir unavailable: %v", err)
	}
	r, err := Parse("~/foo.md")
	if err != nil {
		t.Fatalf("Parse(~/foo.md) returned error: %v", err)
	}
	if !r.Local {
		t.Fatalf("Parse(~/foo.md) returned remote; want Local")
	}
	if !strings.HasPrefix(r.Key, home) {
		t.Errorf("key = %q, want prefix %q", r.Key, home)
	}
}

func TestParse_RejectsBadKey(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Errorf("Parse(\"\") should error")
	}
	if _, err := Parse("bucket:/leading-slash"); err == nil {
		t.Errorf("Parse(bucket:/leading-slash) should error")
	}
}

func TestIsValidBucketName(t *testing.T) {
	good := []string{"aegis-pages", "ab", "a1.b_c-d", "foo123", "a-very-long-bucket-name-that-is-still-ok-12345"}
	for _, s := range good {
		if !IsValidBucketName(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	bad := []string{"", "A", "1", ".foo", "-foo", "FOO", "foo!", "x", strings.Repeat("a", 64)}
	for _, s := range bad {
		if IsValidBucketName(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestString_RoundTrip(t *testing.T) {
	cases := []string{"b:k", "b:k/nested.txt", "./foo.md", "/abs/p"}
	for _, in := range cases {
		r := MustParse(in)
		if got := r.String(); got != in {
			t.Errorf("String() = %q, want %q", got, in)
		}
	}
	if MustParse("-").String() != "-" {
		t.Errorf("stdin String() should be \"-\"")
	}
}
