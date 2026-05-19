package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	for _, action := range networkActions {
		RegisterRenderer(networkRenderer{action: action})
	}
}

var networkActions = []string{
	"delay", "loss", "duplicate", "corrupt", "bandwidth", "partition",
}

type networkRenderer struct {
	action string
}

func (r networkRenderer) Capability() string {
	return "network_" + r.action
}

func (r networkRenderer) Maturity() string { return CapExperimental }

func (r networkRenderer) HandlePrefix() string {
	switch r.action {
	case "delay":
		return "aegis-netdelay"
	case "loss":
		return "aegis-netloss"
	case "duplicate":
		return "aegis-netdup"
	case "corrupt":
		return "aegis-netcorrupt"
	case "bandwidth":
		return "aegis-netbw"
	case "partition":
		return "aegis-netpart"
	}
	return "aegis-net"
}

func (r networkRenderer) GVR() schema.GroupVersionResource { return networkChaosGVR }

func (r networkRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh %s: target.namespace is required", r.Capability())
	}
	return nil
}

func (r networkRenderer) ValidateTarget(target map[string]any) error {
	cap := r.Capability()
	for _, k := range []string{"namespace", "source_app", "target_service"} {
		v, _ := target[k].(string)
		if v == "" {
			return fmt.Errorf("chaos-mesh %s: target.%s is required", cap, k)
		}
	}
	if dir, ok := target["direction"]; ok {
		ds, _ := dir.(string)
		// Step 2 ships `to` only; `from` / `both` need selector↔target
		// swap that hasn't been validated end-to-end.
		switch ds {
		case "", "to":
		default:
			return fmt.Errorf("chaos-mesh %s: target.direction must be \"to\" (step 2)", cap)
		}
	}
	return nil
}

func (r networkRenderer) ValidateParams(params map[string]any) error {
	cap := r.Capability()
	switch r.action {
	case "delay":
		if _, err := getInt(params, "latency_ms"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.latency_ms is required: %w", cap, err)
		}
	case "loss":
		if _, err := getInt(params, "loss_pct"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.loss_pct is required: %w", cap, err)
		}
	case "duplicate":
		if _, err := getInt(params, "duplicate_pct"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.duplicate_pct is required: %w", cap, err)
		}
	case "corrupt":
		if _, err := getInt(params, "corrupt_pct"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.corrupt_pct is required: %w", cap, err)
		}
	case "bandwidth":
		// chaos-mesh BandwidthSpec marks all three non-optional with
		// Minimum=1; the webhook rejects CRs missing any of them.
		for _, k := range []string{"rate_kbps", "limit", "buffer"} {
			if _, err := getInt(params, k); err != nil {
				return fmt.Errorf("chaos-mesh %s: params.%s is required: %w", cap, k, err)
			}
		}
	case "partition":
		// no required params
	}
	return nil
}

func (r networkRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	sourceApp, _ := target["source_app"].(string)
	targetService, _ := target["target_service"].(string)
	direction, _ := target["direction"].(string)
	if direction == "" {
		direction = "to"
	}

	labelKey := sysCtx.LabelKey()
	spec := map[string]any{
		"action":    r.action,
		"mode":      "all",
		"direction": direction,
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				labelKey: sourceApp,
			},
		},
		"target": map[string]any{
			"mode": "all",
			"selector": map[string]any{
				"namespaces": []any{namespace},
				"labelSelectors": map[string]any{
					labelKey: targetService,
				},
			},
		},
	}

	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	if err := r.attachActionParams(spec, params); err != nil {
		return nil, err
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "NetworkChaos",
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

// attachActionParams writes the TC-parameter sub-object for delay/loss/
// duplicate/corrupt/bandwidth onto spec. partition has none. Field names
// and shapes mirror chaos-mesh v1alpha1.NetworkChaosSpec TcParameter
// (see chaos-experiment/chaos/network_chaos.go for the live wiring).
func (r networkRenderer) attachActionParams(spec map[string]any, params map[string]any) error {
	corr := pctStringOrZero(params, "correlation_pct")
	switch r.action {
	case "delay":
		latency, _ := getInt(params, "latency_ms")
		sub := map[string]any{
			"latency":     fmt.Sprintf("%dms", latency),
			"correlation": corr,
		}
		if j, err := getInt(params, "jitter_ms"); err == nil {
			sub["jitter"] = fmt.Sprintf("%dms", j)
		}
		spec["delay"] = sub
	case "loss":
		pct, _ := getInt(params, "loss_pct")
		spec["loss"] = map[string]any{
			"loss":        fmt.Sprintf("%d", pct),
			"correlation": corr,
		}
	case "duplicate":
		pct, _ := getInt(params, "duplicate_pct")
		spec["duplicate"] = map[string]any{
			"duplicate":   fmt.Sprintf("%d", pct),
			"correlation": corr,
		}
	case "corrupt":
		pct, _ := getInt(params, "corrupt_pct")
		spec["corrupt"] = map[string]any{
			"corrupt":     fmt.Sprintf("%d", pct),
			"correlation": corr,
		}
	case "bandwidth":
		rate, _ := getInt(params, "rate_kbps")
		bw := map[string]any{
			"rate": fmt.Sprintf("%dkbps", rate),
		}
		if v, err := getInt(params, "limit"); err == nil {
			bw["limit"] = int64(v)
		}
		if v, err := getInt(params, "buffer"); err == nil {
			bw["buffer"] = int64(v)
		}
		spec["bandwidth"] = bw
	case "partition":
		// nothing extra; action=partition + selector/target/direction is the whole spec
	}
	return nil
}

func getInt(m map[string]any, key string) (int, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("missing %q", key)
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	}
	return 0, fmt.Errorf("param %q must be a number, got %T", key, v)
}

func pctStringOrZero(m map[string]any, key string) string {
	v, err := getInt(m, key)
	if err != nil {
		return "0"
	}
	return fmt.Sprintf("%d", v)
}
