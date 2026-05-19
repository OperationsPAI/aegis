package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"
	chaoscrud "aegis/crud/chaos"
	"aegis/platform/consts"
	"aegis/platform/dto"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/google/uuid"
)

// chaosTypeToCapability maps the legacy guided `chaos_type` enum
// (camel-case, chaos-experiment CRD action names) onto the lane-A capability
// `name` registered in tools/capgen/output/capabilities.json. Hand-written
// rather than generated because (a) capgen is a one-shot dev-time tool whose
// output is committed JSON, not a Go package, and (b) reading the JSON at
// process start would couple aegisctl to a runtime asset path we'd then need
// to ship alongside the binary. The 32 entries are stable — adding a 33rd
// capability also requires a code change downstream, so the lookup table
// staying in lockstep is not a forgotten-update risk.
var chaosTypeToCapability = map[string]string{
	"ContainerKill":            "container_kill",
	"CPUStress":                "cpu_stress",
	"DNSError":                 "dns_error",
	"DNSRandom":                "dns_random",
	"HTTPRequestAbort":         "http_request_abort",
	"HTTPRequestDelay":         "http_request_delay",
	"HTTPRequestReplaceMethod": "http_request_replace_method",
	"HTTPRequestReplacePath":   "http_request_replace_path",
	"HTTPResponseAbort":        "http_response_abort",
	"HTTPResponseDelay":        "http_response_delay",
	"HTTPResponsePatchBody":    "http_response_patch_body",
	"HTTPResponseReplaceBody":  "http_response_replace_body",
	"HTTPResponseReplaceCode":  "http_response_replace_code",
	"JVMCPUStress":             "jvm_cpu_stress",
	"JVMGarbageCollector":      "jvm_gc",
	"JVMMemoryStress":          "jvm_memory_stress",
	"JVMException":             "jvm_method_exception",
	"JVMLatency":               "jvm_method_latency",
	"JVMReturn":                "jvm_method_return",
	"JVMMySQLException":        "jvm_mysql_exception",
	"JVMMySQLLatency":          "jvm_mysql_latency",
	"JVMRuntimeMutator":        "jvm_runtime_mutator",
	"MemoryStress":             "memory_stress",
	"NetworkBandwidth":         "network_bandwidth",
	"NetworkCorrupt":           "network_corrupt",
	"NetworkDelay":             "network_delay",
	"NetworkDuplicate":         "network_duplicate",
	"NetworkLoss":              "network_loss",
	"NetworkPartition":         "network_partition",
	"PodFailure":               "pod_failure",
	"PodKill":                  "pod_kill",
	"TimeSkew":                 "time_skew",
}

// crossServiceCapabilities is the set of capabilities whose Point row carries
// service_id IS NULL — per ADR-0011 cross-service variant, only
// (system, capability, target) participates in the point_id hash.
var crossServiceCapabilities = map[string]struct{}{
	"network_partition": {},
	"dns_error":         {},
	"dns_random":        {},
}

