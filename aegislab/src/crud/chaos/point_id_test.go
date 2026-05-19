package chaos

import "testing"

func TestServiceBoundPointID_DeterministicAcrossKeyOrder(t *testing.T) {
	a, err := ServiceBoundPointID(PointIdentity{
		System: "ts", Service: "frontend", Instance: "default",
		ChartVersion: "v3.2.0", Capability: "http_latency",
		Target: map[string]any{
			"endpoint": "/api/login",
			"method":   "POST",
			"nested":   map[string]any{"a": 1, "b": "x"},
		},
	})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	// Same logical target, keys inserted in different order at every level.
	b, err := ServiceBoundPointID(PointIdentity{
		System: "ts", Service: "frontend", Instance: "default",
		ChartVersion: "v3.2.0", Capability: "http_latency",
		Target: map[string]any{
			"method":   "POST",
			"nested":   map[string]any{"b": "x", "a": 1},
			"endpoint": "/api/login",
		},
	})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a != b {
		t.Fatalf("expected canonical JSON to make ids equal: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("expected 16-char id, got %d", len(a))
	}
}

func TestServiceBoundPointID_InstanceAffectsID(t *testing.T) {
	base := PointIdentity{
		System: "hs", Service: "frontend", Instance: "hs0",
		ChartVersion: "v1.0.0", Capability: "pod_kill",
		Target: map[string]any{},
	}
	a, _ := ServiceBoundPointID(base)
	base.Instance = "hs1"
	b, _ := ServiceBoundPointID(base)
	if a == b {
		t.Fatalf("instance must participate in the id; got identical %q", a)
	}
}

func TestServiceBoundPointID_TargetShapeChangeRegeneratesID(t *testing.T) {
	base := PointIdentity{
		System: "ts", Service: "frontend", Instance: "default",
		ChartVersion: "v3.2.0", Capability: "http_latency",
		Target: map[string]any{"endpoint": "/api/login", "method": "POST"},
	}
	a, _ := ServiceBoundPointID(base)
	base.Target = map[string]any{"endpoint": "/api/login", "method": "GET"}
	b, _ := ServiceBoundPointID(base)
	if a == b {
		t.Fatalf("target shape change must yield different id")
	}
}

func TestCrossServicePointID_OmitsService(t *testing.T) {
	a, _ := CrossServicePointID(CrossServicePointIdentity{
		System: "ts", Capability: "network_partition",
		Target: map[string]any{"from": "frontend", "to": "cart"},
	})
	// Same input via the service-bound recipe with sentinel values
	// would yield a different hash — this just asserts cross-service
	// is well-formed.
	if len(a) != 16 {
		t.Fatalf("expected 16-char id, got %d", len(a))
	}
}
