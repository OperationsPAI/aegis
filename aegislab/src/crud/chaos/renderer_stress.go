package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	RegisterRenderer(stressRenderer{kind: "cpu"})
	RegisterRenderer(stressRenderer{kind: "memory"})
}

type stressRenderer struct {
	kind string // "cpu" | "memory"
}

func (r stressRenderer) Capability() string { return r.kind + "_stress" }

func (stressRenderer) Maturity() string { return CapExperimental }

func (r stressRenderer) HandlePrefix() string {
	switch r.kind {
	case "cpu":
		return "aegis-cpustress"
	case "memory":
		return "aegis-memstress"
	}
	return "aegis-stress"
}

func (stressRenderer) GVR() schema.GroupVersionResource { return stressChaosGVR }

func (r stressRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh %s: target.namespace is required", r.Capability())
	}
	return nil
}

func (r stressRenderer) ValidateTarget(target map[string]any) error {
	if err := r.ValidateForHandle(target); err != nil {
		return err
	}
	cap := r.Capability()
	for _, k := range []string{"app", "container"} {
		if v, _ := target[k].(string); v == "" {
			return fmt.Errorf("chaos-mesh %s: target.%s is required", cap, k)
		}
	}
	return nil
}

func (r stressRenderer) ValidateParams(params map[string]any) error {
	cap := r.Capability()
	switch r.kind {
	case "cpu":
		// chaos-mesh CPUStressor: Workers required (no omitempty), Load *int
		// optional but capgen marks load_pct required (effect is meaningful
		// only with a non-zero load).
		if _, err := getInt(params, "load_pct"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.load_pct is required: %w", cap, err)
		}
	case "memory":
		// MemoryStressor.Size is omitempty in the struct tag but the
		// stress-ng worker needs a target size to allocate. capgen marks
		// size_mib required.
		if _, err := getInt(params, "size_mib"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.size_mib is required: %w", cap, err)
		}
	}
	return nil
}

func (r stressRenderer) RenderCR(name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	container, _ := target["container"].(string)

	workers := 1
	if v, err := getInt(params, "workers"); err == nil {
		workers = v
	}

	var stressors map[string]any
	switch r.kind {
	case "cpu":
		load, _ := getInt(params, "load_pct")
		stressors = map[string]any{
			"cpu": map[string]any{
				"workers": int64(workers),
				"load":    int64(load),
			},
		}
	case "memory":
		size, _ := getInt(params, "size_mib")
		stressors = map[string]any{
			"memory": map[string]any{
				"workers": int64(workers),
				"size":    fmt.Sprintf("%dMiB", size),
			},
		}
	}

	spec := map[string]any{
		"mode": "all",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				"app": app,
			},
		},
		"containerNames": []any{container},
		"stressors":      stressors,
	}
	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "StressChaos",
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
