package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

const (
	ChaosMeshGroup        = "chaos-mesh.org"
	ChaosMeshVersion      = "v1alpha1"
	PodChaosResource      = "podchaos"
	NetworkChaosResource  = "networkchaos"
	StressChaosResource   = "stresschaos"
	TimeChaosResource     = "timechaos"
	DNSChaosResource      = "dnschaos"
	ExecutorNameChaosMesh = "chaos-mesh"
)

var podChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: PodChaosResource,
}

var networkChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: NetworkChaosResource,
}

var stressChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: StressChaosResource,
}

var timeChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: TimeChaosResource,
}

var dnsChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: DNSChaosResource,
}

func ChaosMeshGroupVersionResourceForStressChaos() schema.GroupVersionResource {
	return stressChaosGVR
}

func ChaosMeshGroupVersionResourceForTimeChaos() schema.GroupVersionResource {
	return timeChaosGVR
}

func ChaosMeshGroupVersionResourceForDNSChaos() schema.GroupVersionResource {
	return dnsChaosGVR
}

func ChaosMeshGroupVersionResourceForPodChaos() schema.GroupVersionResource {
	return podChaosGVR
}

func ChaosMeshGroupVersionResourceForNetworkChaos() schema.GroupVersionResource {
	return networkChaosGVR
}

// chaosMeshInformerResync controls how often the shared informer relists
// each watched GVR. 5m matches client-go's typical defaults and is long
// enough that the relist storm at boot doesn't dominate apiserver load,
// short enough that any missed-event drift heals within a single
// reconciler tick window.
const chaosMeshInformerResync = 5 * time.Minute

type ChaosMeshExecutor struct {
	Dyn dynamic.Interface

	informerMu sync.RWMutex
	// factory + listers are populated by WatchStatus. When listers is empty
	// (test / pre-boot path), Status falls back to a per-call dynamic.Get.
	factory dynamicinformer.DynamicSharedInformerFactory
	listers map[schema.GroupVersionResource]cache.GenericLister
}

var _ Executor = (*ChaosMeshExecutor)(nil)

func NewChaosMeshExecutor(dyn dynamic.Interface) *ChaosMeshExecutor {
	return &ChaosMeshExecutor{Dyn: dyn}
}

func (e *ChaosMeshExecutor) Name() string { return ExecutorNameChaosMesh }

func (e *ChaosMeshExecutor) SupportedCapabilities() []CapabilitySupport {
	return registeredCapabilities()
}

type ChaosMeshHandle struct {
	GVR       schema.GroupVersionResource `json:"gvr"`
	Namespace string                      `json:"namespace"`
	Name      string                      `json:"name"`
}

func encodeHandle(h ChaosMeshHandle) (string, error) {
	b, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeHandle(s string) (ChaosMeshHandle, error) {
	var h ChaosMeshHandle
	if err := json.Unmarshal([]byte(s), &h); err != nil {
		return h, fmt.Errorf("chaos: decode handle: %w", err)
	}
	if h.Name == "" || h.Namespace == "" || h.GVR.Resource == "" {
		return h, fmt.Errorf("chaos: handle missing required fields: %+v", h)
	}
	return h, nil
}

func (e *ChaosMeshExecutor) DeriveHandle(
	capability, idempotencyKey, requestNamespace string, target map[string]any,
) (string, error) {
	r, err := lookupRenderer(capability)
	if err != nil {
		return "", err
	}
	if requestNamespace == "" {
		return "", fmt.Errorf("chaos-mesh %s: request namespace is required", capability)
	}
	if err := r.ValidateForHandle(target); err != nil {
		return "", err
	}
	name, err := DeriveChaosMeshCRName(r.HandlePrefix(), idempotencyKey)
	if err != nil {
		return "", err
	}
	return encodeHandle(ChaosMeshHandle{GVR: r.GVR(), Namespace: requestNamespace, Name: name})
}

func (e *ChaosMeshExecutor) Apply(
	ctx context.Context,
	sysCtx SystemContext,
	capability, handle string,
	target, params map[string]any,
) error {
	r, err := lookupRenderer(capability)
	if err != nil {
		return err
	}
	if err := r.ValidateTarget(target); err != nil {
		return err
	}
	if err := r.ValidateParams(params); err != nil {
		return err
	}
	h, err := decodeHandle(handle)
	if err != nil {
		return err
	}
	cr, err := r.RenderCR(sysCtx, h.Name, h.Namespace, target, params)
	if err != nil {
		return fmt.Errorf("chaos-mesh %s: render CR: %w", capability, err)
	}
	_, err = e.Dyn.Resource(h.GVR).Namespace(h.Namespace).Create(ctx, cr, metav1.CreateOptions{})
	switch {
	case err == nil:
		return nil
	case apierrors.IsAlreadyExists(err):
		return nil
	default:
		return fmt.Errorf("chaos-mesh %s: create CR: %w", capability, err)
	}
}

func (e *ChaosMeshExecutor) Status(ctx context.Context, handle string) (ExecState, map[string]any, error) {
	h, err := decodeHandle(handle)
	if err != nil {
		return ExecStateFailed, nil, err
	}

	if got, ok := e.lookupFromInformer(h); ok {
		return interpretChaosMeshStatus(got), readStatusDiagnostics(got), nil
	}

	// Cache miss (informer not started, GVR not watched, or just-after-Apply
	// before the watch has observed the new CR). One dynamic.Get keeps the
	// pre-informer semantics — including the cr_absent → Orphaned branch
	// that the reconciler depends on.
	got, err := e.Dyn.Resource(h.GVR).Namespace(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ExecStateOrphaned, map[string]any{"reason": "cr_absent"}, nil
		}
		return ExecStateFailed, nil, fmt.Errorf("chaos-mesh status: %w", err)
	}
	return interpretChaosMeshStatus(got), readStatusDiagnostics(got), nil
}

