package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	RegisterRenderer(dnsRenderer{action: "error"})
	RegisterRenderer(dnsRenderer{action: "random"})
}

type dnsRenderer struct {
	action string // "error" | "random"
}

func (r dnsRenderer) Capability() string { return "dns_" + r.action }

func (dnsRenderer) Maturity() string { return CapExperimental }

func (r dnsRenderer) HandlePrefix() string {
	switch r.action {
	case "error":
		return "aegis-dnserr"
	case "random":
		return "aegis-dnsrand"
	}
	return "aegis-dns"
}

func (dnsRenderer) GVR() schema.GroupVersionResource { return dnsChaosGVR }

func (r dnsRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh %s: target.namespace is required", r.Capability())
	}
	return nil
}

func (r dnsRenderer) ValidateTarget(target map[string]any) error {
	if err := r.ValidateForHandle(target); err != nil {
		return err
	}
	cap := r.Capability()
	if app, _ := target["app"].(string); app == "" {
		return fmt.Errorf("chaos-mesh %s: target.app is required", cap)
	}
	// spec.patterns is omitempty in chaos-mesh's struct tag, but a
	// DNSChaos with empty patterns matches everything — capgen forbids
	// that bluntness because the blast radius is unbounded.
	pats, err := patternList(target["domain_patterns"])
	if err != nil {
		return fmt.Errorf("chaos-mesh %s: target.domain_patterns: %w", cap, err)
	}
	if len(pats) == 0 {
		return fmt.Errorf("chaos-mesh %s: target.domain_patterns must be non-empty", cap)
	}
	return nil
}

func (dnsRenderer) ValidateParams(_ map[string]any) error { return nil }

func (r dnsRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	pats, err := patternList(target["domain_patterns"])
	if err != nil {
		return nil, err
	}
	patternsAny := make([]any, 0, len(pats))
	for _, p := range pats {
		patternsAny = append(patternsAny, p)
	}

	spec := map[string]any{
		"action": r.action,
		"mode":   "all",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				sysCtx.LabelKey(): app,
			},
		},
		"patterns": patternsAny,
	}
	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "DNSChaos",
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

func patternList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		// Tolerate already-typed slices that surface from non-JSON paths
		// (tests, in-process callers).
		if ss, ok := v.([]string); ok {
			return ss, nil
		}
		return nil, fmt.Errorf("must be a string array, got %T", v)
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("item %d must be a non-empty string", i)
		}
		out = append(out, s)
	}
	return out, nil
}
