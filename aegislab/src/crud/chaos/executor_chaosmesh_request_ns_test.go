package chaos

import (
	"encoding/json"
	"testing"
)

// TestDeriveHandle_RequestNamespaceWins is the Step 5b R5 regression guard
// for Bug 2: the executor must use the request namespace (concrete cluster
// ns, e.g. ns0) for handle/CR placement, NOT the catalog target.namespace
// (logical system name, e.g. sys). Otherwise Apply tries to create the CR
// in a ns that doesn't exist.
func TestDeriveHandle_RequestNamespaceWins(t *testing.T) {
	e := &ChaosMeshExecutor{}
	target := map[string]any{"namespace": "sys", "app": "frontend"}
	handle, err := e.DeriveHandle("pod_kill", "idem-key-1", "ns0", target)
	if err != nil {
		t.Fatalf("DeriveHandle: %v", err)
	}
	var h ChaosMeshHandle
	if err := json.Unmarshal([]byte(handle), &h); err != nil {
		t.Fatalf("decode handle: %v", err)
	}
	if h.Namespace != "ns0" {
		t.Fatalf("handle namespace: got %q, want %q (request ns must win over target.namespace)", h.Namespace, "ns0")
	}
	if h.Name == "" {
		t.Fatal("handle name is empty")
	}

	// Empty request ns must be rejected even if target.namespace is set —
	// the catalog ns is informational, not a fallback.
	if _, err := e.DeriveHandle("pod_kill", "idem-key-2", "", target); err == nil {
		t.Fatal("DeriveHandle should reject empty request namespace")
	}
}

// TestRenderCR_RequestNamespaceFlowsToCR confirms that when the executor
// calls RenderCR with the request namespace, the produced PodChaos CR
// lives in that ns and selects pods only in that ns, with selector labels
// derived from target.app (NOT target.namespace).
func TestRenderCR_RequestNamespaceFlowsToCR(t *testing.T) {
	r := podKillRenderer{}
	target := map[string]any{"namespace": "sys", "app": "frontend"}
	cr, err := r.RenderCR(SystemContext{Name: "sys"}, "pod-kill-x", "ns0", target, map[string]any{"duration_s": 30})
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}

	if got := cr.GetNamespace(); got != "ns0" {
		t.Errorf("metadata.namespace: got %q, want %q", got, "ns0")
	}

	spec, _ := cr.Object["spec"].(map[string]any)
	sel, _ := spec["selector"].(map[string]any)
	nsList, _ := sel["namespaces"].([]any)
	if len(nsList) != 1 || nsList[0] != "ns0" {
		t.Errorf("spec.selector.namespaces: got %v, want [ns0]", nsList)
	}
	labels, _ := sel["labelSelectors"].(map[string]any)
	if _, leaked := labels["namespace"]; leaked {
		t.Errorf("selector labelSelectors must not contain 'namespace' key: %v", labels)
	}
	if got, _ := labels["app"].(string); got != "frontend" {
		t.Errorf("selector labelSelectors.app: got %q, want %q", got, "frontend")
	}
}
