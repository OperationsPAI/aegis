package chaos

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
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

// ChaosMeshGroupVersionResourceForPodChaos exposes the PodChaos GVR for
// out-of-package callers (notably the conformance harness) without
// reflecting the unexported var.
func ChaosMeshGroupVersionResourceForPodChaos() schema.GroupVersionResource {
	return podChaosGVR
}

// ChaosMeshGroupVersionResourceForNetworkChaos exposes the NetworkChaos
// GVR for out-of-package callers (conformance harness).
func ChaosMeshGroupVersionResourceForNetworkChaos() schema.GroupVersionResource {
	return networkChaosGVR
}

type ChaosMeshExecutor struct {
	Dyn dynamic.Interface
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
	capability, idempotencyKey string, target map[string]any,
) (string, error) {
	r, err := lookupRenderer(capability)
	if err != nil {
		return "", err
	}
	if err := r.ValidateForHandle(target); err != nil {
		return "", err
	}
	ns, _ := target["namespace"].(string)
	name, err := DeriveChaosMeshCRName(r.HandlePrefix(), idempotencyKey)
	if err != nil {
		return "", err
	}
	return encodeHandle(ChaosMeshHandle{GVR: r.GVR(), Namespace: ns, Name: name})
}

func (e *ChaosMeshExecutor) Apply(
	ctx context.Context,
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
	cr, err := r.RenderCR(h.Name, h.Namespace, target, params)
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
	got, err := e.Dyn.Resource(h.GVR).Namespace(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CR is gone — for terminal injections this is the post-Destroy
			// steady state; callers interpret in context.
			return ExecStateSucceeded, map[string]any{"reason": "cr_absent"}, nil
		}
		return ExecStateFailed, nil, fmt.Errorf("chaos-mesh status: %w", err)
	}
	return interpretChaosMeshStatus(got), readStatusDiagnostics(got), nil
}

// interpretChaosMeshStatus reads AllInjected / AllRecovered conditions,
// shared across PodChaos / NetworkChaos / HTTPChaos / etc. All Chaos-Mesh
// CRDs publish the same condition shape under status.conditions[].
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
		return ExecStateRunning
	}
	return ExecStatePending
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
