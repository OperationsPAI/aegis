package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	RegisterRenderer(timeSkewRenderer{})
}

type timeSkewRenderer struct{}

func (timeSkewRenderer) Capability() string                  { return "time_skew" }
func (timeSkewRenderer) Maturity() string                    { return CapExperimental }
func (timeSkewRenderer) HandlePrefix() string                { return "aegis-timeskew" }
func (timeSkewRenderer) GVR() schema.GroupVersionResource    { return timeChaosGVR }

func (timeSkewRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh time_skew: target.namespace is required")
	}
	return nil
}

func (r timeSkewRenderer) ValidateTarget(target map[string]any) error {
	if err := r.ValidateForHandle(target); err != nil {
		return err
	}
	for _, k := range []string{"app", "container"} {
		if v, _ := target[k].(string); v == "" {
			return fmt.Errorf("chaos-mesh time_skew: target.%s is required", k)
		}
	}
	return nil
}

func (timeSkewRenderer) ValidateParams(params map[string]any) error {
	// chaos-mesh TimeChaosSpec.TimeOffset has no omitempty — webhook
	// rejects empty. Capgen marks offset_s required.
	if _, err := getInt(params, "offset_s"); err != nil {
		return fmt.Errorf("chaos-mesh time_skew: params.offset_s is required: %w", err)
	}
	return nil
}

func (timeSkewRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	container, _ := target["container"].(string)
	offset, _ := getInt(params, "offset_s")

	spec := map[string]any{
		"mode": "all",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				sysCtx.LabelKey(): app,
			},
		},
		"containerNames": []any{container},
		"timeOffset":     fmt.Sprintf("%ds", offset),
	}
	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "TimeChaos",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "aegis-chaos",
				"aegis-chaos/capability":       "time_skew",
			},
		},
		"spec": spec,
	}}, nil
}
