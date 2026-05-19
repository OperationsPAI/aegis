package chaos

import "testing"

func TestStressRendererRegistry(t *testing.T) {
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	for _, n := range []string{"cpu_stress", "memory_stress"} {
		if got[n] != CapExperimental {
			t.Errorf("%s must be %q, got %q", n, CapExperimental, got[n])
		}
	}
}

func TestStressRenderers(t *testing.T) {
	target := map[string]any{"namespace": "ts", "app": "ts-order", "container": "order"}
	cases := []struct {
		capability string
		params     map[string]any
		subKey     string
		missing    string
	}{
		{"cpu_stress", map[string]any{"load_pct": 80, "workers": 2, "duration_s": 30}, "cpu", "load_pct"},
		{"memory_stress", map[string]any{"size_mib": 256, "workers": 2, "duration_s": 30}, "memory", "size_mib"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			r, err := lookupRenderer(tc.capability)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if err := r.ValidateTarget(target); err != nil {
				t.Fatalf("ValidateTarget: %v", err)
			}
			if err := r.ValidateParams(tc.params); err != nil {
				t.Fatalf("ValidateParams: %v", err)
			}
			// Missing required param must error.
			bad := map[string]any{}
			for k, v := range tc.params {
				if k != tc.missing {
					bad[k] = v
				}
			}
			if err := r.ValidateParams(bad); err == nil {
				t.Errorf("ValidateParams should reject missing %s", tc.missing)
			}

			cr, err := r.RenderCR(SystemContext{}, "x", "ts", target, tc.params)
			if err != nil {
				t.Fatalf("RenderCR: %v", err)
			}
			spec := cr.Object["spec"].(map[string]any)
			stressors, ok := spec["stressors"].(map[string]any)
			if !ok {
				t.Fatalf("spec.stressors missing")
			}
			sub, ok := stressors[tc.subKey].(map[string]any)
			if !ok {
				t.Fatalf("spec.stressors.%s missing", tc.subKey)
			}
			if w, _ := sub["workers"].(int64); w != 2 {
				t.Errorf("workers = %v, want 2", sub["workers"])
			}
			cns, _ := spec["containerNames"].([]any)
			if len(cns) != 1 || cns[0] != "order" {
				t.Errorf("containerNames = %v", spec["containerNames"])
			}
			if cr.Object["kind"] != "StressChaos" {
				t.Errorf("kind = %v", cr.Object["kind"])
			}
		})
	}
}

func TestMemoryStressSizeFormat(t *testing.T) {
	r, _ := lookupRenderer("memory_stress")
	target := map[string]any{"namespace": "ts", "app": "ts-order", "container": "order"}
	cr, err := r.RenderCR(SystemContext{}, "x", "ts", target, map[string]any{"size_mib": 512})
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	spec := cr.Object["spec"].(map[string]any)
	mem := spec["stressors"].(map[string]any)["memory"].(map[string]any)
	if mem["size"] != "512MiB" {
		t.Errorf("size = %v, want 512MiB", mem["size"])
	}
}
