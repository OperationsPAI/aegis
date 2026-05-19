package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	ChaosMeshGroup       = "chaos-mesh.org"
	ChaosMeshVersion     = "v1alpha1"
	PodChaosResource     = "podchaos"
	ExecutorNameChaosMesh = "chaos-mesh"
)

var podChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: PodChaosResource,
}

// ChaosMeshGroupVersionResourceForPodChaos exposes the PodChaos GVR for
// out-of-package callers (notably the conformance harness) without
// reflecting the unexported var.
func ChaosMeshGroupVersionResourceForPodChaos() schema.GroupVersionResource {
	return podChaosGVR
}

// ChaosMeshExecutor renders supported Capabilities into Chaos-Mesh CRs.
// Step 1 supports `pod_kill` only.
type ChaosMeshExecutor struct {
	Dyn dynamic.Interface
}

var _ Executor = (*ChaosMeshExecutor)(nil)

func NewChaosMeshExecutor(dyn dynamic.Interface) *ChaosMeshExecutor {
	return &ChaosMeshExecutor{Dyn: dyn}
}

func (e *ChaosMeshExecutor) Name() string { return ExecutorNameChaosMesh }

func (e *ChaosMeshExecutor) SupportedCapabilities() []CapabilitySupport {
	return []CapabilitySupport{{Capability: "pod_kill", Maturity: CapStable}}
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
	if capability != "pod_kill" {
		return "", fmt.Errorf("chaos-mesh executor: unsupported capability %q", capability)
	}
	ns, _ := target["namespace"].(string)
	if ns == "" {
		return "", fmt.Errorf("chaos-mesh pod_kill: target.namespace is required")
	}
	name, err := DeriveChaosMeshCRName("pod-kill", idempotencyKey)
	if err != nil {
		return "", err
	}
	return encodeHandle(ChaosMeshHandle{GVR: podChaosGVR, Namespace: ns, Name: name})
}

func (e *ChaosMeshExecutor) Apply(
	ctx context.Context,
	capability, handle string,
	target, params map[string]any,
) error {
	if capability != "pod_kill" {
		return fmt.Errorf("chaos-mesh executor: unsupported capability %q", capability)
	}
	app, _ := target["app"].(string)
	if app == "" {
		return fmt.Errorf("chaos-mesh pod_kill: target.app is required")
	}
	h, err := decodeHandle(handle)
	if err != nil {
		return err
	}
	cr := buildPodKillCR(h.Name, h.Namespace, app, params)
	_, err = e.Dyn.Resource(h.GVR).Namespace(h.Namespace).Create(ctx, cr, metav1.CreateOptions{})
	switch {
	case err == nil:
		return nil
	case apierrors.IsAlreadyExists(err):
		return nil
	default:
		return fmt.Errorf("chaos-mesh pod_kill: create CR: %w", err)
	}
}

func buildPodKillCR(name, ns, app string, params map[string]any) *unstructured.Unstructured {
	duration := ""
	if v, ok := params["duration_s"]; ok {
		switch n := v.(type) {
		case float64:
			duration = fmt.Sprintf("%ds", int(n))
		case int:
			duration = fmt.Sprintf("%ds", n)
		case int64:
			duration = fmt.Sprintf("%ds", n)
		case string:
			duration = strings.TrimSpace(n)
		}
	}
	spec := map[string]any{
		"action": "pod-kill",
		"mode":   "one",
		"selector": map[string]any{
			"namespaces": []any{ns},
			"labelSelectors": map[string]any{
				"app": app,
			},
		},
	}
	if duration != "" {
		spec["duration"] = duration
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "PodChaos",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "aegis-chaos",
				"aegis-chaos/capability":       "pod_kill",
			},
		},
		"spec": spec,
	}}
	return u
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
	return interpretPodChaosStatus(got), readStatusDiagnostics(got), nil
}

func interpretPodChaosStatus(u *unstructured.Unstructured) ExecState {
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
