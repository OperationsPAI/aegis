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

// Core regression for the #460/#470 vs #166 incompatibility: a Point imported
// with the catalog's logical namespace must produce the SAME id as an inject
// resolving in a concrete per-instance allocator namespace. namespace is a
// runtime binding and must NOT participate in identity.
func TestServiceBoundPointID_NamespaceExcludedFromIdentity(t *testing.T) {
	imported, err := ServiceBoundPointID(PointIdentity{
		System: "sn", Service: "post-storage-service", Instance: "seed",
		ChartVersion: "seed-genesis", Capability: "pod_failure",
		Target: map[string]any{"namespace": "sn", "app": "post-storage-service"},
	})
	if err != nil {
		t.Fatalf("imported: %v", err)
	}
	injected, err := ServiceBoundPointID(PointIdentity{
		System: "sn", Service: "post-storage-service", Instance: "seed",
		ChartVersion: "seed-genesis", Capability: "pod_failure",
		Target: map[string]any{"namespace": "sn1", "app": "post-storage-service"},
	})
	if err != nil {
		t.Fatalf("injected: %v", err)
	}
	if imported != injected {
		t.Fatalf("namespace must not affect id: imported %q vs injected %q", imported, injected)
	}

	// non-namespace target keys must still drive identity.
	other, _ := ServiceBoundPointID(PointIdentity{
		System: "sn", Service: "post-storage-service", Instance: "seed",
		ChartVersion: "seed-genesis", Capability: "pod_failure",
		Target: map[string]any{"namespace": "sn1", "app": "user-service"},
	})
	if other == injected {
		t.Fatalf("non-namespace target change must yield a different id")
	}
}

func TestCrossServicePointID_NamespaceExcludedFromIdentity(t *testing.T) {
	imported, _ := CrossServicePointID(CrossServicePointIdentity{
		System: "sn", Capability: "network_partition",
		Target: map[string]any{"namespace": "sn", "source_app": "frontend", "target_service": "cart"},
	})
	injected, _ := CrossServicePointID(CrossServicePointIdentity{
		System: "sn", Capability: "network_partition",
		Target: map[string]any{"namespace": "sn3", "source_app": "frontend", "target_service": "cart"},
	})
	if imported != injected {
		t.Fatalf("namespace must not affect cross-service id: %q vs %q", imported, injected)
	}
}

// canonicalTargetJSON must not mutate the caller's map (import.go stores the
// same map it hashes).
func TestCanonicalTargetJSON_DoesNotMutateCaller(t *testing.T) {
	target := map[string]any{"namespace": "sn1", "app": "frontend"}
	if _, err := canonicalTargetJSON(target); err != nil {
		t.Fatalf("canonicalTargetJSON: %v", err)
	}
	if _, ok := target["namespace"]; !ok {
		t.Fatalf("caller's target.namespace was mutated away")
	}
}
