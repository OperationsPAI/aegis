package chaos

import "testing"

func TestPodChaosExtraRendererRegistry(t *testing.T) {
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	for _, n := range []string{"container_kill", "pod_failure"} {
		if got[n] != CapExperimental {
			t.Errorf("%s must be %q, got %q", n, CapExperimental, got[n])
		}
	}
}

func TestContainerKillRender(t *testing.T) {
	r, err := lookupRenderer("container_kill")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	target := map[string]any{"namespace": "ts", "app": "ts-order", "container": "order"}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	// container missing is a required-field trap (chaos-mesh webhook
	// rejects container-kill without containerNames).
	if err := r.ValidateTarget(map[string]any{"namespace": "ts", "app": "ts-order"}); err == nil {
		t.Error("ValidateTarget must reject missing container for container_kill")
	}
	cr, err := r.RenderCR("x", "ts", target, map[string]any{"duration_s": 30})
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	spec := cr.Object["spec"].(map[string]any)
	if spec["action"] != "container-kill" {
		t.Errorf("action = %v", spec["action"])
	}
	cns, ok := spec["containerNames"].([]any)
	if !ok || len(cns) != 1 || cns[0] != "order" {
		t.Errorf("containerNames = %v", spec["containerNames"])
	}
	if spec["duration"] != "30s" {
		t.Errorf("duration = %v", spec["duration"])
	}
}

func TestPodFailureRender(t *testing.T) {
	r, _ := lookupRenderer("pod_failure")
	target := map[string]any{"namespace": "ts", "app": "ts-order"}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	cr, err := r.RenderCR("x", "ts", target, map[string]any{})
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	spec := cr.Object["spec"].(map[string]any)
	if spec["action"] != "pod-failure" {
		t.Errorf("action = %v", spec["action"])
	}
	if _, present := spec["containerNames"]; present {
		t.Error("pod-failure must not set containerNames")
	}
}
