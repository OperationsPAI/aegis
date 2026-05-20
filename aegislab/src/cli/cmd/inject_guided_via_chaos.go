package cmd

import (
	"fmt"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"
	chaoscrud "aegis/crud/chaos"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/google/uuid"
)

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
		pid, cap, target, err := chaoscrud.GuidedChaosPointID(cfgs[0], guidedChaosInstance, guidedChaosChartVersion)
		if err != nil {
			return err
		}
		requestNS := strings.TrimSpace(cfgs[0].Namespace)
		if requestNS == "" {
			return fmt.Errorf("via-chaos: namespace is required (concrete cluster ns where the CR is applied)")
		}
		params := chaoscrud.GuidedToChaosParams(cap, cfgs[0])
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
		pid, cap, target, err := chaoscrud.GuidedChaosPointID(cfgs[i], guidedChaosInstance, guidedChaosChartVersion)
		if err != nil {
			return fmt.Errorf("via-chaos: spec[%d]: %w", i, err)
		}
		requestNS := strings.TrimSpace(cfgs[i].Namespace)
		if requestNS == "" {
			return fmt.Errorf("via-chaos: spec[%d]: namespace is required (concrete cluster ns where the CR is applied)", i)
		}
		params := chaoscrud.GuidedToChaosParams(cap, cfgs[i])
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