// lookupFromInformer returns (CR, true) if the shared informer for h.GVR
// is started and its cache currently holds the named CR. Any other
// outcome — informer not started, GVR not watched, cache miss, type
// mismatch — returns (_, false) so Status falls back to dynamic.Get.
// Treating cache-miss as "fall back" (rather than as cr_absent) is
// deliberate: informers are eventually consistent, and a just-applied CR
// can legitimately be absent from the cache for a tick.
func (e *ChaosMeshExecutor) lookupFromInformer(h ChaosMeshHandle) (*unstructured.Unstructured, bool) {
	e.informerMu.RLock()
	lister, ok := e.listers[h.GVR]
	e.informerMu.RUnlock()
	if !ok || lister == nil {
		return nil, false
	}
	obj, err := lister.ByNamespace(h.Namespace).Get(h.Name)
	if err != nil {
		return nil, false
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, false
	}
	return u, true
}

// WatchStatus starts a chaos-mesh CR informer for every GVR currently
// known to the renderer registry and blocks until ctx is done. Status
// reads from the informer cache instead of issuing one GET per row per
// tick once this returns from WaitForCacheSync.
//
// The watch is single-flight: a second concurrent WatchStatus call
// returns immediately. client-go's shared informer factory handles
// apiserver disconnects and resyncs internally, so no manual restart
// logic is needed.
func (e *ChaosMeshExecutor) WatchStatus(ctx context.Context) error {
	e.informerMu.Lock()
	if e.factory != nil {
		e.informerMu.Unlock()
		<-ctx.Done()
		return nil
	}
	factory := dynamicinformer.NewDynamicSharedInformerFactory(e.Dyn, chaosMeshInformerResync)
	gvrs := registeredGVRs()
	listers := make(map[schema.GroupVersionResource]cache.GenericLister, len(gvrs))
	for _, gvr := range gvrs {
		listers[gvr] = factory.ForResource(gvr).Lister()
	}
	e.factory = factory
	e.listers = listers
	e.informerMu.Unlock()

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	<-ctx.Done()
	return nil
}

// interpretChaosMeshStatus reads AllInjected / AllRecovered conditions,
// shared across PodChaos / NetworkChaos / HTTPChaos / etc. All Chaos-Mesh
// CRDs publish the same condition shape under status.conditions[].
//
// Destructive PodChaos actions (pod-kill, container-kill) never flip
// AllRecovered to True — the pod is gone, there's nothing to recover.
// Treat AllInjected=True as terminal-succeeded for those actions.
// Other actions recover via duration expiry → AllRecovered.
func interpretChaosMeshStatus(u *unstructured.Unstructured) ExecState {
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found {
		return ExecStatePending
	}
	var allInjected, allRecovered bool
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		s, _ := m["status"].(string)
		switch t {
		case "AllInjected":
			if s == "True" {
				allInjected = true
			}
		case "AllRecovered":
			if s == "True" {
				allRecovered = true
			}
		}
	}
	if allRecovered {
		return ExecStateSucceeded
	}
	if allInjected {
		if isDestructivePodChaos(u) {
			return ExecStateSucceeded
		}
		return ExecStateRunning
	}
	return ExecStatePending
}

func isDestructivePodChaos(u *unstructured.Unstructured) bool {
	if u.GetKind() != "PodChaos" {
		return false
	}
	action, _, _ := unstructured.NestedString(u.Object, "spec", "action")
	return action == "pod-kill" || action == "container-kill"
}

func readStatusDiagnostics(u *unstructured.Unstructured) map[string]any {
	st, found, _ := unstructured.NestedMap(u.Object, "status")
	if !found {
		return nil
	}
	return st
}

func (e *ChaosMeshExecutor) Destroy(ctx context.Context, handle string) error {
	h, err := decodeHandle(handle)
	if err != nil {
		return err
	}
	err = e.Dyn.Resource(h.GVR).Namespace(h.Namespace).Delete(ctx, h.Name, metav1.DeleteOptions{})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("chaos-mesh destroy: %w", err)
}

// durationFromParams reads the `duration_s` knob commonly carried in
// param payloads and converts it to a Chaos-Mesh duration string. Empty
// return means "no duration override" — caller should not set the field.
func durationFromParams(params map[string]any) string {
	v, ok := params["duration_s"]
	if !ok {
		return ""
	}
	switch n := v.(type) {
	case float64:
		return fmt.Sprintf("%ds", int(n))
	case int:
		return fmt.Sprintf("%ds", n)
	case int64:
		return fmt.Sprintf("%ds", n)
	case string:
		return n
	}
	return ""
}
