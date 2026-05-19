package chaos

import "testing"

func TestTimeSkewRender(t *testing.T) {
	r, err := lookupRenderer("time_skew")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	target := map[string]any{"namespace": "ts", "app": "ts-order", "container": "order"}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	// offset_s is required (TimeChaosSpec.TimeOffset is non-omitempty).
	if err := r.ValidateParams(map[string]any{"duration_s": 30}); err == nil {
		t.Error("ValidateParams must reject missing offset_s")
	}
	params := map[string]any{"offset_s": -60, "duration_s": 30}
	if err := r.ValidateParams(params); err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
	cr, err := r.RenderCR("x", "ts", target, params)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	spec := cr.Object["spec"].(map[string]any)
	if cr.Object["kind"] != "TimeChaos" {
		t.Errorf("kind = %v", cr.Object["kind"])
	}
	if spec["timeOffset"] != "-60s" {
		t.Errorf("timeOffset = %v, want -60s", spec["timeOffset"])
	}
	cns, _ := spec["containerNames"].([]any)
	if len(cns) != 1 || cns[0] != "order" {
		t.Errorf("containerNames = %v", spec["containerNames"])
	}
}

func TestTimeSkewRegistry(t *testing.T) {
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	if got["time_skew"] != CapExperimental {
		t.Errorf("time_skew must be %q, got %q", CapExperimental, got["time_skew"])
	}
}
