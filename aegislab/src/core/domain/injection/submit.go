package injection

import (
	"aegis/platform/dto"
	"aegis/platform/k8s"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	guidedcli "aegis/platform/chaos"
	"github.com/sirupsen/logrus"
)

// ensureGuidedNamespaces creates any referenced namespace that doesn't exist
// yet, so that the guided-build submit-time pod listing can find the (empty)
// namespace instead of failing with `namespaces "X" not found`. RestartPedestal
// will helm-install workloads into the namespace moments later; creating it
// early is harmless. First-run only: existing namespaces are left alone.
// Errors here are warnings, not fatal — if the cluster genuinely rejects the
// create, the subsequent BuildInjection will fail and report that instead.
//
// Also bumps the system's `Count` (via chaosSystems.EnsureCountForNamespace)
// so the namespace is registered in `config.GetAllNamespaces()` — without
// this, AcquireLock would later reject `sockshop14` with "not found in
// current configuration", which is the deeper root cause of #156:
// `aegisctl inject guided --install --namespace sockshop14` had been
// creating the workload at the k8s level but leaving the chaos-system
// count at 1, so the runtime always fell back to the NsPattern pool
// (sockshop0). Bumping count here makes any submit path — `--install`,
// pre-installed, or scripted — register the requested namespace
// idempotently. A bump failure is fatal: silent count mismatches are
// exactly what produced the original silent-fallback bug.
func ensureGuidedNamespaces(ctx context.Context, system string, configs []guidedcli.GuidedConfig, chaosSystems ChaosSystemWriter) error {
	gw := k8s.NewGateway(nil)
	seen := make(map[string]struct{}, len(configs))
	for _, cfg := range configs {
		ns := strings.TrimSpace(cfg.Namespace)
		if ns == "" {
			continue
		}
		if _, dup := seen[ns]; dup {
			continue
		}
		seen[ns] = struct{}{}

		created, err := gw.EnsureNamespace(ctx, ns)
		if err != nil {
			logrus.Warnf("submit: could not ensure namespace %q exists (will let BuildInjection surface the real error): %v", ns, err)
			// Still try to register the count below: even if k8s
			// EnsureNamespace failed transiently, the user's intent was
			// clear and a count bump is harmless on its own.
		} else if created {
			logrus.Infof("submit: created namespace %q for guided submit (first-run bootstrap)", ns)
		}

		if chaosSystems == nil {
			// Tests construct injection.Service with a nil writer; skip
			// silently rather than panicking. Production wiring (fx) always
			// supplies the real Writer.
			continue
		}
		bumped, bumpErr := chaosSystems.EnsureCountForNamespace(ctx, system, ns)
		if bumpErr != nil {
			return fmt.Errorf("register namespace %q with system %s: %w", ns, system, bumpErr)
		}
		if bumped {
			logrus.Infof("submit: bumped chaos-system %s count to register namespace %q", system, ns)
		}
	}
	return nil
}

// mergeSpecServicesForDupCheck merges one spec's groundtruth services into
// the running cross-spec `uniqueServices` map and returns a duplicate
// warning for each service that clashes with a *different* spec at a
// previous index. Services repeated within the same spec are deduped
// first — see #157: HTTP chaos groundtruth can legitimately list the same
// service name twice (e.g. `GET /` against `front-end` yields
// `["front-end","front-end"]`), and without this dedup the cross-spec
// check would fire against itself and produce `positions 0 and 0`
// self-duplicates.
func mergeSpecServicesForDupCheck(uniqueServices map[string]int, specServices []string, idx int) []string {
	seenInSpec := make(map[string]struct{}, len(specServices))
	var warnings []string
	for _, service := range specServices {
		if service == "" {
			continue
		}
		if _, dup := seenInSpec[service]; dup {
			continue
		}
		seenInSpec[service] = struct{}{}
		if oldIdx, exists := uniqueServices[service]; exists {
			warnings = append(warnings,
				fmt.Sprintf("service '%s' at positions %d and %d", service, oldIdx, idx))
			continue
		}
		uniqueServices[service] = idx
	}
	return warnings
}

