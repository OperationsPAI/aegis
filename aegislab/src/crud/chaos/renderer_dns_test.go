package chaos

import "testing"

func TestDNSRenderers(t *testing.T) {
	cases := []struct {
		capability string
		action     string
	}{
		{"dns_error", "error"},
		{"dns_random", "random"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			r, err := lookupRenderer(tc.capability)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			target := map[string]any{
				"namespace":       "ts",
				"app":             "ts-order",
				"domain_patterns": []any{"*.example.com", "api.foo"},
			}
			if err := r.ValidateTarget(target); err != nil {
				t.Fatalf("ValidateTarget: %v", err)
			}
			// Empty patterns must be rejected — chaos-mesh's omitempty
			// would otherwise match every host (blast-radius trap).
			bad := map[string]any{"namespace": "ts", "app": "ts-order", "domain_patterns": []any{}}
			if err := r.ValidateTarget(bad); err == nil {
				t.Error("ValidateTarget must reject empty domain_patterns")
			}
			cr, err := r.RenderCR(SystemContext{}, "x", "ts", target, map[string]any{"duration_s": 30})
			if err != nil {
				t.Fatalf("RenderCR: %v", err)
			}
			spec := cr.Object["spec"].(map[string]any)
			if cr.Object["kind"] != "DNSChaos" {
				t.Errorf("kind = %v", cr.Object["kind"])
			}
			if spec["action"] != tc.action {
				t.Errorf("action = %v, want %s", spec["action"], tc.action)
			}
			pats, ok := spec["patterns"].([]any)
			if !ok || len(pats) != 2 {
				t.Errorf("patterns = %v", spec["patterns"])
			}
		})
	}
}

func TestDNSRegistry(t *testing.T) {
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	for _, n := range []string{"dns_error", "dns_random"} {
		if got[n] != CapExperimental {
			t.Errorf("%s must be %q, got %q", n, CapExperimental, got[n])
		}
	}
}
