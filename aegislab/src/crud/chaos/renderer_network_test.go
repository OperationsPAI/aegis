package chaos

import (
	"strings"
	"testing"
)

// TestRendererRegistry locks in that the 6 network renderers register
// under their `network_*` capability names and that the registry routes
// lookups correctly. Regressions here would silently drop a Capability
// from the executor's `SupportedCapabilities()` surface.
func TestRendererRegistry(t *testing.T) {
	want := []string{
		"network_bandwidth",
		"network_corrupt",
		"network_delay",
		"network_duplicate",
		"network_loss",
		"network_partition",
		"pod_kill",
	}
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("renderer registry missing capability %q (have %v)", w, got)
		}
	}
	if got["pod_kill"] != CapStable {
		t.Errorf("pod_kill must be %q, got %q", CapStable, got["pod_kill"])
	}
	for _, n := range want {
		if strings.HasPrefix(n, "network_") && got[n] != CapExperimental {
			t.Errorf("%s must be %q, got %q", n, CapExperimental, got[n])
		}
	}

	if _, err := lookupRenderer("network_delay"); err != nil {
		t.Errorf("lookup network_delay: %v", err)
	}
	if _, err := lookupRenderer("does-not-exist"); err == nil {
		t.Error("lookupRenderer should reject unknown capability")
	}
}

// TestNetworkDelayRender validates that network_delay produces a
// NetworkChaos CR with the expected action, selector, target, and
// TC-parameter shape. Tests the integration between ValidateParams →
// RenderCR end-to-end.
func TestNetworkDelayRender(t *testing.T) {
	r, err := lookupRenderer("network_delay")
	if err != nil {
		t.Fatalf("lookupRenderer: %v", err)
	}
	target := map[string]any{
		"namespace":      "ts",
		"source_app":     "ts-order-service",
		"target_service": "ts-user-service",
		"direction":      "to",
	}
	params := map[string]any{
		"latency_ms":      100,
		"jitter_ms":       10,
		"correlation_pct": 25,
		"duration_s":      60,
	}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	if err := r.ValidateParams(params); err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}

	cr, err := r.RenderCR(SystemContext{}, "aegis-netdelay-abc123", "ts", target, params)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	obj := cr.Object
	if obj["kind"] != "NetworkChaos" {
		t.Errorf("kind = %v, want NetworkChaos", obj["kind"])
	}
	if obj["apiVersion"] != "chaos-mesh.org/v1alpha1" {
		t.Errorf("apiVersion = %v", obj["apiVersion"])
	}
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec missing")
	}
	if spec["action"] != "delay" {
		t.Errorf("spec.action = %v, want delay", spec["action"])
	}
	if spec["direction"] != "to" {
		t.Errorf("spec.direction = %v", spec["direction"])
	}
	if spec["duration"] != "60s" {
		t.Errorf("spec.duration = %v, want 60s", spec["duration"])
	}
	delay, ok := spec["delay"].(map[string]any)
	if !ok {
		t.Fatalf("spec.delay missing or wrong type")
	}
	if delay["latency"] != "100ms" {
		t.Errorf("spec.delay.latency = %v, want 100ms", delay["latency"])
	}
	if delay["jitter"] != "10ms" {
		t.Errorf("spec.delay.jitter = %v, want 10ms", delay["jitter"])
	}
	if delay["correlation"] != "25" {
		t.Errorf("spec.delay.correlation = %v, want 25", delay["correlation"])
	}

	sel, _ := spec["selector"].(map[string]any)
	labels, _ := sel["labelSelectors"].(map[string]any)
	if labels["app"] != "ts-order-service" {
		t.Errorf("source selector.app = %v", labels["app"])
	}
	tgt, _ := spec["target"].(map[string]any)
	tgtSel, _ := tgt["selector"].(map[string]any)
	tgtLabels, _ := tgtSel["labelSelectors"].(map[string]any)
	if tgtLabels["app"] != "ts-user-service" {
		t.Errorf("target selector.app = %v", tgtLabels["app"])
	}
}

