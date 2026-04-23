package resourcelookup

import (
	"context"
	"sort"

	"github.com/OperationsPAI/chaos-experiment/client"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// GetInjectableAppLabels returns the app labels used by guided app-level chaos.
// The allowlist is the union of every service the system has metadata for
// (service-endpoints, gRPC operations, database operations, JVM class methods,
// runtime mutator targets). Limiting it to network-pair Sources used to drop
// every leaf service whose recorded spans were Server-kind only — even though
// PodKill / PodFailure / CPUStress legitimately target those pods.
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
	allowed := injectableServices(system)
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

// injectableServices returns every service the system has metadata for, in any
// metadata category. Used as the allowlist for app-level chaos.
func injectableServices(system systemconfig.SystemType) map[string]bool {
	names, err := systemconfig.GetMetadataStore().GetAllServiceNames(string(system))
	if err != nil || len(names) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			allowed[name] = true
		}
	}
	return allowed
}