// buildFreshSlotItem produces an injectionProcessItem for a batch whose
// namespace was bootstrapped (PR-C of #166): the slot has no deployed
// workload at submit time, so we can't run guidedcli.BuildInjection's
// pod-listing without a spurious "app not found" error. Skip the
// validation entirely; pull maxDuration from the request fields and let
// the runtime FaultInjection task do the real BuildInjection after
// RestartPedestal has helm-installed the workload. Cross-spec
// groundtruth dedup is skipped because fresh slots are exclusive — no
// other batch lands here in the same submit.
func buildFreshSlotItem(batchIndex int, configs []guidedcli.GuidedConfig) injectionProcessItem {
	maxDuration := 0
	for _, cfg := range configs {
		if cfg.Duration != nil && *cfg.Duration > maxDuration {
			maxDuration = *cfg.Duration
		}
	}
	return injectionProcessItem{
		index:         batchIndex,
		faultDuration: maxDuration,
		guidedConfigs: configs,
	}
}

// stripTrailingDigits removes the numeric suffix from a system short-code so
// the namespace-instance form ("ts0", "sockshop14") collapses to the bare
// pedestal name ("ts", "sockshop"). Returns the input unchanged when there
// are no trailing digits.
func stripTrailingDigits(s string) string {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == 0 {
		return s
	}
	return s[:i]
}

// firstGuidedNamespace returns the first non-empty `namespace` among the
// given guided configs. Used by SubmitFaultInjection to promote the
// user-supplied namespace into RestartPedestal's payload as a hard
// constraint (#156). Empty when no config names a namespace — callers must
// then fall back to the NsPattern-pool selection in monitor.
func firstGuidedNamespace(configs []guidedcli.GuidedConfig) string {
	for _, cfg := range configs {
		if ns := strings.TrimSpace(cfg.Namespace); ns != "" {
			return ns
		}
	}
	return ""
}

type injectionProcessItem struct {
	index         int
	faultDuration int
	guidedConfigs []guidedcli.GuidedConfig
	executeTime   time.Time
	// allocatedNamespace and preallocTraceID are populated by the
	// AutoAllocate submit path (#166) when the server picks a namespace at
	// submit time. The pre-generated traceID is later assigned to the
	// task's TraceID so the runtime RestartPedestal's same-owner re-acquire
	// matches the allocator's existing lock. Empty otherwise.
	allocatedNamespace string
	preallocTraceID    string
}

func flattenYAMLToParameters(data map[string]any, prefix string) []dto.ParameterSpec {
	var params []dto.ParameterSpec
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]any:
			params = append(params, flattenYAMLToParameters(v, fullKey)...)
		case []any:
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				logrus.Warnf("Failed to marshal array for key %s: %v", fullKey, err)
				continue
			}
			params = append(params, dto.ParameterSpec{Key: fullKey, Value: string(jsonBytes)})
		default:
			params = append(params, dto.ParameterSpec{Key: fullKey, Value: v})
		}
	}
	return params
}

