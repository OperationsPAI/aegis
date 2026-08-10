package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	RegisterRenderer(podChaosExtraRenderer{action: "container-kill"})
	RegisterRenderer(podChaosExtraRenderer{action: "pod-failure"})
}

// podChaosExtraRenderer handles the two PodChaos siblings of pod_kill
// (container-kill and pod-failure). pod_kill stays in renderer_podkill.go
// because it ships `stable` and has a distinct contract; the two
// experimental siblings only differ from each other in action + the
// container-kill containerNames requirement.
type podChaosExtraRenderer struct {
	action string // "container-kill" | "pod-failure"
}

func (r podChaosExtraRenderer) Capability() string {
	switch r.action {
	case "container-kill":
		return "container_kill"
	case "pod-failure":
		return "pod_failure"
	}
	return ""
}

func (podChaosExtraRenderer) Maturity() string { return CapExperimental }

func (r podChaosExtraRenderer) HandlePrefix() string {
	switch r.action {
	case "container-kill":
		return "aegis-ctnkill"
	case "pod-failure":
		return "aegis-podfail"
	}
	return "aegis-pod"
}

func (podChaosExtraRenderer) GVR() schema.GroupVersionResource { return podChaosGVR }

func (r podChaosExtraRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh %s: target.namespace is required", r.Capability())
	}
	return nil
}

func (r podChaosExtraRenderer) ValidateTarget(target map[string]any) error {
	if err := r.ValidateForHandle(target); err != nil {
		return err
	}
	cap := r.Capability()
	if app, _ := target["app"].(string); app == "" {
		return fmt.Errorf("chaos-mesh %s: target.app is required", cap)
	}
	if r.action == "container-kill" {
		// chaos-mesh PodChaos webhook enforces ContainerNames non-empty
		// when action==container-kill (GetSelectorSpecs returns the
		// ContainerSelector for this action).
		if c, _ := target["container"].(string); c == "" {
			return fmt.Errorf("chaos-mesh %s: target.container is required", cap)
		}
	}
	return nil
}

func (podChaosExtraRenderer) ValidateParams(_ map[string]any) error { return nil }

func (r podChaosExtraRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	spec := map[string]any{
		"action": r.action,
		"mode":   "one",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				sysCtx.LabelKey(): app,
			},
		},
	}
	if r.action == "container-kill" {
		container, _ := target["container"].(string)
		spec["containerNames"] = []any{container}
	}
	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "PodChaos",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "aegis-chaos",
				"aegis-chaos/capability":       r.Capability(),
			},
		},
		"spec": spec,
	}}, nil
}
