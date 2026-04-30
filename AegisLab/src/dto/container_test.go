package dto

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetValuesMap_EmptyValueFile_NoPanic regresses the live byte-cluster
// outage where a 0-byte helm-values yaml caused 79 worker panics with
// "assignment to entry in nil map" inside GetValuesMap. The defensive guard
// must keep root non-nil so DynamicValues merge succeeds.
func TestGetValuesMap_EmptyValueFile_NoPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty_values.yaml")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	hci := &HelmConfigItem{
		ValueFile: path,
		DynamicValues: []ParameterItem{
			{Key: "global.image.tag", Value: "v1.2.3"},
		},
	}

	got := hci.GetValuesMap()
	global, ok := got["global"].(map[string]any)
	if !ok {
		t.Fatalf("expected global map, got %T (%v)", got["global"], got)
	}
	image, ok := global["image"].(map[string]any)
	if !ok {
		t.Fatalf("expected image map, got %T", global["image"])
	}
	if image["tag"] != "v1.2.3" {
		t.Fatalf("expected tag=v1.2.3, got %v", image["tag"])
	}
}

// TestGetValuesMap_NonexistentValueFile_FallsThrough confirms the historical
// "delete the 0-byte file" workaround still works: missing ValueFile is a
// non-fatal load error, root stays empty, DynamicValues merge proceeds.
func TestGetValuesMap_NonexistentValueFile_FallsThrough(t *testing.T) {
	hci := &HelmConfigItem{
		ValueFile: "/tmp/this-file-does-not-exist-aegis-315.yaml",
		DynamicValues: []ParameterItem{
			{Key: "replicas", Value: 2},
		},
	}

	got := hci.GetValuesMap()
	if got["replicas"] != 2 {
		t.Fatalf("expected replicas=2, got %v", got["replicas"])
	}
}

// TestGetValuesMap_ScalarTopLevelYAML_NoPanic covers a YAML file whose root
// node is a scalar (not a mapping). LoadYAMLFile will reject it with a parse
// error — the defensive guard must still leave root as a usable empty map.
func TestGetValuesMap_ScalarTopLevelYAML_NoPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scalar.yaml")
	if err := os.WriteFile(path, []byte("just-a-scalar\n"), 0o644); err != nil {
		t.Fatalf("write scalar file: %v", err)
	}

	hci := &HelmConfigItem{
		ValueFile: path,
		DynamicValues: []ParameterItem{
			{Key: "service.port", Value: 8080},
		},
	}

	got := hci.GetValuesMap()
	svc, ok := got["service"].(map[string]any)
	if !ok {
		t.Fatalf("expected service map, got %T", got["service"])
	}
	if svc["port"] != 8080 {
		t.Fatalf("expected port=8080, got %v", svc["port"])
	}
}
