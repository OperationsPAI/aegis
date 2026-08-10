package chaos

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// Apply must NOT adopt a pre-existing CR that bears the same content-hashed
// name as a prior round's stale artifact. Adopting a non-terminal stale CR
// (selector matched no pods → never AllInjected) wedged the round forever.
// reap-before-apply deletes it and recreates fresh, so the new round's CR
// carries the new render and no leftover stale status.
func TestChaosMeshExecutor_ApplyReapsStaleCR(t *testing.T) {
	scheme, listKinds := newAllChaosSchemeForTest()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	exec := NewChaosMeshExecutor(dyn)

	target := map[string]any{"namespace": "sys", "app": "frontend"}
	handle, err := exec.DeriveHandle("pod_kill", "trace-1:p1", "ns0", target)
	if err != nil {
		t.Fatalf("DeriveHandle: %v", err)
	}
	h, _ := decodeHandle(handle)

	// Seed a stale CR at the derived name: a non-terminal, never-injected CR
	// carrying a sentinel annotation so we can prove the post-Apply CR is a
	// fresh render rather than the adopted stale object.
	stale := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "PodChaos",
		"metadata": map[string]any{
			"name":        h.Name,
			"namespace":   h.Namespace,
			"annotations": map[string]any{"sentinel": "stale-round"},
		},
		"spec": map[string]any{"action": "pod-kill"},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Selected", "status": "False"},
			},
		},
	}}
	if _, err := dyn.Resource(h.GVR).Namespace(h.Namespace).Create(context.Background(), stale, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed stale CR: %v", err)
	}

	if err := exec.Apply(context.Background(), SystemContext{Name: "sys", AppLabelKey: "app"}, "pod_kill", handle, target, map[string]any{"duration_s": float64(60)}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := dyn.Resource(h.GVR).Namespace(h.Namespace).Get(context.Background(), h.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get post-apply CR: %v", err)
	}
	if _, found, _ := unstructured.NestedString(got.Object, "metadata", "annotations", "sentinel"); found {
		t.Fatalf("post-apply CR still carries the stale round's sentinel annotation — stale CR was adopted, not reaped")
	}
	if _, found, _ := unstructured.NestedSlice(got.Object, "status", "conditions"); found {
		t.Fatalf("post-apply CR carries the stale Selected=False status — stale CR was adopted, not reaped")
	}
}
