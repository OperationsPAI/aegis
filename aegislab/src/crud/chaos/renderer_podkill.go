package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	RegisterRenderer(podKillRenderer{})
}

type podKillRenderer struct{}

func (podKillRenderer) Capability() string                  { return "pod_kill" }
func (podKillRenderer) Maturity() string                    { return CapStable }
func (podKillRenderer) HandlePrefix() string                { return "pod-kill" }
func (podKillRenderer) GVR() schema.GroupVersionResource    { return podChaosGVR }

func (podKillRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh pod_kill: target.namespace is required")
	}
	return nil
}

func (r podKillRenderer) ValidateTarget(target map[string]any) error {
	if err := r.ValidateForHandle(target); err != nil {
		return err
	}
	if app, _ := target["app"].(string); app == "" {
		return fmt.Errorf("chaos-mesh pod_kill: target.app is required")
	}
	return nil
}

func (podKillRenderer) ValidateParams(_ map[string]any) error { return nil }

func (podKillRenderer) RenderCR(name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	spec := map[string]any{
		"action": "pod-kill",
		"mode":   "one",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				"app": app,
			},
		},
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
				"aegis-chaos/capability":       "pod_kill",
			},
		},
		"spec": spec,
	}}, nil
}
