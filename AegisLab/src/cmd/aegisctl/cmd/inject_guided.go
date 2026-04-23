package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Flags for `aegisctl inject guided`.
var (
	guidedCfgPath       string
	guidedResetConfig   bool
	guidedNoSaveConfig  bool
	guidedNamespace     string
	guidedSystem        string
	guidedSystemType    string
	guidedApp           string
	guidedChaosType     string
	guidedContainer     string
	guidedTargetService string
	guidedDomain        string
	guidedClass         string
	guidedMethod        string
	guidedMutatorConfig string
	guidedRoute         string
	guidedHTTPMethod    string
	guidedDatabase      string
	guidedTable         string
	guidedOperation     string
	guidedDirection     string
	guidedReturnType    string
	guidedReturnOpt     string
	guidedExceptionOpt  string
	guidedMemType       string
	guidedBodyType      string
	guidedReplaceMethod string
	guidedNext          string
	guidedOutput        string
	guidedApply         bool
	guidedSkipStaleCheck bool

	guidedDuration        int
	guidedMemorySize      int
	guidedMemWorker       int
	guidedTimeOffset      int
	guidedCPULoad         int
	guidedCPUWorker       int
	guidedLatency         int
	guidedCorrelation     int
	guidedJitter          int
	guidedLoss            int
	guidedDuplicate       int
	guidedCorrupt         int
	guidedRate            int
	guidedLimit           int
	guidedBuffer          int
	guidedDelayDuration   int
	guidedLatencyDuration int
	guidedLatencyMs       int
	guidedCPUCount        int
	guidedStatusCode      int

	// --apply envelope flags: mirror the injection YAML contract so a finished
	// guided session can be shipped to /inject with a single invocation.
	guidedApplyPedestalName  string
	guidedApplyPedestalTag   string
	guidedApplyBenchmarkName string
	guidedApplyBenchmarkTag  string
	guidedApplyInterval      int
	guidedApplyPreDuration   int
)

