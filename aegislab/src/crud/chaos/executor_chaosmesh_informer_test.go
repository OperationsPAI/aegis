package chaos

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// newAllChaosSchemeForTest registers every chaos-mesh GVR the renderer
// registry produces. The informer factory watches all of them at boot;
// the fake dynamic client panics if its scheme doesn't know how to LIST a
// watched resource. Mirror the registry here so adding a new renderer
// can't silently break this test.
func newAllChaosSchemeForTest() (*runtime.Scheme, map[schema.GroupVersionResource]string) {
	scheme := runtime.NewScheme()
	kindByResource := map[string]string{
		"podchaos":     "PodChaos",
		"networkchaos": "NetworkChaos",
		"stresschaos":  "StressChaos",
		"timechaos":    "TimeChaos",
		"dnschaos":     "DNSChaos",
		"jvmchaos":     "JVMChaos",
		"httpchaos":    "HTTPChaos",
	}
	listKinds := make(map[schema.GroupVersionResource]string, len(kindByResource))
	for resource, kind := range kindByResource {
		gvk := schema.GroupVersionKind{Group: ChaosMeshGroup, Version: ChaosMeshVersion, Kind: kind}
		scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		listGVK := gvk
		listGVK.Kind += "List"
		scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
		gvr := schema.GroupVersionResource{Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: resource}
		listKinds[gvr] = kind + "List"
	}
	return scheme, listKinds
}

// TestChaosMeshExecutor_StatusServedFromInformerCache verifies that once
// WatchStatus has synced the informer cache, Status reads the CR from the
// cache and does NOT call the dynamic client's GET. This is the whole
// point of the informer wiring — it caps apiserver QPS at one watch per
// GVR regardless of how many rows the reconciler scans.
func TestChaosMeshExecutor_StatusServedFromInformerCache(t *testing.T) {
	scheme, listKinds := newAllChaosSchemeForTest()
	podChaosGVK := schema.GroupVersionKind{Group: ChaosMeshGroup, Version: ChaosMeshVersion, Kind: "PodChaos"}

	cr := &unstructured.Unstructured{}
	cr.SetGroupVersionKind(podChaosGVK)
	cr.SetNamespace("ns0")
	cr.SetName("podkill-xyz")
	_ = unstructured.SetNestedSlice(cr.Object, []any{
		map[string]any{"type": "AllRecovered", "status": "True"},
	}, "status", "conditions")

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	if _, err := dyn.Resource(podChaosGVR).Namespace("ns0").Create(context.Background(), cr, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed CR: %v", err)
	}

	var getCalls int64
	dyn.PrependReactor("get", "podchaos", func(action clienttesting.Action) (bool, runtime.Object, error) {
		atomic.AddInt64(&getCalls, 1)
		return false, nil, nil // let the default tracker handle it
	})

	exec := NewChaosMeshExecutor(dyn)

	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	watchDone := make(chan struct{})
	go func() {
		_ = exec.WatchStatus(watchCtx)
		close(watchDone)
	}()

	// Poll until the informer cache has seen our seeded CR. WaitForCacheSync
	// returns once the initial LIST completes; ByNamespace.Get then succeeds.
	handle, err := exec.DeriveHandle("pod_kill", "k", "ns0", map[string]any{"namespace": "sys", "app": "x"})
	if err != nil {
		t.Fatalf("DeriveHandle: %v", err)
	}
	h, _ := decodeHandle(handle)
	h.Name = "podkill-xyz"
	rehandle, _ := encodeHandle(h)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if u, ok := exec.lookupFromInformer(h); ok && u != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := exec.lookupFromInformer(h); !ok {
		t.Fatal("informer cache never observed the seeded CR")
	}

	// Reset the get counter — the dynamicfake's initial LIST does not
	// flow through the "get" reactor, but be defensive.
	atomic.StoreInt64(&getCalls, 0)

	state, _, err := exec.Status(context.Background(), rehandle)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state != ExecStateSucceeded {
		t.Fatalf("state: got %v, want %v (AllRecovered)", state, ExecStateSucceeded)
	}
	if n := atomic.LoadInt64(&getCalls); n != 0 {
		t.Fatalf("Status issued %d dynamic GET calls; expected 0 (informer cache hit)", n)
	}
}

// TestChaosMeshExecutor_StatusFallsBackOnCacheMiss exercises the
// just-after-Apply path: the informer is running but hasn't observed a
// freshly-created CR yet (we simulate this by NOT seeding the CR before
// starting the informer, then creating it AFTER cache sync but querying
// immediately so the cache may still be empty). The fallback dynamic.Get
// must succeed regardless.
func TestChaosMeshExecutor_StatusFallsBackOnCacheMiss(t *testing.T) {
	scheme, listKinds := newAllChaosSchemeForTest()
	gvk := schema.GroupVersionKind{Group: ChaosMeshGroup, Version: ChaosMeshVersion, Kind: "PodChaos"}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	exec := NewChaosMeshExecutor(dyn)

	// Start informer with no objects.
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	go func() { _ = exec.WatchStatus(watchCtx) }()

	// Wait briefly so the informer is registered.
	time.Sleep(50 * time.Millisecond)

	// Apply a CR.
	cr := &unstructured.Unstructured{}
	cr.SetGroupVersionKind(gvk)
	cr.SetNamespace("ns0")
	cr.SetName("late-cr")
	_ = unstructured.SetNestedSlice(cr.Object, []any{
		map[string]any{"type": "AllInjected", "status": "True"},
	}, "status", "conditions")
	if _, err := dyn.Resource(podChaosGVR).Namespace("ns0").Create(context.Background(), cr, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Build the handle the executor expects.
	h := ChaosMeshHandle{GVR: podChaosGVR, Namespace: "ns0", Name: "late-cr"}
	handle, _ := encodeHandle(h)

	// Immediately query Status. The informer cache may not have observed
	// the CR yet — Status must fall back to dynamic.Get and still return
	// the right state (Running, since AllInjected=True without AllRecovered
	// for non-destructive PodChaos defaults to Running, but our CR has no
	// spec.action so isDestructivePodChaos returns false → Running).
	state, _, err := exec.Status(context.Background(), handle)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state != ExecStateRunning && state != ExecStateSucceeded {
		// Succeeded acceptable if the cache happened to observe in time
		// and the renderer treated it as destructive (it doesn't here);
		// Running is the expected non-destructive AllInjected state.
		t.Fatalf("state: got %v, want Running (AllInjected, non-destructive)", state)
	}
}

// Compile-time guard: the type-assertion in module.go expects this
// signature. If WatchStatus's signature drifts, the fx wiring will
// silently stop starting the informer — keep the contract pinned.
var _ interface {
	WatchStatus(context.Context) error
} = (*ChaosMeshExecutor)(nil)

