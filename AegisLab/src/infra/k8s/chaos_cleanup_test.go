package k8s

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// stubLister returns a fixed list of namespaced chaos GVRs and pretends the
// discovery surface is healthy. Lets the test exercise the
// list/strip-finalizers/delete loop without an apiserver.
type stubLister struct {
	gvrs []schema.GroupVersionResource
	err  error
}

func (s *stubLister) NamespacedChaosGVRs(ctx context.Context) ([]schema.GroupVersionResource, error) {
	return s.gvrs, s.err
}

func chaosGVR(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    ChaosMeshAPIGroup,
		Version:  "v1alpha1",
		Resource: resource,
	}
}

func newChaosCR(resource, namespace, name string, finalizers []string) *unstructured.Unstructured {
	// Map dynamic-resource plural to a Kind suitable for the fake registry.
	kindByResource := map[string]string{
		"httpchaos":      "HTTPChaos",
		"networkchaos":   "NetworkChaos",
		"podchaos":       "PodChaos",
		"podhttpchaos":   "PodHttpChaos",
		"jvmchaos":       "JVMChaos",
		"stresschaos":    "StressChaos",
		"timechaos":      "TimeChaos",
		"iochaos":        "IOChaos",
		"podiochaos":     "PodIOChaos",
		"podnetworkchaos": "PodNetworkChaos",
		"dnschaos":       "DNSChaos",
		"blockchaos":     "BlockChaos",
	}
	kind := kindByResource[resource]
	if kind == "" {
		kind = "Generic"
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ChaosMeshAPIGroup,
		Version: "v1alpha1",
		Kind:    kind,
	})
	u.SetNamespace(namespace)
	u.SetName(name)
	if len(finalizers) > 0 {
		u.SetFinalizers(finalizers)
	}
	return u
}

// chaosResourceKinds is the resource→kind mapping the tests need. Chaos-mesh
// plurals like `httpchaos` are already plural, so we cannot rely on the
// fake client's `UnsafeGuessKindToResource` heuristic (which would store
// items under `httpchaoses`).
var chaosResourceKinds = []struct {
	Resource string
	Kind     string
}{
	{"httpchaos", "HTTPChaos"},
	{"networkchaos", "NetworkChaos"},
	{"podchaos", "PodChaos"},
	{"podhttpchaos", "PodHttpChaos"},
	{"jvmchaos", "JVMChaos"},
	{"stresschaos", "StressChaos"},
	{"timechaos", "TimeChaos"},
	{"iochaos", "IOChaos"},
	{"podiochaos", "PodIOChaos"},
	{"podnetworkchaos", "PodNetworkChaos"},
	{"dnschaos", "DNSChaos"},
	{"blockchaos", "BlockChaos"},
}

// newFakeDynamicClient builds a fake dynamic client whose object tracker
// stores `objs` under the *exact* chaos GVRs (e.g. `httpchaos`, not
// `httpchaoses`). Items are inserted via Create against an explicit GVR
// rather than the constructor's seeding path so the tracker doesn't
// auto-pluralise.
func newFakeDynamicClient(t *testing.T, objs ...*unstructured.Unstructured) dynamic.Interface {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := make(map[schema.GroupVersionResource]string, len(chaosResourceKinds))
	for _, r := range chaosResourceKinds {
		gvk := schema.GroupVersionKind{Group: ChaosMeshAPIGroup, Version: "v1alpha1", Kind: r.Kind}
		scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		listGVK := gvk
		listGVK.Kind += "List"
		scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
		listKinds[chaosGVR(r.Resource)] = r.Kind + "List"
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	for _, obj := range objs {
		gvk := obj.GroupVersionKind()
		// Find the matching plural for this kind from our static table.
		var resource string
		for _, r := range chaosResourceKinds {
			if r.Kind == gvk.Kind {
				resource = r.Resource
				break
			}
		}
		if resource == "" {
			t.Fatalf("test fixture has unknown chaos kind %s; add it to chaosResourceKinds", gvk.Kind)
		}
		gvr := chaosGVR(resource)
		if _, err := dyn.Resource(gvr).Namespace(obj.GetNamespace()).Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed fake dynamic client: %v", err)
		}
	}
	return dyn
}