// injectGuidedCmd is the AegisLab-aware wrapper around the chaos-experiment
// guided session model. It mirrors `chaos-exp`'s interactive stepper and, on
// --apply, POSTs a GuidedConfig envelope to /inject. This is the only
// supported submission path: the backend's SubmitInjectionReq accepts guided
// configs exclusively.
var injectGuidedCmd = &cobra.Command{
	Use:   "guided",
	Short: "Step through a guided fault-injection session (AI-friendly, enum-driven)",
	Args:  requireNoArgs,
	Long: `Step through a guided fault-injection session backed by chaos-experiment's
pkg/guidedcli. Each invocation returns a GuidedResponse describing the next
field to fill, with its allowed values, until the config is ready to apply.

The session state is persisted to --config (default ~/.aegisctl/inject-guided.yaml)
so you can resume. Use --reset-config to start over, --next VALUE to apply the
current stage's selection, and --apply to submit the finalized config.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		path := guidedCfgPath
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("determine home directory: %w", err)
			}
			path = home + "/.aegisctl/inject-guided.yaml"
		}

		fileCfg, err := guidedcli.LoadConfig(path)
		if err != nil {
			return fmt.Errorf("load guided config: %w", err)
		}
		if guidedResetConfig {
			fileCfg.GuidedSession = guidedcli.GuidedSession{}
		}
		effectiveSave := !guidedNoSaveConfig

		cliCfg := guidedcli.GuidedConfig{
			System:         guidedSystem,
			SystemType:     guidedSystemType,
			Namespace:      guidedNamespace,
			App:            guidedApp,
			ChaosType:      guidedChaosType,
			Container:      guidedContainer,
			TargetService:  guidedTargetService,
			Domain:         guidedDomain,
			Class:          guidedClass,
			Method:         guidedMethod,
			MutatorConfig:  guidedMutatorConfig,
			Route:          guidedRoute,
			HTTPMethod:     guidedHTTPMethod,
			Database:       guidedDatabase,
			Table:          guidedTable,
			Operation:      guidedOperation,
			Direction:      guidedDirection,
			ReturnType:     guidedReturnType,
			ReturnValueOpt: guidedReturnOpt,
			ExceptionOpt:   guidedExceptionOpt,
			MemType:        guidedMemType,
			BodyType:       guidedBodyType,
			ReplaceMethod:  guidedReplaceMethod,
			// Apply is intentionally left false for the local guidedcli.Resolve
			// call below: the aegisctl CLI submits via the backend /inject
			// endpoint (see submitGuidedApply), and we do not want the local
			// resolver to run handler.BatchCreate against the caller's
			// kubeconfig. Doing so would emit misleading "namespaces not found"
			// errors while the real execution happens server-side (issue #132).
			Apply:      false,
			SaveConfig: effectiveSave,
			ResetConfig:    guidedResetConfig,
		}

		setInt := func(dst **int, v int, allowZero bool) {
			if allowZero || v != 0 {
				tmp := v
				*dst = &tmp
			}
		}
		setInt(&cliCfg.Duration, guidedDuration, false)
		setInt(&cliCfg.MemorySize, guidedMemorySize, false)
		setInt(&cliCfg.MemWorker, guidedMemWorker, false)
		setInt(&cliCfg.TimeOffset, guidedTimeOffset, true)
		setInt(&cliCfg.CPULoad, guidedCPULoad, false)
		setInt(&cliCfg.CPUWorker, guidedCPUWorker, false)
		setInt(&cliCfg.Latency, guidedLatency, false)
		setInt(&cliCfg.Correlation, guidedCorrelation, false)
		setInt(&cliCfg.Jitter, guidedJitter, false)
		setInt(&cliCfg.Loss, guidedLoss, false)
		setInt(&cliCfg.Duplicate, guidedDuplicate, false)
		setInt(&cliCfg.Corrupt, guidedCorrupt, false)
		setInt(&cliCfg.Rate, guidedRate, false)
		setInt(&cliCfg.Limit, guidedLimit, false)
		setInt(&cliCfg.Buffer, guidedBuffer, false)
		setInt(&cliCfg.DelayDuration, guidedDelayDuration, false)
		setInt(&cliCfg.LatencyDuration, guidedLatencyDuration, false)
		setInt(&cliCfg.LatencyMs, guidedLatencyMs, false)
		setInt(&cliCfg.CPUCount, guidedCPUCount, false)
		setInt(&cliCfg.StatusCode, guidedStatusCode, false)

		merged := guidedcli.MergeConfig(fileCfg, cliCfg)
		// Suppress any Apply=true inherited from the persisted session — see
		// the Apply comment above; apply happens exclusively via the backend
		// submit path.
		merged.Apply = false
		if guidedNext != "" {
			current, err := guidedcli.Resolve(ctx, merged)
			if err != nil {
				return fmt.Errorf("resolve current guided response: %w", err)
			}
			merged, err = guidedcli.ApplyNextSelection(current, guidedNext)
			if err != nil {
				return fmt.Errorf("apply --next: %w", err)
			}
			merged.SaveConfig = effectiveSave
			merged.ResetConfig = guidedResetConfig
			merged.Apply = false
		}

		response, err := guidedcli.Resolve(ctx, merged)
		if err != nil {
			return fmt.Errorf("resolve guided response: %w", err)
		}

		if effectiveSave {
			if err := guidedcli.SaveConfig(path, fileCfg, response.Config); err != nil {
				return fmt.Errorf("save guided config: %w", err)
			}
		}

		if guidedApply {
			return submitGuidedApply(response.Config)
		}

		switch guidedOutput {
		case "", "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(response); err != nil {
				return fmt.Errorf("encode json response: %w", err)
			}
		case "yaml":
			data, err := yaml.Marshal(response)
			if err != nil {
				return fmt.Errorf("encode yaml response: %w", err)
			}
			fmt.Fprint(os.Stdout, string(data))
		default:
			return fmt.Errorf("unsupported output format %q", guidedOutput)
		}
		return nil
	},
}

func init() {
	f := injectGuidedCmd.Flags()
	f.StringVar(&guidedCfgPath, "config", "", "Path to guided session YAML (default ~/.aegisctl/inject-guided.yaml)")
	f.BoolVar(&guidedResetConfig, "reset-config", false, "Discard the saved guided session before resolving")
	f.BoolVar(&guidedNoSaveConfig, "no-save-config", false, "Do not persist the session snapshot")
	f.StringVar(&guidedNamespace, "namespace", "", "Target namespace (e.g. ts, otel-demo0)")
	f.StringVar(&guidedSystem, "system", "", "System namespace instance (e.g. ts0)")
	f.StringVar(&guidedSystemType, "system-type", "", "System type identifier (overrides inferred type)")
	f.StringVar(&guidedApp, "app", "", "App label selection")
	f.StringVar(&guidedChaosType, "chaos-type", "", "Chaos type (e.g. PodFailure, CPUStress, NetworkDelay)")
	f.StringVar(&guidedContainer, "container", "", "Container name selection")
	f.StringVar(&guidedTargetService, "target-service", "", "Target service for network chaos")
	f.StringVar(&guidedDomain, "domain", "", "Domain for DNS chaos")
	f.StringVar(&guidedClass, "class", "", "JVM class name")
	f.StringVar(&guidedMethod, "method", "", "JVM method name")
	f.StringVar(&guidedMutatorConfig, "mutator-config", "", "Runtime mutator config key")
	f.StringVar(&guidedRoute, "route", "", "HTTP route selection")
	f.StringVar(&guidedHTTPMethod, "http-method", "", "HTTP method selection")
	f.StringVar(&guidedDatabase, "database", "", "Database name")
	f.StringVar(&guidedTable, "table", "", "Database table")
	f.StringVar(&guidedOperation, "operation", "", "Database operation")
	f.StringVar(&guidedDirection, "direction", "", "Network direction: to|from|both")
	f.StringVar(&guidedReturnType, "return-type", "", "JVM return type: string|int")
	f.StringVar(&guidedReturnOpt, "return-value-opt", "", "JVM return value option: default|random")
	f.StringVar(&guidedExceptionOpt, "exception-opt", "", "JVM exception option: default|random")
	f.StringVar(&guidedMemType, "mem-type", "", "Memory stress type: heap|stack")
	f.StringVar(&guidedBodyType, "body-type", "", "HTTP body type: empty|random")
	f.StringVar(&guidedReplaceMethod, "replace-method", "", "HTTP method to use as replacement")
	f.StringVar(&guidedNext, "next", "", "Apply a single next-step selection using the current session state")
	f.StringVar(&guidedOutput, "output", "json", "Output format: json|yaml")
	f.BoolVar(&guidedApply, "apply", false, "Finalize the session and attempt to submit")
	f.BoolVar(&guidedSkipStaleCheck, "skip-stale-check", false, "Skip the pre-submit warning about orphaned PodChaos CRs in the target namespace")

	// --apply envelope flags (mirror the injection YAML contract)
	f.StringVar(&guidedApplyPedestalName, "pedestal-name", "", "Pedestal container name (required with --apply)")
	f.StringVar(&guidedApplyPedestalTag, "pedestal-tag", "", "Pedestal container version/tag (required with --apply)")
	f.StringVar(&guidedApplyBenchmarkName, "benchmark-name", "", "Benchmark container name (required with --apply)")
	f.StringVar(&guidedApplyBenchmarkTag, "benchmark-tag", "", "Benchmark container version/tag (required with --apply)")
	f.IntVar(&guidedApplyInterval, "interval", 0, "Total experiment interval in minutes (required with --apply)")
	f.IntVar(&guidedApplyPreDuration, "pre-duration", 0, "Normal-data collection duration in minutes (required with --apply)")

	f.IntVar(&guidedDuration, "duration", 0, "Duration in minutes (default 5)")
	f.IntVar(&guidedMemorySize, "memory-size", 0, "Memory size in MiB")
	f.IntVar(&guidedMemWorker, "mem-worker", 0, "Memory stress worker count")
	f.IntVar(&guidedTimeOffset, "time-offset", 0, "Time offset in seconds")
	f.IntVar(&guidedCPULoad, "cpu-load", 0, "CPU load percentage")
	f.IntVar(&guidedCPUWorker, "cpu-worker", 0, "CPU worker count")
	f.IntVar(&guidedLatency, "latency", 0, "Network latency in milliseconds")
	f.IntVar(&guidedCorrelation, "correlation", 0, "Correlation percentage")
	f.IntVar(&guidedJitter, "jitter", 0, "Jitter in milliseconds")
	f.IntVar(&guidedLoss, "loss", 0, "Packet loss percentage")
	f.IntVar(&guidedDuplicate, "duplicate", 0, "Packet duplication percentage")
	f.IntVar(&guidedCorrupt, "corrupt", 0, "Packet corruption percentage")
	f.IntVar(&guidedRate, "rate", 0, "Bandwidth rate in kbps")
	f.IntVar(&guidedLimit, "limit", 0, "Bandwidth limit bytes")
	f.IntVar(&guidedBuffer, "buffer", 0, "Bandwidth buffer bytes")
	f.IntVar(&guidedDelayDuration, "delay-duration", 0, "HTTP delay duration in milliseconds")
	f.IntVar(&guidedLatencyDuration, "latency-duration", 0, "JVM latency duration in milliseconds")
	f.IntVar(&guidedLatencyMs, "latency-ms", 0, "Database latency in milliseconds")
	f.IntVar(&guidedCPUCount, "cpu-count", 0, "JVM CPU core count")
	f.IntVar(&guidedStatusCode, "status-code", 0, "HTTP status code")

	injectCmd.AddCommand(injectGuidedCmd)
}

// submitGuidedApply wraps a finalized GuidedConfig in the SubmitInjectionReq
// envelope expected by POST /api/v2/projects/{id}/injections/inject and
// forwards it through the guided-only backend path.
func submitGuidedApply(cfg guidedcli.GuidedConfig) error {
	// Validate required envelope flags up front so the user gets a clear
	// message instead of a 400 from the backend.
	if guidedApplyPedestalName == "" || guidedApplyPedestalTag == "" || guidedApplyBenchmarkName == "" || guidedApplyBenchmarkTag == "" {
		return usageErrorf("--apply requires --pedestal-name, --pedestal-tag, --benchmark-name, and --benchmark-tag")
	}
	if guidedApplyInterval <= 0 || guidedApplyPreDuration <= 0 {
		return usageErrorf("--apply requires --interval and --pre-duration (positive minutes)")
	}
	if guidedApplyInterval <= guidedApplyPreDuration {
		return usageErrorf("--interval must be greater than --pre-duration")
	}
	if err := requireAPIContext(true); err != nil {
		return err
	}
	if !guidedSkipStaleCheck {
		// Non-blocking: any error from the check itself is silently downgraded
		// to an info line by warnStalePodChaos. We still ignore its return.
		_ = warnStalePodChaos(context.Background(), cfg.Namespace, guidedStalePodChaosListerHook, os.Stderr)
	}
	opts := guidedApplyOptions{
		PedestalName:  guidedApplyPedestalName,
		PedestalTag:   guidedApplyPedestalTag,
		BenchmarkName: guidedApplyBenchmarkName,
		BenchmarkTag:  guidedApplyBenchmarkTag,
		Interval:      guidedApplyInterval,
		PreDuration:   guidedApplyPreDuration,
	}
	pid, err := resolveProjectIDForApply(flagProject)
	if err != nil {
		return err
	}
	envelope := map[string]any{
		"pedestal": map[string]any{
			"name":    opts.PedestalName,
			"version": opts.PedestalTag,
		},
		"benchmark": map[string]any{
			"name":    opts.BenchmarkName,
			"version": opts.BenchmarkTag,
		},
		"interval":     opts.Interval,
		"pre_duration": opts.PreDuration,
		"specs":        [][]guidedcli.GuidedConfig{{cfg}},
	}
	if flagDryRun {
		output.PrintJSON(map[string]any{
			"dry_run":    true,
			"operation":  "inject_guided_apply",
			"project":    flagProject,
			"project_id": pid,
			"method":     "POST",
			"path":       fmt.Sprintf("/api/v2/projects/%d/injections/inject", pid),
			"spec":       envelope,
		})
		return nil
	}
	resp, err := submitGuidedApplyWithOptions(flagProject, cfg, opts)
	if err != nil {
		return err
	}
	if resp.Data.IsDedupedAll() {
		summary := resp.Data.DedupeSummary()
		output.PrintInfo(summary)
		// Still emit the raw response envelope so scripts that captured stdout
		// before we tightened the contract can inspect warnings.
		output.PrintJSON(resp.Data)
		return newDedupeSuppressedError(summary)
	}
	output.PrintJSON(resp.Data)
	return nil
}

type guidedApplyOptions struct {
	PedestalName  string
	PedestalTag   string
	BenchmarkName string
	BenchmarkTag  string
	Interval      int
	PreDuration   int
}

func submitGuidedApplyWithOptions(projectName string, cfg guidedcli.GuidedConfig, opts guidedApplyOptions) (*client.APIResponse[injectSubmitResponse], error) {
	if opts.PedestalName == "" || opts.PedestalTag == "" || opts.BenchmarkName == "" || opts.BenchmarkTag == "" {
		return nil, fmt.Errorf("--apply requires --pedestal-name, --pedestal-tag, --benchmark-name, and --benchmark-tag")
	}
	if opts.Interval <= 0 || opts.PreDuration <= 0 {
		return nil, fmt.Errorf("--apply requires --interval and --pre-duration (positive minutes)")
	}
	if opts.Interval <= opts.PreDuration {
		return nil, fmt.Errorf("--interval must be greater than --pre-duration")
	}

	pid, err := resolveProjectIDForApply(projectName)
	if err != nil {
		return nil, err
	}

	envelope := map[string]any{
		"pedestal": map[string]any{
			"name":    opts.PedestalName,
			"version": opts.PedestalTag,
		},
		"benchmark": map[string]any{
			"name":    opts.BenchmarkName,
			"version": opts.BenchmarkTag,
		},
		"interval":     opts.Interval,
		"pre_duration": opts.PreDuration,
		"specs":        [][]guidedcli.GuidedConfig{{cfg}},
	}

	c := newClient()
	var resp client.APIResponse[injectSubmitResponse]
	if err := c.Post(fmt.Sprintf("/api/v2/projects/%d/injections/inject", pid), envelope, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func resolveProjectIDForApply(projectName string) (int, error) {
	if projectName == "" {
		return resolveProjectIDByName()
	}
	return newResolver().ProjectID(projectName)
}