func (s *Service) removeDuplicated(items []injectionProcessItem) ([]injectionProcessItem, []int, []int, error) {
	engineConfigStrs := make([]string, len(items))
	for i, item := range items {
		if len(item.guidedConfigs) == 0 {
			continue
		}

		b, err := json.Marshal(item.guidedConfigs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to marshal engine config at batch index %d: %w", i, err)
		}
		engineConfigStrs[i] = string(b)
	}

	orderedUniqueIdx := make([]int, 0, len(engineConfigStrs))
	seen := make(map[string]struct{}, len(engineConfigStrs))
	duplicatedInRequest := make([]int, 0)
	for i, key := range engineConfigStrs {
		if key == "" {
			orderedUniqueIdx = append(orderedUniqueIdx, i)
			continue
		}
		if _, ok := seen[key]; ok {
			duplicatedInRequest = append(duplicatedInRequest, items[i].index)
			continue
		}
		seen[key] = struct{}{}
		orderedUniqueIdx = append(orderedUniqueIdx, i)
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}

	existed := make(map[string]struct{})
	for start := 0; start < len(keys); start += 100 {
		end := min(start+100, len(keys))
		existing, err := s.repo.listExistingEngineConfigs(keys[start:end])
		if err != nil {
			return nil, nil, nil, err
		}
		for _, v := range existing {
			existed[v] = struct{}{}
		}
	}

	out := make([]injectionProcessItem, 0, len(orderedUniqueIdx))
	alreadyExisted := make([]int, 0)
	for _, idx := range orderedUniqueIdx {
		key := engineConfigStrs[idx]
		if key != "" {
			if _, ok := existed[key]; ok {
				alreadyExisted = append(alreadyExisted, items[idx].index)
				continue
			}
		}

		items[idx].executeTime = time.Now().Add(time.Duration(idx*2) * time.Second)
		out = append(out, items[idx])
	}

	return out, duplicatedInRequest, alreadyExisted, nil
}

// parseBatchGuidedSpecs parses a single batch of GuidedConfig specs for
// parallel execution. Each spec contributes its duration to the batch max,
// its system to the pedestal sanity check, and its app to the cross-spec
// duplicate-service warning. The previous in-process BuildInjection call
// (which round-tripped through chaos-experiment's resourcelookup) is gone:
// chaos-service owns the real builder now and runs it at execute time. The
// dispatcher's renderGroundtruths uses the same `[]string{cfg.App}` shape, so
// keeping the dedup check on cfg.App is consistent with what actually gets
// persisted as ground_truth.
func parseBatchGuidedSpecs(_ context.Context, pedestal string, batchIndex int, configs []guidedcli.GuidedConfig) (*injectionProcessItem, string, error) {
	if len(configs) == 0 {
		return nil, "", fmt.Errorf("empty guided fault batch at index %d", batchIndex)
	}

	maxDuration := 0
	uniqueServices := make(map[string]int, len(configs))
	var duplicateServiceWarnings []string

	for idx, cfg := range configs {
		// cfg.System carries the namespace-instance short code (e.g. "ts0");
		// pedestal is the system short code (e.g. "ts"). Strip the trailing
		// digits before comparing so a `ts0` spec lines up with a `ts`
		// pedestal. Empty cfg.System means the user left it to the server —
		// that's allowed; skip the check rather than rejecting.
		if sys := strings.TrimSpace(cfg.System); sys != "" {
			if normalized := stripTrailingDigits(sys); normalized != pedestal {
				return nil, "", fmt.Errorf("mismatched system type %s for pedestal %s at index %d", normalized, pedestal, idx)
			}
		}

		duration := 0
		if cfg.Duration != nil {
			duration = *cfg.Duration
		}
		if duration > maxDuration {
			maxDuration = duration
		}

		// Dedup on cfg.App. dispatcher.renderGroundtruths writes
		// {service: [cfg.App]} to caller_metadata.groundtruths, so a clash on
		// App is exactly what surfaces as a duplicate ground_truth service
		// downstream.
		duplicateServiceWarnings = append(duplicateServiceWarnings,
			mergeSpecServicesForDupCheck(uniqueServices, []string{strings.TrimSpace(cfg.App)}, idx)...)
	}

	var warning string
	if len(duplicateServiceWarnings) > 0 {
		warning = fmt.Sprintf("Batch %d contains duplicate service injections: %s",
			batchIndex, strings.Join(duplicateServiceWarnings, "; "))
	}

	return &injectionProcessItem{
		index:         batchIndex,
		faultDuration: maxDuration,
		guidedConfigs: configs,
	}, warning, nil
}