func TestCleanupNamespaceChaosResources_ReapsZombiesAndStripsFinalizers(t *testing.T) {
	ctx := context.Background()

	zombieFinalizers := []string{"chaos-mesh/finalizers"}
	objs := []*unstructured.Unstructured{
		// otel-demo1 zombies (analogous to the byte-cluster evidence).
		newChaosCR("httpchaos", "otel-demo1", "stuck-http-1", zombieFinalizers),
		newChaosCR("httpchaos", "otel-demo1", "stuck-http-2", zombieFinalizers),
		newChaosCR("podhttpchaos", "otel-demo1", "intermediate-1", zombieFinalizers),
		// Different namespace — must NOT be touched by an otel-demo1 cleanup.
		newChaosCR("httpchaos", "ts0", "another-stuck", zombieFinalizers),
	}

	dyn := newFakeDynamicClient(t, objs...)
	lister := &stubLister{gvrs: []schema.GroupVersionResource{
		chaosGVR("httpchaos"),
		chaosGVR("networkchaos"),
		chaosGVR("podhttpchaos"),
	}}

	summary, warnings := cleanupNamespaceChaosResourcesWith(ctx, lister, dyn, "otel-demo1")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	if got := summary["httpchaos"]; got != 2 {
		t.Errorf("httpchaos reap count: got %d, want 2", got)
	}
	if got := summary["podhttpchaos"]; got != 1 {
		t.Errorf("podhttpchaos reap count: got %d, want 1", got)
	}
	// `networkchaos` was discovered but had no instances — by design the
	// summary omits zero-reap entries to keep the production log line short.
	if _, present := summary["networkchaos"]; present {
		t.Errorf("networkchaos should be absent from summary (no zombies), got %v", summary)
	}

	// Verify cross-namespace scoping: ts0's CR survives.
	survivor, err := dyn.Resource(chaosGVR("httpchaos")).Namespace("ts0").Get(ctx, "another-stuck", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ts0 zombie got reaped by otel-demo1 cleanup: %v", err)
	}
	if survivor.GetName() != "another-stuck" {
		t.Fatalf("unexpected survivor: %v", survivor)
	}

	// And verify the otel-demo1 zombies are actually gone.
	gone, err := dyn.Resource(chaosGVR("httpchaos")).Namespace("otel-demo1").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("post-cleanup list otel-demo1 httpchaos: %v", err)
	}
	if len(gone.Items) != 0 {
		t.Errorf("expected 0 surviving httpchaos in otel-demo1, got %d", len(gone.Items))
	}

	// Summarizer should produce a stable, alphabetised line.
	line := SummarizeChaosCleanup(summary)
	want := "2 httpchaos, 1 podhttpchaos"
	if line != want {
		t.Errorf("summary line:\n got  %q\n want %q", line, want)
	}

	// Assert that finalizers were merge-patched to []. Without the patch,
	// real-cluster deletes would hang on the chaos finalizer; the fake
	// client doesn't enforce that, so we instead verify the patch action
	// occurred and was scoped to chaos-mesh.org.
	fake, ok := dyn.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("dyn is not a FakeDynamicClient: %T", dyn)
	}
	patchCount := 0
	for _, act := range fake.Actions() {
		patch, ok := act.(clienttesting.PatchAction)
		if !ok {
			continue
		}
		if patch.GetResource().Group != ChaosMeshAPIGroup {
			t.Errorf("patch scoped to non-chaos group: %v", patch.GetResource())
		}
		if patch.GetNamespace() != "otel-demo1" {
			t.Errorf("patch hit wrong namespace: %s", patch.GetNamespace())
		}
		if got := string(patch.GetPatch()); got != `{"metadata":{"finalizers":[]}}` {
			t.Errorf("unexpected patch body: %s", got)
		}
		patchCount++
	}
	if patchCount != 3 {
		t.Errorf("expected 3 finalizer-strip patches (2 httpchaos + 1 podhttpchaos in otel-demo1), got %d", patchCount)
	}
}

func TestCleanupNamespaceChaosResources_RefusesNonChaosGVR(t *testing.T) {
	ctx := context.Background()
	dyn := newFakeDynamicClient(t)
	// A malicious/buggy lister returning a non-chaos-mesh.org GVR must be
	// rejected with a warning rather than silently patched — the whole
	// purpose of the group filter is to never strip finalizers off
	// unrelated CRDs.
	lister := &stubLister{gvrs: []schema.GroupVersionResource{
		{Group: "apps", Version: "v1", Resource: "deployments"},
	}}

	summary, warnings := cleanupNamespaceChaosResourcesWith(ctx, lister, dyn, "otel-demo1")
	if len(summary) != 0 {
		t.Errorf("expected empty summary, got %v", summary)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected a refusal warning for non-chaos GVR, got none")
	}
}

func TestCleanupNamespaceChaosResources_NoChaosMeshInstalled(t *testing.T) {
	// chaos-mesh isn't installed — discovery returns an empty GVR list. The
	// cleanup must be a no-op (empty summary, no warnings, no panic) so
	// RestartPedestal proceeds normally on clusters without chaos-mesh.
	ctx := context.Background()
	dyn := newFakeDynamicClient(t)
	lister := &stubLister{gvrs: nil}

	summary, warnings := cleanupNamespaceChaosResourcesWith(ctx, lister, dyn, "otel-demo1")
	if len(summary) != 0 {
		t.Errorf("expected empty summary, got %v", summary)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

