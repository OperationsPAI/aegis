package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	RegisterRenderer(runtimeMutatorRenderer{})
}

const runtimeMutatorChaosResource = "runtimemutatorchaos"

var runtimeMutatorChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: runtimeMutatorChaosResource,
}

// runtimeMutatorRenderer renders jvm_runtime_mutator points into the
// OperationsPAI-fork RuntimeMutatorChaos CRD. The mutation action
// (constant/operator/string) is carried in target.mutation_type_name and maps
// 1:1 to the fork's RuntimeMutatorChaosAction enum; constant uses from/to,
// operator/string use strategy (matching guided/resolver runtimeMutatorKey).
type runtimeMutatorRenderer struct{}

func (runtimeMutatorRenderer) Capability() string { return "jvm_runtime_mutator" }

func (runtimeMutatorRenderer) Maturity() string { return CapExperimental }

func (runtimeMutatorRenderer) HandlePrefix() string { return "aegis-jvmmut" }

func (runtimeMutatorRenderer) GVR() schema.GroupVersionResource { return runtimeMutatorChaosGVR }

func (runtimeMutatorRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh jvm_runtime_mutator: target.namespace is required")
	}
	return nil
}

func (runtimeMutatorRenderer) ValidateTarget(target map[string]any) error {
	for _, k := range []string{"namespace", "app", "class", "method", "mutation_type_name"} {
		if v, _ := target[k].(string); v == "" {
			return fmt.Errorf("chaos-mesh jvm_runtime_mutator: target.%s is required", k)
		}
	}
	action, err := runtimeMutatorAction(target)
	if err != nil {
		return err
	}
	// Mirror the fork's RuntimeMutatorChaos admission webhook: constant needs
	// from+to and rejects strategy; operator/string need strategy and reject
	// from/to. Catch the mismatch here so a malformed point fails at import
	// instead of opaquely at cluster apply.
	from, _ := target["mutation_from"].(string)
	to, _ := target["mutation_to"].(string)
	strategy, _ := target["mutation_strategy"].(string)
	switch action {
	case "constant":
		if from == "" || to == "" {
			return fmt.Errorf("chaos-mesh jvm_runtime_mutator: constant mutation requires mutation_from and mutation_to")
		}
		if strategy != "" {
			return fmt.Errorf("chaos-mesh jvm_runtime_mutator: constant mutation must not set mutation_strategy")
		}
	case "operator", "string":
		if strategy == "" {
			return fmt.Errorf("chaos-mesh jvm_runtime_mutator: %s mutation requires mutation_strategy", action)
		}
		if from != "" || to != "" {
			return fmt.Errorf("chaos-mesh jvm_runtime_mutator: %s mutation must not set mutation_from/mutation_to", action)
		}
	}
	return nil
}

func (runtimeMutatorRenderer) ValidateParams(map[string]any) error { return nil }

func (r runtimeMutatorRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	action, err := runtimeMutatorAction(target)
	if err != nil {
		return nil, err
	}

	spec := map[string]any{
		"action": action,
		"mode":   "all",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				sysCtx.LabelKey(): app,
			},
		},
		"class":  target["class"],
		"method": target["method"],
	}

	switch action {
	case "constant":
		if v, _ := target["mutation_from"].(string); v != "" {
			spec["from"] = v
		}
		if v, _ := target["mutation_to"].(string); v != "" {
			spec["to"] = v
		}
	case "operator", "string":
		if v, _ := target["mutation_strategy"].(string); v != "" {
			spec["strategy"] = v
		}
	}

	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "RuntimeMutatorChaos",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "aegis-chaos",
				"aegis-chaos/capability":       "jvm_runtime_mutator",
			},
		},
		"spec": spec,
	}}, nil
}

func runtimeMutatorAction(target map[string]any) (string, error) {
	action, _ := target["mutation_type_name"].(string)
	switch action {
	case "constant", "operator", "string":
		return action, nil
	}
	return "", fmt.Errorf("chaos-mesh jvm_runtime_mutator: unknown mutation_type_name %q (want constant|operator|string)", action)
}
