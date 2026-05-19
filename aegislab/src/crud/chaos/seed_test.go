package chaos

import (
	"testing"
)

// TestPodKillSeedShape locks in the structural shape of the pod_kill
// seed: it MUST exist, be `stable` per §11 step 1, have a target schema
// requiring namespace+app, and a param schema that admits duration_s.
// Regressions here would silently break the conformance harness and the
// Chaos-Mesh executor wiring.
func TestPodKillSeedShape(t *testing.T) {
	ts := podKillTargetSchema()
	props, ok := ts["properties"].(map[string]any)
	if !ok {
		t.Fatalf("target_schema.properties missing")
	}
	for _, k := range []string{"namespace", "app"} {
		if _, ok := props[k]; !ok {
			t.Fatalf("target_schema missing required key %q", k)
		}
	}
	required, _ := ts["required"].([]any)
	if len(required) != 2 {
		t.Fatalf("target_schema.required must enumerate namespace+app, got %v", required)
	}

	ps := podKillParamSchema()
	pprops, ok := ps["properties"].(map[string]any)
	if !ok {
		t.Fatalf("param_schema.properties missing")
	}
	if _, ok := pprops["duration_s"]; !ok {
		t.Fatalf("param_schema must allow duration_s")
	}

	contract := podKillObservableContract()
	if contract["name"] != "pod_kill" {
		t.Fatalf("contract.name mismatch: %v", contract["name"])
	}
}
