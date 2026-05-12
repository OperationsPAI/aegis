package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePackagedChart_FromHelmStdout(t *testing.T) {
	tmp := t.TempDir()
	tgz := filepath.Join(tmp, "mm-0.4.2.tgz")
	if err := os.WriteFile(tgz, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write dummy tgz: %v", err)
	}
	stdout := "Successfully packaged chart and saved it to: " + tgz + "\n"

	path, name, ver, err := resolvePackagedChart(tmp, stdout, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if path != tgz {
		t.Errorf("path = %q, want %q", path, tgz)
	}
	if name != "mm" {
		t.Errorf("name = %q, want %q", name, "mm")
	}
	if ver != "0.4.2" {
		t.Errorf("version = %q, want %q", ver, "0.4.2")
	}
}

func TestResolvePackagedChart_DirScanFallback(t *testing.T) {
	tmp := t.TempDir()
	tgz := filepath.Join(tmp, "teastore-1.2.3.tgz")
	if err := os.WriteFile(tgz, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write dummy tgz: %v", err)
	}

	path, name, ver, err := resolvePackagedChart(tmp, "", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if path != tgz {
		t.Errorf("path = %q, want %q", path, tgz)
	}
	if name != "teastore" {
		t.Errorf("name = %q, want %q", name, "teastore")
	}
	if ver != "1.2.3" {
		t.Errorf("version = %q, want %q", ver, "1.2.3")
	}
}

func TestResolvePackagedChart_AmbiguousTgz(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"a-1.0.0.tgz", "b-2.0.0.tgz"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if _, _, _, err := resolvePackagedChart(tmp, "", ""); err == nil {
		t.Fatal("expected error on ambiguous tgz, got nil")
	}
}

func TestOCIRegistryHost(t *testing.T) {
	cases := map[string]string{
		"oci://ghcr.io/example":               "ghcr.io",
		"oci://harbor.local:5000/charts":      "harbor.local:5000",
		"oci://registry-1.docker.io/acme/dev": "registry-1.docker.io",
	}
	for in, want := range cases {
		got, err := ociRegistryHost(in)
		if err != nil {
			t.Errorf("%s: unexpected err: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ociRegistryHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildOCIRef(t *testing.T) {
	got := buildOCIRef("oci://ghcr.io/example/", "mm")
	if got != "oci://ghcr.io/example/mm" {
		t.Errorf("buildOCIRef trimmed slash: got %q", got)
	}
	got = buildOCIRef("oci://ghcr.io/example", "mm")
	if got != "oci://ghcr.io/example/mm" {
		t.Errorf("buildOCIRef plain: got %q", got)
	}
}
