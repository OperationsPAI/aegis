package chaos

import (
	"fmt"
	"strings"

	"aegis/platform/consts"

	guidedcli "aegis/platform/chaos"
)

// CrossServiceCapabilities is the set of capabilities whose Point row carries
// service_id IS NULL — per ADR-0011, only (system, capability, target) hashes
// into the point_id. Shared between aegisctl (--via-chaos) and the backend
// dispatcher so both address the identical Point.
var CrossServiceCapabilities = map[string]struct{}{
	"network_partition": {},
	"dns_error":         {},
	"dns_random":        {},
}

// GuidedToChaosTarget produces the canonical target map for the chosen
// capability from a finalized GuidedConfig. Per-capability shapes match
// tools/capgen/output/capabilities.json target_schema.required.
//
// target.namespace is the LOGICAL system name (catalog identifier), NOT the
// concrete kubernetes namespace. The concrete ns travels separately in the
// request body and is what the executor uses for CR placement.
func GuidedToChaosTarget(capability string, cfg guidedcli.GuidedConfig) (map[string]any, error) {
	app := strings.TrimSpace(cfg.App)
	system := strings.TrimSpace(cfg.System)
	if system == "" {
		system = strings.TrimSpace(cfg.SystemType)
	}
	if app == "" || system == "" {
		return nil, fmt.Errorf("via-chaos: app and system are required (got app=%q system=%q)", app, system)
	}

	switch capability {
	case "pod_failure", "pod_kill", "jvm_gc":
		return map[string]any{"namespace": system, "app": app}, nil
	case "container_kill", "cpu_stress", "memory_stress", "time_skew":
		container := strings.TrimSpace(cfg.Container)
		if container == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires container", capability)
		}
		return map[string]any{"namespace": system, "app": app, "container": container}, nil
	case "network_bandwidth", "network_corrupt", "network_delay",
		"network_duplicate", "network_loss", "network_partition":
		target := strings.TrimSpace(cfg.TargetService)
		if target == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires target_service", capability)
		}
		return map[string]any{"namespace": system, "source_app": app, "target_service": target}, nil
	case "dns_error", "dns_random":
		domain := strings.TrimSpace(cfg.Domain)
		if domain == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires domain", capability)
		}
		parts := splitTrim(domain, ",")
		return map[string]any{"namespace": system, "app": app, "domain_patterns": parts}, nil
	case "jvm_cpu_stress", "jvm_memory_stress",
		"jvm_method_exception", "jvm_method_latency", "jvm_method_return",
		"jvm_runtime_mutator":
		class := strings.TrimSpace(cfg.Class)
		method := strings.TrimSpace(cfg.Method)
		if class == "" || method == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires class and method", capability)
		}
		return map[string]any{"namespace": system, "app": app, "class": class, "method": method}, nil
	case "jvm_mysql_exception", "jvm_mysql_latency":
		db := strings.TrimSpace(cfg.Database)
		table := strings.TrimSpace(cfg.Table)
		if db == "" || table == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires database and table", capability)
		}
		target := map[string]any{"namespace": system, "app": app, "db_name": db, "table": table}
		if op := strings.ToLower(strings.TrimSpace(cfg.Operation)); op != "" {
			target["sql_type"] = op
		}
		return target, nil
	case "http_request_abort", "http_request_delay", "http_request_replace_method",
		"http_request_replace_path", "http_response_abort", "http_response_delay",
		"http_response_patch_body", "http_response_replace_body", "http_response_replace_code":
		route := strings.TrimSpace(cfg.Route)
		method := strings.TrimSpace(cfg.HTTPMethod)
		if route == "" || method == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires route and http_method", capability)
		}
		return nil, fmt.Errorf("via-chaos: HTTP capabilities (%s) require a port that GuidedConfig does not yet expose; needs guided extension", capability)
	default:
		return nil, fmt.Errorf("via-chaos: unmapped capability %s", capability)
	}
}

