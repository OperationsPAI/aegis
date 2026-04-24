package injection

import (
	"aegis/dto"
	"aegis/infra/k8s"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/sirupsen/logrus"
)

// ensureGuidedNamespaces creates any referenced namespace that doesn't exist
// yet, so that the guided-build submit-time pod listing can find the (empty)
// namespace instead of failing with `namespaces "X" not found`. RestartPedestal
// will helm-install workloads into the namespace moments later; creating it
// early is harmless. First-run only: existing namespaces are left alone.
// Errors here are warnings, not fatal — if the cluster genuinely rejects the
// create, the subsequent BuildInjection will fail and report that instead.
func ensureGuidedNamespaces(ctx context.Context, configs []guidedcli.GuidedConfig) {
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
			continue
		}
		if created {
			logrus.Infof("submit: created namespace %q for guided submit (first-run bootstrap)", ns)
		}
	}
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
// parallel execution. Each GuidedConfig is resolved to an InjectionConf via
// guidedcli.BuildInjection solely to compute duration, system-type sanity
// check, and groundtruth-service dedup warnings. The returned item carries
// the original GuidedConfigs; the actual BuildInjection call at execute-time
// lives in the consumer.
func parseBatchGuidedSpecs(ctx context.Context, pedestal string, batchIndex int, configs []guidedcli.GuidedConfig) (*injectionProcessItem, string, error) {
	if len(configs) == 0 {
		return nil, "", fmt.Errorf("empty guided fault batch at index %d", batchIndex)
	}

	maxDuration := 0
	uniqueServices := make(map[string]int, len(configs))
	var duplicateServiceWarnings []string

	for idx, cfg := range configs {
		conf, systemType, err := guidedcli.BuildInjection(ctx, cfg)
		if err != nil {
			return nil, "", fmt.Errorf("failed to build injection from guided config at index %d: %w", idx, err)
		}
		if pedestal != systemType.String() {
			return nil, "", fmt.Errorf("mismatched system type %s for pedestal %s at index %d", systemType.String(), pedestal, idx)
		}

		duration := 0
		if cfg.Duration != nil {
			duration = *cfg.Duration
		}
		if duration > maxDuration {
			maxDuration = duration
		}

		groundtruth, err := conf.GetGroundtruth(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get groundtruth from guided config at index %d: %w", idx, err)
		}
		duplicateServiceWarnings = append(duplicateServiceWarnings,
			mergeSpecServicesForDupCheck(uniqueServices, groundtruth.Service, idx)...)
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