// TestNetworkRenderersActionMapping is a table-driven sweep over all 6
// network capabilities asserting:
//   1. spec.action matches the chaos-mesh NetworkChaosAction string
//   2. the per-action TC sub-object is present (or absent for partition)
//   3. ValidateParams enforces the required field
func TestNetworkRenderersActionMapping(t *testing.T) {
	target := map[string]any{
		"namespace":      "ts",
		"source_app":     "src",
		"target_service": "dst",
	}
	cases := []struct {
		capability   string
		wantAction   string
		wantSubKey   string // "" means partition (no sub-object)
		validParams  map[string]any
		missingField string
	}{
		{"network_delay", "delay", "delay", map[string]any{"latency_ms": 100}, "latency_ms"},
		{"network_loss", "loss", "loss", map[string]any{"loss_pct": 10}, "loss_pct"},
		{"network_duplicate", "duplicate", "duplicate", map[string]any{"duplicate_pct": 10}, "duplicate_pct"},
		{"network_corrupt", "corrupt", "corrupt", map[string]any{"corrupt_pct": 10}, "corrupt_pct"},
		{"network_bandwidth", "bandwidth", "bandwidth", map[string]any{"rate_kbps": 1024, "limit": 20480, "buffer": 10240}, "rate_kbps"},
		{"network_partition", "partition", "", map[string]any{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.capability, func(t *testing.T) {
			r, err := lookupRenderer(tc.capability)
			if err != nil {
				t.Fatalf("lookupRenderer: %v", err)
			}
			if err := r.ValidateTarget(target); err != nil {
				t.Fatalf("ValidateTarget: %v", err)
			}
			if err := r.ValidateParams(tc.validParams); err != nil {
				t.Fatalf("ValidateParams(valid): %v", err)
			}
			cr, err := r.RenderCR(SystemContext{}, "aegis-x", "ts", target, tc.validParams)
			if err != nil {
				t.Fatalf("RenderCR: %v", err)
			}
			spec := cr.Object["spec"].(map[string]any)
			if spec["action"] != tc.wantAction {
				t.Errorf("action = %v, want %v", spec["action"], tc.wantAction)
			}
			if tc.wantSubKey != "" {
				if _, ok := spec[tc.wantSubKey]; !ok {
					t.Errorf("missing spec.%s sub-object", tc.wantSubKey)
				}
			} else {
				for _, k := range []string{"delay", "loss", "duplicate", "corrupt", "bandwidth"} {
					if _, present := spec[k]; present {
						t.Errorf("partition should not set spec.%s", k)
					}
				}
			}

			if tc.missingField != "" {
				bad := map[string]any{}
				for k, v := range tc.validParams {
					if k != tc.missingField {
						bad[k] = v
					}
				}
				if err := r.ValidateParams(bad); err == nil {
					t.Errorf("ValidateParams should reject missing %s", tc.missingField)
				}
			}
		})
	}
}

// TestNetworkTargetValidation covers the boundary checks on target shape
// — namespace/source_app/target_service are required, direction is
// enum-constrained.
func TestNetworkTargetValidation(t *testing.T) {
	r, _ := lookupRenderer("network_delay")
	bad := []map[string]any{
		{"source_app": "a", "target_service": "b"},                                          // no namespace
		{"namespace": "ns", "target_service": "b"},                                          // no source_app
		{"namespace": "ns", "source_app": "a"},                                              // no target_service
		{"namespace": "ns", "source_app": "a", "target_service": "b", "direction": "x"},    // unknown direction
		{"namespace": "ns", "source_app": "a", "target_service": "b", "direction": "from"}, // not supported in step 2
		{"namespace": "ns", "source_app": "a", "target_service": "b", "direction": "both"}, // not supported in step 2
	}
	for i, tgt := range bad {
		if err := r.ValidateTarget(tgt); err == nil {
			t.Errorf("case %d should error: %v", i, tgt)
		}
	}
}

// TestDeriveHandleNamespaceOnly asserts the §8 contract that DeriveHandle
// requires only the fields the CR name depends on (namespace). Full
// target shape is enforced at Apply. Regression-guard against silently
// tightening the contract during a registry refactor.
func TestDeriveHandleNamespaceOnly(t *testing.T) {
	e := &ChaosMeshExecutor{}
	target := map[string]any{"namespace": "ts"} // no `app`, no `source_app`/`target_service`
	for _, capability := range []string{"pod_kill", "network_delay", "network_partition"} {
		if _, err := e.DeriveHandle(capability, "key-"+capability, "ns0", target); err != nil {
			t.Errorf("%s DeriveHandle with namespace-only target: %v", capability, err)
		}
	}
	// Missing request namespace must be rejected.
	for _, capability := range []string{"pod_kill", "network_delay"} {
		if _, err := e.DeriveHandle(capability, "key", "", target); err == nil {
			t.Errorf("%s DeriveHandle should reject empty request namespace", capability)
		}
	}
	// Empty target still rejected (logical ns is a catalog-completeness check).
	for _, capability := range []string{"pod_kill", "network_delay"} {
		if _, err := e.DeriveHandle(capability, "key", "ns0", map[string]any{}); err == nil {
			t.Errorf("%s DeriveHandle should reject empty target", capability)
		}
	}
}