// GuidedToChaosParams extracts the capability-specific param map.
func GuidedToChaosParams(capability string, cfg guidedcli.GuidedConfig) map[string]any {
	// GuidedConfig.Duration is in minutes (pinned to FixedAbnormalWindowMinutes
	// upstream in api_types.go). chaos service expects seconds.
	durationS := consts.FixedAbnormalWindowSeconds
	if cfg.Duration != nil && *cfg.Duration > 0 {
		durationS = *cfg.Duration * 60
	}
	out := map[string]any{"duration_s": durationS}

	derefOr := func(p *int, key string) {
		if p != nil {
			out[key] = *p
		}
	}

	switch capability {
	case "cpu_stress":
		derefOr(cfg.CPULoad, "load_pct")
		derefOr(cfg.CPUWorker, "workers")
	case "memory_stress":
		derefOr(cfg.MemorySize, "size_mib")
		derefOr(cfg.MemWorker, "workers")
		if cfg.MemType != "" {
			out["memory_type"] = cfg.MemType
		}
	case "network_delay":
		derefOr(cfg.LatencyMs, "latency_ms")
		derefOr(cfg.Jitter, "jitter_ms")
		derefOr(cfg.Correlation, "correlation_pct")
	case "network_loss":
		derefOr(cfg.Loss, "loss_pct")
		derefOr(cfg.Correlation, "correlation_pct")
	case "network_duplicate":
		derefOr(cfg.Duplicate, "duplicate_pct")
		derefOr(cfg.Correlation, "correlation_pct")
	case "network_corrupt":
		derefOr(cfg.Corrupt, "corrupt_pct")
		derefOr(cfg.Correlation, "correlation_pct")
	case "network_bandwidth":
		derefOr(cfg.Rate, "rate_kbps")
		derefOr(cfg.Limit, "limit")
		derefOr(cfg.Buffer, "buffer")
	case "time_skew":
		derefOr(cfg.TimeOffset, "offset_s")
	case "jvm_cpu_stress":
		derefOr(cfg.CPUCount, "cpu_count")
	case "jvm_memory_stress":
		if cfg.MemType != "" {
			out["memory_type"] = cfg.MemType
		}
	case "jvm_method_latency":
		derefOr(cfg.LatencyDuration, "delay_ms")
	case "jvm_method_exception":
		if cfg.ExceptionOpt != "" {
			out["exception_mode"] = cfg.ExceptionOpt
		}
	case "jvm_method_return":
		if cfg.ReturnType != "" {
			out["return_type"] = cfg.ReturnType
		}
		if cfg.ReturnValueOpt != "" {
			out["value_mode"] = cfg.ReturnValueOpt
		}
	}
	return out
}

// GuidedChaosPointID derives the Point ID for a finalized guided config using
// the same canonicalisation rule the chaos service applies at manifest import.
// instance and chartVersion address per-instance seed catalog rows without a
// backend round-trip.
func GuidedChaosPointID(cfg guidedcli.GuidedConfig, instance, chartVersion string) (string, string, map[string]any, error) {
	cap, ok := ChaosTypeToCapability[strings.TrimSpace(cfg.ChaosType)]
	if !ok {
		return "", "", nil, fmt.Errorf("via-chaos: unknown chaos_type %q (no capability mapping)", cfg.ChaosType)
	}
	target, err := GuidedToChaosTarget(cap, cfg)
	if err != nil {
		return "", "", nil, err
	}

	system := strings.TrimSpace(cfg.System)
	if system == "" {
		system = strings.TrimSpace(cfg.SystemType)
	}
	if system == "" {
		return "", "", nil, fmt.Errorf("via-chaos: system (or system_type) is required to address a Point")
	}

	if _, isCross := CrossServiceCapabilities[cap]; isCross {
		id, err := CrossServicePointID(CrossServicePointIdentity{
			System:     system,
			Capability: cap,
			Target:     target,
		})
		return id, cap, target, err
	}

	service := strings.TrimSpace(cfg.App)
	id, err := ServiceBoundPointID(PointIdentity{
		System:       system,
		Service:      service,
		Instance:     instance,
		ChartVersion: chartVersion,
		Capability:   cap,
		Target:       target,
	})
	return id, cap, target, err
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