// guidedToChaosTarget produces the canonical target map for the chosen
// capability from a finalized GuidedConfig. Per-capability shapes match
// tools/capgen/output/capabilities.json target_schema.required.
func guidedToChaosTarget(capability string, cfg guidedcli.GuidedConfig) (map[string]any, error) {
	app := strings.TrimSpace(cfg.App)
	ns := strings.TrimSpace(cfg.Namespace)
	if app == "" || ns == "" {
		return nil, fmt.Errorf("via-chaos: app and namespace are required (got app=%q namespace=%q)", app, ns)
	}

	switch capability {
	case "pod_failure", "pod_kill", "jvm_gc":
		return map[string]any{"namespace": ns, "app": app}, nil
	case "container_kill", "cpu_stress", "memory_stress", "time_skew":
		container := strings.TrimSpace(cfg.Container)
		if container == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires container", capability)
		}
		return map[string]any{"namespace": ns, "app": app, "container": container}, nil
	case "network_bandwidth", "network_corrupt", "network_delay",
		"network_duplicate", "network_loss", "network_partition":
		target := strings.TrimSpace(cfg.TargetService)
		if target == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires target_service", capability)
		}
		return map[string]any{"namespace": ns, "source_app": app, "target_service": target}, nil
	case "dns_error", "dns_random":
		domain := strings.TrimSpace(cfg.Domain)
		if domain == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires domain", capability)
		}
		// Server-side schema uses `domain_patterns` (array). YAML carries a
		// single comma-separated `domain` string today; split on commas.
		parts := splitTrim(domain, ",")
		return map[string]any{"namespace": ns, "app": app, "domain_patterns": parts}, nil
	case "jvm_cpu_stress", "jvm_memory_stress",
		"jvm_method_exception", "jvm_method_latency", "jvm_method_return",
		"jvm_mysql_exception", "jvm_mysql_latency", "jvm_runtime_mutator":
		class := strings.TrimSpace(cfg.Class)
		method := strings.TrimSpace(cfg.Method)
		if class == "" || method == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires class and method", capability)
		}
		return map[string]any{"namespace": ns, "app": app, "class": class, "method": method}, nil
	case "http_request_abort", "http_request_delay", "http_request_replace_method",
		"http_request_replace_path", "http_response_abort", "http_response_delay",
		"http_response_patch_body", "http_response_replace_body", "http_response_replace_code":
		route := strings.TrimSpace(cfg.Route)
		method := strings.TrimSpace(cfg.HTTPMethod)
		if route == "" || method == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires route and http_method", capability)
		}
		// Port is not carried in GuidedConfig today — guided resolver embeds it
		// into the route option metadata. Until guided emits port directly we
		// can't safely synthesise the target; fail loud rather than guess.
		return nil, fmt.Errorf("via-chaos: HTTP capabilities (%s) require a port that GuidedConfig does not yet expose; needs guided extension", capability)
	default:
		return nil, fmt.Errorf("via-chaos: unmapped capability %s", capability)
	}
}

