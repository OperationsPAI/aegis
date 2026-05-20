package cmd

import (
	"reflect"
	"testing"

	chaoscrud "aegis/crud/chaos"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
)

// TestChaosTypeTranslation pins the guided→capability lookup to the 32
// entries lane A registered in capgen/output/capabilities.json. The table is
// hand-maintained; adding a 33rd capability without updating both files is
// exactly the bug this test catches.
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
	if len(chaoscrud.ChaosTypeToCapability) != len(expected) {
		t.Fatalf("table size drift: got %d entries, expected %d (capabilities.json has 32)",
			len(chaoscrud.ChaosTypeToCapability), len(expected))
	}
	for in, want := range expected {
		got, ok := chaoscrud.ChaosTypeToCapability[in]
		if !ok {
			t.Errorf("missing entry for chaos_type=%s", in)
			continue
		}
		if got != want {
			t.Errorf("chaos_type=%s: got capability=%s, want %s", in, got, want)
		}
	}
}

// TestGuidedToChaosPointID_OtelDemoCart pins the derived Point ID to the
// literal hash the chaos service recorded when it imported
// manifests/aegis-chaos/otel-demo/cart.yaml for pod_kill. Drifts here mean
// EITHER the seed catalog moved (re-import flagged a real change) OR the
// YAML→target derivation broke — both warrant human review, not a silent
// auto-pass. Recomputing via chaoscrud here would be tautological.
const expectCartPodKillPointID = "86db3bb27a46fedf"

func TestGuidedToChaosPointID_OtelDemoCart(t *testing.T) {
	cfg := guidedcli.GuidedConfig{
		System:    "otel-demo",
		Namespace: "otel-demo",
		App:       "cart",
		ChaosType: "PodKill",
	}
	pid, cap, target, err := chaoscrud.GuidedChaosPointID(cfg, "seed", "seed-genesis")
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
	if pid != expectCartPodKillPointID {
		t.Fatalf("point_id drift: got %q want %q (seed catalog out of sync OR target derivation broke)", pid, expectCartPodKillPointID)
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
	pid, cap, target, err := chaoscrud.GuidedChaosPointID(cfg, "seed", "seed-genesis")
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

// TestGuidedToChaosTarget_LogicalNsWhenPoolAllocated is the Step 5b R5 regression
// guard for the R4b cluster validation gap: when the pool allocates a concrete
// namespace (e.g. otel-demo0) that differs from the system name (e.g. otel-demo),
// the chaos catalog still keys its Point on the logical system name, so the
// derived target.namespace MUST be the system name — otherwise locally-derived
// point_id won't match the catalog row and inject returns 404.
func TestGuidedToChaosTarget_LogicalNsWhenPoolAllocated(t *testing.T) {
	cfg := guidedcli.GuidedConfig{
		System:    "otel-demo",
		Namespace: "otel-demo0", // pool-allocated; differs from system
		App:       "cart",
		ChaosType: "PodKill",
	}
	pid, cap, target, err := chaoscrud.GuidedChaosPointID(cfg, "seed", "seed-genesis")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if cap != "pod_kill" {
		t.Errorf("capability: got %s, want pod_kill", cap)
	}
	if got, _ := target["namespace"].(string); got != "otel-demo" {
		t.Errorf("target.namespace: got %q, want logical system %q", got, "otel-demo")
	}
	if pid != expectCartPodKillPointID {
		t.Fatalf("point_id with pool-allocated ns must match catalog row keyed on logical ns: got %q want %q",
			pid, expectCartPodKillPointID)
	}
}
