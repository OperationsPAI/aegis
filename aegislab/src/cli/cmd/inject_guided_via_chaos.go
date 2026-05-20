package cmd

import (
	"fmt"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"
	chaoscrud "aegis/crud/chaos"
	"aegis/platform/consts"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/google/uuid"
)

// Alias to the canonical chaos_type -> capability map in crud/chaos so the
// backend preflight and aegisctl address the same Point. Do not mutate from
// the CLI side: the backing map is shared by reference.
var chaosTypeToCapability = chaoscrud.ChaosTypeToCapability

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
//
// target.namespace is the LOGICAL system name (catalog identifier, stable
// across pool-allocated cluster topology) — NOT the concrete kubernetes
// namespace. The concrete ns travels separately in the request body and
// is what the executor uses for CR placement.
func guidedToChaosTarget(capability string, cfg guidedcli.GuidedConfig) (map[string]any, error) {
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
		"jvm_mysql_exception", "jvm_mysql_latency", "jvm_runtime_mutator":
		class := strings.TrimSpace(cfg.Class)
		method := strings.TrimSpace(cfg.Method)
		if class == "" || method == "" {
			return nil, fmt.Errorf("via-chaos: capability %s requires class and method", capability)
		}
		return map[string]any{"namespace": system, "app": app, "class": class, "method": method}, nil
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
// service. Singleton specs go to /v1beta/injections; multi-spec batches go
// to /v1beta/injection-batches. caller_metadata carries the campaign-side
// linkage the backend hook receiver needs to rejoin the state machine.
func submitGuidedViaChaos(cfgs []guidedcli.GuidedConfig, opts guidedApplyOptions) error {
	if len(cfgs) == 0 {
		return usageErrorf("no guided config to apply")
	}

	// Resolve the benchmark container version to its real ImageRef so the
	// downstream BuildDatapack pod doesn't try to pull "<name>:<tag>" against
	// docker.io. Legacy POST /v2/injections does this server-side; --via-chaos
	// has no server-side resolution, so we do it client-side via the same
	// ContainersAPI the `container version describe` command uses.
	benchmark, err := resolveBenchmarkImageRef(opts.BenchmarkName, opts.BenchmarkTag)
	if err != nil {
		return fmt.Errorf("via-chaos: resolve benchmark image_ref: %w", err)
	}

	traceID := uuid.NewString()
	taskID := uuid.NewString()
	makeMeta := func(namespace string) map[string]any {
		// CallerMetadata.Benchmark is *dto.ContainerVersionItem; Datapack is
		// stubbed with just `name` so the receiver's name-key resolve works.
		return map[string]any{
			"task_id":    taskID,
			"trace_id":   traceID,
			"project_id": opts.ProjectID,
			"benchmark": map[string]any{
				"id":             benchmark.ID,
				"name":           benchmark.Name,
				"image_ref":      benchmark.ImageRef,
				"command":        benchmark.Command,
				"container_name": benchmark.Name,
			},
			"datapack": map[string]any{
				"name":         taskID,
				"pre_duration": opts.PreDuration,
			},
			"pedestal":     opts.PedestalName,
			"pre_duration": opts.PreDuration,
			"namespace":    namespace,
		}
	}

	chaosURL := flagChaosServer

	if len(cfgs) == 1 {
		pid, cap, target, err := guidedChaosPointID(cfgs[0], guidedChaosInstance, guidedChaosChartVersion)
		if err != nil {
			return err
		}
		requestNS := strings.TrimSpace(cfgs[0].Namespace)
		if requestNS == "" {
			return fmt.Errorf("via-chaos: namespace is required (concrete cluster ns where the CR is applied)")
		}
		params := guidedToChaosParams(cap, cfgs[0])
		idemKey := traceID + ":" + pid
		if flagDryRun {
			output.PrintJSON(map[string]any{
				"dry_run":   true,
				"via_chaos": true,
				"method":    "POST",
				"url":       strings.TrimRight(chaosURL, "/") + "/v1beta/injections",
				"body": map[string]any{
					"point_id":        pid,
					"namespace":       requestNS,
					"idempotency_key": idemKey,
					"params":          params,
					"caller_metadata": makeMeta(requestNS),
				},
				"derived": map[string]any{
					"capability": cap,
					"target":     target,
					"trace_id":   traceID,
					"task_id":    taskID,
				},
			})
			return nil
		}
		cli, ctx, err := newChaosAPIClient()
		if err != nil {
			return err
		}
		body := *apiclient.NewChaosChaosCreateInjectionReq()
		body.PointId = &pid
		body.IdempotencyKey = &idemKey
		body.Params = params
		body.CallerMetadata = makeMeta(requestNS)
		body.AdditionalProperties = map[string]any{"namespace": requestNS}
		resp, _, err := cli.ChaosAPI.ChaosCreateInjection(ctx).
			ChaosChaosCreateInjectionReq(body).Execute()
		if err != nil {
			return fmt.Errorf("via-chaos: POST /v1beta/injections: %w", err)
		}
		output.PrintJSON(map[string]any{
			"via_chaos":    true,
			"trace_id":     traceID,
			"task_id":      taskID,
			"injection_id": strDeref(resp.Data.Id),
			"point_id":     pid,
			"capability":   cap,
			"status":       strDeref(resp.Data.Status),
		})
		return nil
	}

	batchKey := traceID + ":batch"
	children := make([]apiclient.ChaosChaosCreateBatchChildReq, 0, len(cfgs))
	pointIDs := make([]string, 0, len(cfgs))
	dryChildren := make([]map[string]any, 0, len(cfgs))
	for i := range cfgs {
		pid, cap, target, err := guidedChaosPointID(cfgs[i], guidedChaosInstance, guidedChaosChartVersion)
		if err != nil {
			return fmt.Errorf("via-chaos: spec[%d]: %w", i, err)
		}
		requestNS := strings.TrimSpace(cfgs[i].Namespace)
		if requestNS == "" {
			return fmt.Errorf("via-chaos: spec[%d]: namespace is required (concrete cluster ns where the CR is applied)", i)
		}
		params := guidedToChaosParams(cap, cfgs[i])
		childKey := fmt.Sprintf("%s:%d:%s", traceID, i, pid)
		entry := apiclient.ChaosChaosCreateBatchChildReq{
			PointId:              &pid,
			IdempotencyKey:       &childKey,
			Params:               params,
			CallerMetadata:       makeMeta(requestNS),
			AdditionalProperties: map[string]any{"namespace": requestNS},
		}
		children = append(children, entry)
		pointIDs = append(pointIDs, pid)
		dryChildren = append(dryChildren, map[string]any{
			"point_id":        pid,
			"namespace":       requestNS,
			"capability":      cap,
			"target":          target,
			"params":          params,
			"idempotency_key": childKey,
		})
	}
	if flagDryRun {
		output.PrintJSON(map[string]any{
			"dry_run":   true,
			"via_chaos": true,
			"method":    "POST",
			"url":       strings.TrimRight(chaosURL, "/") + "/v1beta/injection-batches",
			"body": map[string]any{
				"idempotency_key":       batchKey,
				"batch_caller_metadata": makeMeta(""),
				"children":              dryChildren,
			},
			"derived": map[string]any{
				"trace_id": traceID,
				"task_id":  taskID,
			},
		})
		return nil
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	body := *apiclient.NewChaosChaosCreateInjectionBatchReq(batchKey, children)
	body.BatchCallerMetadata = makeMeta("")
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

// resolvedBenchmark carries the container_version fields downstream
// BuildDatapack needs to render a runnable pod spec. Built by --via-chaos
// since there's no server-side resolution on that path.
type resolvedBenchmark struct {
	ID       int
	Name     string
	ImageRef string
	Command  string
}

// resolveBenchmarkImageRef looks up the container_version row keyed by
// (container name, version tag) and returns its ImageRef + Command. Required
// by the --via-chaos path because the locally-built caller_metadata.benchmark
// feeds the downstream BuildDatapack pod spec, and that pod can only pull
// from the registry the operator wired up — a bare "<name>:<tag>" attempts
// docker.io which is unreachable from byte-cluster, and an empty Command
// makes containerd refuse to start the pod (no entrypoint).
func resolveBenchmarkImageRef(name, tag string) (resolvedBenchmark, error) {
	var empty resolvedBenchmark
	if err := requireAPIContext(true); err != nil {
		return empty, err
	}
	r := newResolver()
	cid, _, err := r.ContainerIDOrName(name)
	if err != nil {
		return empty, fmt.Errorf("container %q not found: %w", name, err)
	}
	cli, ctx := newAPIClient()
	ctrResp, _, err := cli.ContainersAPI.GetContainerById(ctx, int32(cid)).Execute()
	if err != nil {
		return empty, fmt.Errorf("fetch container detail: %w", err)
	}
	ctrData := ctrResp.GetData()
	versions := apiVersionsToLocal(ctrData.GetVersions())
	vid, err := resolveContainerVersionID(versions, tag)
	if err != nil {
		return empty, fmt.Errorf("version %q not found on container %q: %w", tag, name, err)
	}
	vResp, _, err := cli.ContainersAPI.GetContainerVersionById(ctx, int32(cid), int32(vid)).Execute()
	if err != nil {
		return empty, fmt.Errorf("fetch container version: %w", err)
	}
	vData := vResp.GetData()
	ref := strings.TrimSpace(vData.GetImageRef())
	if ref == "" {
		return empty, fmt.Errorf("container version %s:%s has empty image_ref", name, tag)
	}
	return resolvedBenchmark{
		ID:       vid,
		Name:     name,
		ImageRef: ref,
		Command:  strings.TrimSpace(vData.GetCommand()),
	}, nil
}