// guidedToChaosParams extracts the capability-specific param map.
func guidedToChaosParams(capability string, cfg guidedcli.GuidedConfig) map[string]any {
	durationS := consts.FixedAbnormalWindowSeconds
	if cfg.Duration != nil && *cfg.Duration > 0 {
		durationS = *cfg.Duration
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

// guidedChaosPointID derives the Point ID for a finalized guided config using
// the same canonicalisation rule the chaos service applies at manifest import.
// `instance` and `chartVersion` are taken from the (per-process) via-chaos
// flags so seed catalog rows are addressable without a backend round-trip.
func guidedChaosPointID(cfg guidedcli.GuidedConfig, instance, chartVersion string) (string, string, map[string]any, error) {
	cap, ok := chaosTypeToCapability[strings.TrimSpace(cfg.ChaosType)]
	if !ok {
		return "", "", nil, fmt.Errorf("via-chaos: unknown chaos_type %q (no capability mapping)", cfg.ChaosType)
	}
	target, err := guidedToChaosTarget(cap, cfg)
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

	if _, isCross := crossServiceCapabilities[cap]; isCross {
		id, err := chaoscrud.CrossServicePointID(chaoscrud.CrossServicePointIdentity{
			System:     system,
			Capability: cap,
			Target:     target,
		})
		return id, cap, target, err
	}

	service := strings.TrimSpace(cfg.App)
	id, err := chaoscrud.ServiceBoundPointID(chaoscrud.PointIdentity{
		System:       system,
		Service:      service,
		Instance:     instance,
		ChartVersion: chartVersion,
		Capability:   cap,
		Target:       target,
	})
	return id, cap, target, err
}

// submitGuidedViaChaos routes finalized guided configs through the chaos
// service. Singleton specs go to /v1beta/injections; multi-spec batches go to
// /v1beta/injection-batches. caller_metadata carries the campaign-side
// linkage the backend hook receiver needs to rejoin the state machine.
func submitGuidedViaChaos(cfgs []guidedcli.GuidedConfig, opts guidedApplyOptions) error {
	if len(cfgs) == 0 {
		return usageErrorf("no guided config to apply")
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}

	traceID := uuid.NewString()
	taskID := uuid.NewString()
	benchmark := &dto.ContainerVersionItem{
		Name: opts.BenchmarkName,
		// dto.ContainerVersionItem has no Tag/Version field directly — the
		// version-tag travels in ImageRef as `name:tag` after backend resolve.
		// At submit-time we only know the user's tag input; encode it there
		// so the receiver can re-parse without a DB round-trip.
		ImageRef: opts.BenchmarkName + ":" + opts.BenchmarkTag,
	}
	datapack := &dto.InjectionItem{
		// Datapack name is the campaign step's identity; until guided emits it
		// the trace id is the best-stable correlator.
		Name: taskID,
	}

	makeMeta := func() map[string]any {
		// Marshal via JSON round-trip so the receiver decodes against the live
		// CallerMetadata struct (single source of truth for tag names).
		m := struct {
			TaskID    string                     `json:"task_id"`
			TraceID   string                     `json:"trace_id"`
			Benchmark *dto.ContainerVersionItem  `json:"benchmark,omitempty"`
			Datapack  *dto.InjectionItem         `json:"datapack,omitempty"`
		}{
			TaskID:    taskID,
			TraceID:   traceID,
			Benchmark: benchmark,
			Datapack:  datapack,
		}
		raw, _ := json.Marshal(m)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}

	if len(cfgs) == 1 {
		pid, cap, _, err := guidedChaosPointID(cfgs[0], guidedChaosInstance, guidedChaosChartVersion)
		if err != nil {
			return err
		}
		params := guidedToChaosParams(cap, cfgs[0])
		idemKey := traceID + ":" + pid
		body := *apiclient.NewChaosChaosCreateInjectionReq()
		body.PointId = &pid
		body.IdempotencyKey = &idemKey
		body.Params = params
		body.CallerMetadata = makeMeta()
		resp, _, err := cli.ChaosAPI.ChaosCreateInjection(ctx).
			ChaosChaosCreateInjectionReq(body).Execute()
		if err != nil {
			return fmt.Errorf("via-chaos: POST /v1beta/injections: %w", err)
		}
		output.PrintJSON(map[string]any{
			"via_chaos":     true,
			"trace_id":      traceID,
			"task_id":       taskID,
			"injection_id":  strDeref(resp.Data.Id),
			"point_id":      pid,
			"capability":    cap,
			"status":        strDeref(resp.Data.Status),
		})
		return nil
	}

	batchKey := traceID + ":batch"
	children := make([]apiclient.ChaosChaosCreateBatchChildReq, 0, len(cfgs))
	pointIDs := make([]string, 0, len(cfgs))
	for i := range cfgs {
		pid, cap, _, err := guidedChaosPointID(cfgs[i], guidedChaosInstance, guidedChaosChartVersion)
		if err != nil {
			return fmt.Errorf("via-chaos: spec[%d]: %w", i, err)
		}
		params := guidedToChaosParams(cap, cfgs[i])
		childKey := fmt.Sprintf("%s:%d:%s", traceID, i, pid)
		entry := apiclient.ChaosChaosCreateBatchChildReq{
			PointId:        &pid,
			IdempotencyKey: &childKey,
			Params:         params,
			CallerMetadata: makeMeta(),
		}
		children = append(children, entry)
		pointIDs = append(pointIDs, pid)
	}
	body := *apiclient.NewChaosChaosCreateInjectionBatchReq(batchKey, children)
	body.BatchCallerMetadata = makeMeta()
	resp, _, err := cli.ChaosAPI.ChaosCreateInjectionBatch(ctx).
		ChaosChaosCreateInjectionBatchReq(body).Execute()
	if err != nil {
		return fmt.Errorf("via-chaos: POST /v1beta/injection-batches: %w", err)
	}
	output.PrintJSON(map[string]any{
		"via_chaos":         true,
		"trace_id":          traceID,
		"task_id":           taskID,
		"batch_id":          strDeref(resp.Data.Id),
		"point_ids":         pointIDs,
		"aggregated_status": strDeref(resp.Data.AggregatedStatus),
		"children":          len(resp.Data.Children),
	})
	return nil
}
