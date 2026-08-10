package dto

import (
	"os"
	"path/filepath"
	"testing"
)

// countLeaves walks a values map and counts scalar leaves. Mirrors the
// CLI's countLeafPaths so the assertion lines up with the user-facing
// "merged N overrides" log.
func countLeaves(m map[string]any) int {
	n := 0
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			n += countLeaves(sub)
			continue
		}
		n++
	}
	return n
}

// TestGetValuesMap_DistinctKeysFromHelmConfigValues regresses issue #476:
// the 11 distinct config_key rows on ts@1.0.6's helm_config_values must
// each surface as a leaf in the rendered values map, even when seed has
// been applied twice and the resolver sees 22 rows (every key duplicated).
// The historical bug: callers saw "merged 5 helm_config_values overrides"
// because the log line counted top-level groups (global, mysql, …) rather
// than distinct leaf paths.
func TestGetValuesMap_DistinctKeysFromHelmConfigValues(t *testing.T) {
	keys := []string{
		"services.tsUiDashboard.type",
		"global.security.allowInsecureImages",
		"global.image.repository",
		"mysql.image.repository",
		"rabbitmq.image.repository",
		"loadgenerator.initContainer.image",
		"global.otelcollector",
		"loadgenerator.opentelemetry.endpoint",
		"loadgenerator.image.repository",
		"loadgenerator.image.tag",
		"mysql.service.type",
	}

	items := make([]ParameterItem, 0, len(keys)*2)
	for i, k := range keys {
		items = append(items, ParameterItem{Key: k, Value: i})
	}
	// Duplicate every row — matches the live DB state where reseed inserted
	// each parameter_config twice (22 rows for 11 distinct keys).
	for i, k := range keys {
		items = append(items, ParameterItem{Key: k, Value: i})
	}

	hci := &HelmConfigItem{DynamicValues: items}
	got := hci.GetValuesMap()

	if n := countLeaves(got); n != len(keys) {
		t.Fatalf("expected %d distinct leaf paths, got %d (values=%#v)", len(keys), n, got)
	}

	mustLeaf := func(path ...string) {
		cur := any(got)
		for i, p := range path {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("expected map at %v[:%d], got %T", path, i, cur)
			}
			cur, ok = m[p]
			if !ok {
				t.Fatalf("missing key %q at %v[:%d]", p, path, i+1)
			}
		}
		if _, isMap := cur.(map[string]any); isMap {
			t.Fatalf("expected leaf at %v, got map", path)
		}
	}
	for _, k := range keys {
		mustLeaf(splitDotPath(k)...)
	}
}

func splitDotPath(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

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
		ValueFile: filepath.Join(t.TempDir(), "does-not-exist.yaml"),
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
