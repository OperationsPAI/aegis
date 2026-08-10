package helm

import (
	"os"
	"path/filepath"
	"testing"

	"helm.sh/helm/v3/pkg/cli"
)

// Reproduces issue #374: cached <chart>-<oldver>.tgz must NOT shadow a
// caller-requested newer <chart>-<newver>.tgz.
func TestFindCachedChart_VersionMismatchMisses(t *testing.T) {
	tmp := t.TempDir()
	staleTgz := filepath.Join(tmp, "trainticket-0.3.0.tgz")
	if err := os.WriteFile(staleTgz, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	settings := cli.New()
	settings.RepositoryCache = tmp

	got, err := findCachedChart(settings, "operations-pai/trainticket", "0.3.1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "" {
		t.Fatalf("expected cache miss for version 0.3.1 with only 0.3.0 cached; got %q", got)
	}
}

func TestFindCachedChart_VersionMatchHits(t *testing.T) {
	tmp := t.TempDir()
	freshTgz := filepath.Join(tmp, "trainticket-0.3.1.tgz")
	if err := os.WriteFile(freshTgz, []byte("fresh"), 0o644); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	settings := cli.New()
	settings.RepositoryCache = tmp

	got, err := findCachedChart(settings, "operations-pai/trainticket", "0.3.1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != freshTgz {
		t.Fatalf("expected cache hit at %q; got %q", freshTgz, got)
	}
}

// When the caller doesn't pin a version (empty string), legacy any-version
// behavior is preserved — the existing reseed flow that omits version (e.g.
// during initial bootstrap) must still pick up whatever tgz is on disk.
func TestFindCachedChart_EmptyVersionFallsBackToAnyMatch(t *testing.T) {
	tmp := t.TempDir()
	tgz := filepath.Join(tmp, "trainticket-0.3.0.tgz")
	if err := os.WriteFile(tgz, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	settings := cli.New()
	settings.RepositoryCache = tmp

	got, err := findCachedChart(settings, "operations-pai/trainticket", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != tgz {
		t.Fatalf("expected legacy any-version match at %q; got %q", tgz, got)
	}
}
