package resourcelookup

import (
	"context"
	"sort"

	"github.com/OperationsPAI/chaos-experiment/client"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// GetInjectableAppLabels returns the app labels used by guided app-level chaos.
// This intentionally filters the namespace pod labels down to services that can
// act as network sources so AppIdx stays consistent between guided resolution
// and later handler-side CRD creation / groundtruth lookup.
func GetInjectableAppLabels(ctx context.Context, system systemconfig.SystemType, namespace string) ([]string, error) {
	labelKey := systemconfig.GetAppLabelKey(system)
	labels, err := client.GetLabels(ctx, namespace, labelKey)
	if err != nil || len(labels) == 0 {
		fallback, fallbackErr := systemconfig.GetMetadataStore().GetAllServiceNames(string(system))
		if len(fallback) == 0 {
			if err != nil {
				return nil, err
			}
			return nil, fallbackErr
		}
		sort.Strings(fallback)
		labels = fallback
	}

	filtered := filterInjectableServiceLabels(system, labels)
	if len(filtered) == 0 {
		return labels, nil
	}
	return filtered, nil
}

func filterInjectableServiceLabels(system systemconfig.SystemType, labels []string) []string {
	allowed := injectableSourceServices(system)
	if len(allowed) == 0 {
		return labels
	}

	result := make([]string, 0, len(labels))
	seen := make(map[string]bool, len(labels))
	for _, label := range labels {
		if allowed[label] && !seen[label] {
			seen[label] = true
			result = append(result, label)
		}
	}
	return result
}

func injectableSourceServices(system systemconfig.SystemType) map[string]bool {
	pairs, err := systemconfig.GetMetadataStore().GetNetworkPairs(string(system))
	if err != nil || len(pairs) == 0 {
		return nil
	}

	allowed := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		if pair.Source != "" {
			allowed[pair.Source] = true
		}
	}
	return allowed
}
