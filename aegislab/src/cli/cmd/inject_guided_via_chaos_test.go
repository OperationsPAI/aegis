package cmd

import (
	"reflect"
	"testing"

	chaoscrud "aegis/crud/chaos"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
)

// TestChaosTypeTranslation pins the guided→capability lookup to the 32
// entries lane A registered in capgen/output/capabilities.json. The table is
// hand-maintained (see file-level comment in inject_guided_via_chaos.go);
// adding a 33rd capability without updating both files is exactly the bug
// this test catches.
func TestChaosTypeTranslation(t *testing.T) {
	expected := map[string]string{
		"ContainerKill":            "container_kill",
		"CPUStress":                "cpu_stress",
		"DNSError":                 "dns_error",
		"DNSRandom":                "dns_random",
		"HTTPRequestAbort":         "http_request_abort",
		"HTTPRequestDelay":         "http_request_delay",
		"HTTPRequestReplaceMethod": "http_request_replace_method",
		"HTTPRequestReplacePath":   "http_request_replace_path",
		"HTTPResponseAbort":        "http_response_abort",
		"HTTPResponseDelay":        "http_response_delay",
		"HTTPResponsePatchBody":    "http_response_patch_body",
		"HTTPResponseReplaceBody":  "http_response_replace_body",
		"HTTPResponseReplaceCode":  "http_response_replace_code",
		"JVMCPUStress":             "jvm_cpu_stress",
		"JVMGarbageCollector":      "jvm_gc",
		"JVMMemoryStress":          "jvm_memory_stress",
		"JVMException":             "jvm_method_exception",
		"JVMLatency":               "jvm_method_latency",
		"JVMReturn":                "jvm_method_return",
		"JVMMySQLException":        "jvm_mysql_exception",
		"JVMMySQLLatency":          "jvm_mysql_latency",
		"JVMRuntimeMutator":        "jvm_runtime_mutator",
		"MemoryStress":             "memory_stress",
		"NetworkBandwidth":         "network_bandwidth",
		"NetworkCorrupt":           "network_corrupt",
		"NetworkDelay":             "network_delay",
		"NetworkDuplicate":         "network_duplicate",
		"NetworkLoss":              "network_loss",
		"NetworkPartition":         "network_partition",
		"PodFailure":               "pod_failure",
		"PodKill":                  "pod_kill",
		"TimeSkew":                 "time_skew",
	}
	if len(chaosTypeToCapability) != len(expected) {
		t.Fatalf("table size drift: got %d entries, expected %d (capabilities.json has 32)",
			len(chaosTypeToCapability), len(expected))
	}
	for in, want := range expected {
		got, ok := chaosTypeToCapability[in]
		if !ok {
			t.Errorf("missing entry for chaos_type=%s", in)
			continue
		}
		if got != want {
			t.Errorf("chaos_type=%s: got capability=%s, want %s", in, got, want)
		}
	}
}

// TestGuidedToChaosPointID_OtelDemoCart asserts that a guided YAML config
// matching the seed catalog manifest (manifests/aegis-chaos/otel-demo/cart.yaml)
// for `pod_kill` derives the same Point ID the chaos service computed at
// import time. This is non-tautological: it tests the YAML→target derivation
// in guidedToChaosTarget, then asserts the derived target hashes to the
// expected service-bound id.
func TestGuidedToChaosPointID_OtelDemoCart(t *testing.T) {
	cfg := guidedcli.GuidedConfig{
		System:    "otel-demo",
		Namespace: "otel-demo",
		App:       "cart",
		ChaosType: "PodKill",
	}
	pid, cap, target, err := guidedChaosPointID(cfg, "seed", "seed-genesis")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if cap != "pod_kill" {
		t.Errorf("capability: got %s, want pod_kill", cap)
	}
	wantTarget := map[string]any{"namespace": "otel-demo", "app": "cart"}
	if !reflect.DeepEqual(target, wantTarget) {
		t.Errorf("target: got %v, want %v", target, wantTarget)
	}
	// Independently recompute via the crud package to confirm the wiring
	// (system, service, instance, chart_version, capability, target) is
	// passed through unchanged.
	wantID, err := chaoscrud.ServiceBoundPointID(chaoscrud.PointIdentity{
		System:       "otel-demo",
		Service:      "cart",
		Instance:     "seed",
		ChartVersion: "seed-genesis",
		Capability:   "pod_kill",
		Target:       wantTarget,
	})
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if pid != wantID {
		t.Errorf("point_id: got %s, want %s", pid, wantID)
	}
}

// TestGuidedToChaosPointID_CrossService asserts NetworkPartition routes to
// the cross-service ID variant (point.service_id IS NULL).
func TestGuidedToChaosPointID_CrossService(t *testing.T) {
	cfg := guidedcli.GuidedConfig{
		System:        "otel-demo",
		Namespace:     "otel-demo",
		App:           "cart",
		TargetService: "checkout",
		ChaosType:     "NetworkPartition",
	}
	pid, cap, target, err := guidedChaosPointID(cfg, "seed", "seed-genesis")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if cap != "network_partition" {
		t.Errorf("capability: got %s, want network_partition", cap)
	}
	wantID, err := chaoscrud.CrossServicePointID(chaoscrud.CrossServicePointIdentity{
		System:     "otel-demo",
		Capability: "network_partition",
		Target:     target,
	})
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if pid != wantID {
		t.Errorf("point_id: got %s, want %s (cross-service variant)", pid, wantID)
	}
	// Same input via ServiceBoundPointID would produce a DIFFERENT id because
	// service/instance/chart_version participate in that hash. Confirm the
	// router actually picked the cross-service branch.
	serviceID, _ := chaoscrud.ServiceBoundPointID(chaoscrud.PointIdentity{
		System:       "otel-demo",
		Service:      "cart",
		Instance:     "seed",
		ChartVersion: "seed-genesis",
		Capability:   "network_partition",
		Target:       target,
	})
	if pid == serviceID {
		t.Errorf("expected cross-service id, but result matched service-bound id %s", pid)
	}
}
