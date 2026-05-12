package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

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

	// Issue #138: install-aware guided. --install bootstraps a missing
	// workload before app discovery so the first guided invocation works on
	// an empty namespace. Restart on apply is still scheduled by the
	// backend's RestartPedestal task (default-on); --skip-restart-pedestal
	// just threads the existing no-op hint through the submit envelope.
	guidedInstall               bool
	guidedInstallReadyTimeoutSec int
	guidedSkipRestartPedestal   bool

	// #166: --auto asks the server to pick a free deployed namespace from
	// the system's pool at submit time, instead of the user pre-naming one.
	// Mutually exclusive with --namespace. The chosen ns is surfaced in the
	// submit response so scripts can read it back without parsing trace
	// logs. Honors only the explicit-flag path; saved session configs that
	// already have `namespace` set keep their value unless --auto is passed
	// (in which case the namespace is cleared just for this apply).
	guidedAutoAllocate bool

	// #166 PR-C: pair with --auto to let the server bootstrap a fresh pool
	// slot (helm install workload + bump system count) when every existing
	// slot is occupied. No-op without --auto.
	guidedAllowBootstrap bool

	// Test seams: replace with fakes in unit tests.
	guidedInstallerHook chartInstaller
	guidedPodListerHook PodLister

	// Multi-fault batch staging (mirrors backend SubmitInjectionReq.Specs[i]
	// inner-array semantics: configs in one inner slice run in parallel).
	// --stage appends the current finalized cfg to a stage file and resets
	// the working session so the user can configure the next fault. --apply
	// --batch ships every staged cfg as one parallel batch.
	guidedStage      bool
	guidedBatch      bool
	guidedResetBatch bool
	guidedListBatch  bool
	guidedBatchPath  string

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
	// guided session can be shipped to /inject with a single invocation. The
	// fault-injection time windows (warmup, normal, abnormal, restart timeout)
	// are pinned server-side and intentionally not exposed here — see
	// consts.Fixed*.
	guidedApplyPedestalName  string
	guidedApplyPedestalTag   string
	guidedApplyBenchmarkName string
	guidedApplyBenchmarkTag  string
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

		// Multi-fault batch dispatch. --stage / --reset-batch / --list-batch
		// are mutually exclusive with each other, and --batch is only
		// meaningful in combination with --apply (= submit staged specs as
		// one parallel batch).
		batchModes := 0
		for _, f := range []bool{guidedStage, guidedResetBatch, guidedListBatch} {
			if f {
				batchModes++
			}
		}
		if batchModes > 1 {
			return usageErrorf("--stage, --reset-batch, and --list-batch are mutually exclusive")
		}
		if guidedBatch && !guidedApply && !guidedListBatch {
			return usageErrorf("--batch is only valid with --apply (or implicitly with --list-batch); pass --apply to submit staged specs")
		}
		if guidedStage && guidedApply {
			return usageErrorf("--stage and --apply are mutually exclusive: --stage saves the current cfg for later, --apply submits now")
		}

		batchPath, err := resolveGuidedBatchPath(guidedBatchPath)
		if err != nil {
			return err
		}

		if guidedResetBatch {
			if err := saveGuidedBatch(batchPath, &guidedBatchFile{Version: 1}); err != nil {
				return err
			}
			output.PrintInfo(fmt.Sprintf("cleared guided batch stage file %s", batchPath))
			return nil
		}

		if guidedListBatch {
			batch, err := loadGuidedBatch(batchPath)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(map[string]any{
				"path":         batchPath,
				"version":      batch.Version,
				"staged_count": len(batch.StagedSpecs),
				"staged_specs": batch.StagedSpecs,
			}); err != nil {
				return fmt.Errorf("encode batch listing: %w", err)
			}
			return nil
		}

		// --apply --batch short-circuits the working-session resolve loop:
		// the staged file is the source of truth.
		if guidedApply && guidedBatch {
			batch, err := loadGuidedBatch(batchPath)
			if err != nil {
				return err
			}
			if len(batch.StagedSpecs) == 0 {
				return usageErrorf("--apply --batch: no staged specs in %s; run `--stage` first to add at least one ready_to_apply config", batchPath)
			}
			if err := submitGuidedApply(batch.StagedSpecs...); err != nil {
				return err
			}
			// Submit succeeded (non-dedupe path returns nil; dedupe-suppressed
			// returns a sentinel that we let propagate). Clear the stage file
			// only on the clean-success path so a dedupe error keeps the
			// staged specs around for the user to re-inspect.
			if err := saveGuidedBatch(batchPath, &guidedBatchFile{Version: 1}); err != nil {
				return fmt.Errorf("submit ok but failed to clear batch stage file %s: %w", batchPath, err)
			}
			output.PrintInfo(fmt.Sprintf("cleared guided batch stage file %s after successful submit", batchPath))
			return nil
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
		// Per-spec duration is pinned server-side (consts.FixedAbnormalWindowSeconds);
		// we deliberately omit a CLI flag for it so loop agents can't drift it.
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

		// Issue #138: before any guidedcli.Resolve call (which performs
		// in-cluster app discovery via list-pods), optionally bootstrap the
		// target workload via `aegisctl pedestal chart install` when the
		// namespace is empty. Restart on apply is NOT performed here — it
		// remains the backend's RestartPedestal task, default-scheduled by
		// every submit. --install is a one-time bootstrap for the empty
		// first-run case; repeat runs skip it automatically because pods
		// already exist.
		if guidedInstall {
			if err := bootstrapGuidedInstall(ctx, merged); err != nil {
				return err
			}
		}

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

		if guidedStage {
			// Same fail-loud guard as --apply: only stage configs the local
			// resolver believes are submittable.
			if err := guidedResolveErr(response); err != nil {
				return err
			}
			batch, err := loadGuidedBatch(batchPath)
			if err != nil {
				return err
			}
			candidate := append(batch.StagedSpecs, response.Config)
			if err := validateBatchCompat(candidate); err != nil {
				return err
			}
			batch.StagedSpecs = candidate
			if err := saveGuidedBatch(batchPath, batch); err != nil {
				return err
			}
			// Reset the working session so the next invocation starts clean
			// for the next fault. Honors --no-save-config: when the user
			// asked us not to persist the session, leave the working file
			// untouched (they accept the responsibility of resetting it
			// themselves on the next call).
			if effectiveSave {
				fileCfg.GuidedSession = guidedcli.GuidedSession{}
				if err := guidedcli.SaveConfig(path, fileCfg, guidedcli.GuidedConfig{}); err != nil {
					return fmt.Errorf("reset working session after stage: %w", err)
				}
			}
			output.PrintInfo(fmt.Sprintf("staged spec #%d to %s; working session reset", len(candidate), batchPath))
			return nil
		}

		if guidedApply {
			// Issue #176: short-circuit before shipping an un-normalized
			// config to the backend when the local resolver reported errors.
			// Without this guard, finalizeOrRequest's failure path returns
			// cfg with Duration=nil (normalizeDuration only runs on success),
			// which the server's uniform validator then rejects with a
			// misleading "specs[0][0].duration must be greater than 0"
			// instead of the actual builder error (e.g. JVM method not
			// found, mem_type unset, etc.).
			if err := guidedResolveErr(response); err != nil {
				return err
			}
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

	// Issue #138 flags.
	f.BoolVar(&guidedInstall, "install", false, "Before app discovery, if --namespace has no pods, shell out to 'aegisctl pedestal chart install <system>' and wait for readiness (requires --system and --namespace)")
	f.IntVar(&guidedInstallReadyTimeoutSec, "install-ready-timeout", 600, "Seconds --install waits for pods in the target namespace to reach Ready before continuing with discovery")
	f.BoolVar(&guidedSkipRestartPedestal, "skip-restart-pedestal", false, "On --apply, hint the backend's RestartPedestal task to skip the helm install when the release is already healthy (task still runs; only the install step short-circuits)")
	f.BoolVar(&guidedAutoAllocate, "auto", false, "On --apply, ask the server to pick a free deployed namespace from the system's pool (mutually exclusive with --namespace; allocated namespace surfaces in submit response). See #166.")
	f.BoolVar(&guidedAllowBootstrap, "allow-bootstrap", false, "Pair with --auto: when no deployed slot is free, let the server bootstrap a fresh slot (helm install + bump system count) instead of failing with pool-exhausted. No-op without --auto. See #166 PR-C.")

	// --apply envelope flags (mirror the injection YAML contract)
	f.StringVar(&guidedApplyPedestalName, "pedestal-name", "", "Pedestal container name (required with --apply)")
	f.StringVar(&guidedApplyPedestalTag, "pedestal-tag", "", "Pedestal container version/tag (required with --apply)")
	f.StringVar(&guidedApplyBenchmarkName, "benchmark-name", "", "Benchmark container name (required with --apply)")
	f.StringVar(&guidedApplyBenchmarkTag, "benchmark-tag", "", "Benchmark container version/tag (required with --apply)")

	f.BoolVar(&guidedStage, "stage", false, "Append the current ready_to_apply config to the batch stage file and reset the working session (does not submit). Use with --apply --batch later to submit every staged spec as one parallel batch.")
	f.BoolVar(&guidedBatch, "batch", false, "With --apply, submit every staged spec from the batch file as a single parallel batch (cleared on success). Without --apply, used by --list-batch to point at the stage file.")
	f.BoolVar(&guidedResetBatch, "reset-batch", false, "Discard the batch stage file and exit (mutually exclusive with --apply / --stage / --list-batch)")
	f.BoolVar(&guidedListBatch, "list-batch", false, "Print the contents of the batch stage file as JSON and exit (mutually exclusive with --apply / --stage / --reset-batch)")
	f.StringVar(&guidedBatchPath, "batch-config", "", "Path to the batch stage file (default ~/.aegisctl/inject-guided-batch.yaml)")

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

// guidedResolveErr inspects a GuidedResponse for resolver errors or a
// non-final stage and returns a usage-coded error surfacing them to the user.
// Returns nil when the response is safe to submit.
//
// Background (issue #176): chaos-experiment's finalizeOrRequest returns the
// caller's config un-normalized (Duration still nil, etc.) whenever its
// per-type builder fails. If the CLI ships that config to /inject anyway,
// the server's uniform validator rejects it with a generic
// "specs[0][0].duration must be greater than 0" that hides the real builder
// error (missing JVM method, missing mem_type, decode failure, ...). We must
// fail fast on the client and surface every resolver error so the user can
// fix the actual root cause.
func guidedResolveErr(response *guidedcli.GuidedResponse) error {
	if response == nil {
		return usageErrorf("guided resolver returned no response")
	}
	if len(response.Errors) == 0 && (response.Stage == "" || response.Stage == "ready_to_apply" || response.Stage == "applied") {
		return nil
	}

	// Build a preamble that names what failed so the user knows which
	// (chaos_type, app, class, method, ...) tuple to fix.
	cfg := response.Config
	preamble := "guided resolver did not finalize the config"
	if strings.TrimSpace(cfg.ChaosType) != "" {
		preamble = fmt.Sprintf("guided resolver failed for chaos_type=%s", strings.TrimSpace(cfg.ChaosType))
		identifiers := make([]string, 0, 4)
		if v := strings.TrimSpace(cfg.App); v != "" {
			identifiers = append(identifiers, fmt.Sprintf("app=%s", v))
		}
		if v := strings.TrimSpace(cfg.Class); v != "" {
			identifiers = append(identifiers, fmt.Sprintf("class=%s", v))
		}
		if v := strings.TrimSpace(cfg.Method); v != "" {
			identifiers = append(identifiers, fmt.Sprintf("method=%s", v))
		}
		if v := strings.TrimSpace(cfg.Container); v != "" {
			identifiers = append(identifiers, fmt.Sprintf("container=%s", v))
		}
		if len(identifiers) > 0 {
			preamble = fmt.Sprintf("%s (%s)", preamble, strings.Join(identifiers, ", "))
		}
	}

	if len(response.Errors) == 0 {
		return usageErrorf("%s: stage=%q is not ready_to_apply (refusing to submit un-normalized config to /inject)",
			preamble, response.Stage)
	}

	// Preserve every resolver error so the user sees the full set, not just
	// the first one.
	if len(response.Errors) == 1 {
		return usageErrorf("%s: %s", preamble, response.Errors[0])
	}
	bullets := make([]string, 0, len(response.Errors))
	for _, e := range response.Errors {
		bullets = append(bullets, "  - "+e)
	}
	return usageErrorf("%s:\n%s", preamble, strings.Join(bullets, "\n"))
}

// submitGuidedApply wraps one or more finalized GuidedConfigs in the
// SubmitInjectionReq envelope expected by POST
// /api/v2/projects/{id}/injections/inject and forwards them through the
// guided-only backend path. When len(cfgs) > 1 the configs are sent as one
// inner specs slice so the backend treats them as a parallel batch (see
// SubmitInjectionReq.Specs semantics in module/injection/api_types.go).
func submitGuidedApply(cfgs ...guidedcli.GuidedConfig) error {
	if len(cfgs) == 0 {
		return usageErrorf("no guided config to apply")
	}
	// Validate required envelope flags up front so the user gets a clear
	// message instead of a 400 from the backend.
	if guidedApplyPedestalName == "" || guidedApplyPedestalTag == "" || guidedApplyBenchmarkName == "" || guidedApplyBenchmarkTag == "" {
		return usageErrorf("--apply requires --pedestal-name, --pedestal-tag, --benchmark-name, and --benchmark-tag")
	}
	if len(cfgs) > 1 {
		if err := validateBatchCompat(cfgs); err != nil {
			return err
		}
	}
	for i := range cfgs {
		if guidedAutoAllocate && strings.TrimSpace(cfgs[i].Namespace) != "" && strings.TrimSpace(guidedNamespace) != "" {
			// Only fire the conflict error when the user explicitly passed
			// --namespace alongside --auto. A namespace inherited from the
			// saved session config gets silently overridden below.
			return usageErrorf("--auto cannot be combined with --namespace; pick one")
		}
		if guidedAutoAllocate {
			cfgs[i].Namespace = ""
		}
	}
	if err := requireAPIContext(true); err != nil {
		return err
	}
	if !guidedSkipStaleCheck {
		// All staged cfgs share a namespace (validateBatchCompat enforced
		// it), so we only probe once.
		_ = warnStalePodChaos(context.Background(), cfgs[0].Namespace, guidedStalePodChaosListerHook, os.Stderr)
	}
	opts := guidedApplyOptions{
		PedestalName:  guidedApplyPedestalName,
		PedestalTag:   guidedApplyPedestalTag,
		BenchmarkName: guidedApplyBenchmarkName,
		BenchmarkTag:  guidedApplyBenchmarkTag,
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
		"specs": [][]guidedcli.GuidedConfig{cfgs},
	}
	if guidedSkipRestartPedestal {
		envelope["skip_restart_pedestal"] = true
	}
	if guidedAutoAllocate {
		envelope["auto_allocate"] = true
	}
	if guidedAllowBootstrap {
		envelope["allow_bootstrap"] = true
	}
	if flagDryRun {
		output.PrintJSON(map[string]any{
			"dry_run":    true,
			"operation":  "inject_guided_apply",
			"project":    flagProject,
			"project_id": pid,
			"method":     "POST",
			"path":       consts.APIPathProjectInjectionsInject(pid),
			"spec":       envelope,
		})
		return nil
	}
	resp, err := submitGuidedApplyWithOptions(flagProject, cfgs, opts)
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
}

func submitGuidedApplyWithOptions(projectName string, cfgs []guidedcli.GuidedConfig, opts guidedApplyOptions) (*client.APIResponse[injectSubmitResponse], error) {
	if len(cfgs) == 0 {
		return nil, fmt.Errorf("no guided config to apply")
	}
	if opts.PedestalName == "" || opts.PedestalTag == "" || opts.BenchmarkName == "" || opts.BenchmarkTag == "" {
		return nil, fmt.Errorf("--apply requires --pedestal-name, --pedestal-tag, --benchmark-name, and --benchmark-tag")
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
		"specs": [][]guidedcli.GuidedConfig{cfgs},
	}
	if guidedSkipRestartPedestal {
		envelope["skip_restart_pedestal"] = true
	}
	if guidedAutoAllocate {
		envelope["auto_allocate"] = true
	}
	if guidedAllowBootstrap {
		envelope["allow_bootstrap"] = true
	}

	c := newClient()
	var resp client.APIResponse[injectSubmitResponse]
	if err := c.Post(consts.APIPathProjectInjectionsInject(pid), envelope, &resp); err != nil {
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

// bootstrapGuidedInstall is the --install path: on an empty target namespace,
// shell out to `aegisctl pedestal chart install <system> --namespace <ns>` and
// wait for readiness before returning. If pods already exist it is a no-op,
// so repeated guided invocations stay cheap. It reuses the regression-preflight
// installer + pod-lister surfaces verbatim to avoid a second install path.
func bootstrapGuidedInstall(ctx context.Context, cfg guidedcli.GuidedConfig) error {
	if cfg.System == "" || cfg.Namespace == "" {
		return usageErrorf("--install requires both --system and --namespace so we know what chart to install and where")
	}
	lister := guidedPodListerHook
	if lister == nil {
		l, err := newLivePodLister()
		if err != nil {
			return fmt.Errorf("--install: build k8s client: %w", err)
		}
		lister = l
	}

	// Skip install if anything is already present in the namespace — guided
	// discovery will pick up whatever is there. This makes --install
	// idempotent across repeated invocations.
	if n, err := lister.ListPods(ctx, cfg.Namespace, ""); err != nil {
		return fmt.Errorf("--install: probe namespace %q: %w", cfg.Namespace, err)
	} else if n > 0 {
		return nil
	}

	installer := guidedInstallerHook
	if installer == nil {
		installer = defaultChartInstaller
	}
	output.PrintInfo(fmt.Sprintf("--install: namespace %q is empty; installing chart for system %q", cfg.Namespace, cfg.System))
	if err := installer(ctx, cfg.System, cfg.Namespace); err != nil {
		return fmt.Errorf("--install: chart install failed for system=%s namespace=%s: %w", cfg.System, cfg.Namespace, err)
	}

	timeoutSec := guidedInstallReadyTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		total, ready, err := lister.CountReadyPods(ctx, cfg.Namespace, "")
		if err != nil {
			return fmt.Errorf("--install: wait-for-ready ns=%s: %w", cfg.Namespace, err)
		}
		if total > 0 && ready == total {
			output.PrintInfo(fmt.Sprintf("--install: ns=%s ready (%d/%d)", cfg.Namespace, ready, total))
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("--install: timed out after %ds waiting for pods in ns=%s (ready %d/%d); bump --install-ready-timeout or inspect with `kubectl -n %s get pods`",
				timeoutSec, cfg.Namespace, ready, total, cfg.Namespace)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
