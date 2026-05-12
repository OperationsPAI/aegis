package config

import (
	"fmt"
	"regexp"
	"time"

	"aegis/platform/consts"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/mitchellh/mapstructure"
)

const (
	ConfigKeyChaosSystem = "injection.system"
	// DefaultReadinessTimeoutSeconds is applied when the per-system override
	// (`injection.system.<sys>.readiness_timeout_seconds`) is unset or
	// non-positive. 900s = 15 min; long enough for the larger built-in
	// systems (sockshop / otel-demo / TrainTicket) to come up on a cold
	// kind cluster, short enough to fail loudly on a genuinely broken
	// install.
	DefaultReadinessTimeoutSeconds = 900
)

// ChaosSystemConfig is the aggregate of every injection.system.<name>.* key in
// Viper (which mirrors etcd at runtime). etcd is the single source of truth —
// there is no longer a systems table to consult.
type ChaosSystemConfig struct {
	System         string
	Count          int               `mapstructure:"count"`
	NsPattern      string            `mapstructure:"ns_pattern"`
	ExtractPattern string            `mapstructure:"extract_pattern"`
	DisplayName    string            `mapstructure:"display_name"`
	AppLabelKey    string            `mapstructure:"app_label_key"`
	IsBuiltin      bool              `mapstructure:"is_builtin"`
	Status         consts.StatusType `mapstructure:"status"`
	// ReadinessTimeoutSeconds caps the post-install workload-readiness probe
	// in RestartPedestal. DSB systems (HotelReservation, SocialNetwork,
	// MediaMicroservices, TrainTicket) chain init containers across 20–41
	// services and routinely need 15–25 min on cold clusters; the previous
	// hard-coded 5 min Helm wait timed out and tripped restart.pedestal.failed.
	// Per-system override via etcd key
	// `injection.system.<sys>.readiness_timeout_seconds`. Zero / unset falls
	// back to DefaultReadinessTimeoutSeconds (900 = 15 min).
	ReadinessTimeoutSeconds int `mapstructure:"readiness_timeout_seconds"`
}

// ReadinessTimeout returns the workload-readiness probe timeout for this
// system, falling back to DefaultReadinessTimeoutSeconds when the per-system
// override is unset or non-positive.
func (s *ChaosSystemConfig) ReadinessTimeout() time.Duration {
	secs := s.ReadinessTimeoutSeconds
	if secs <= 0 {
		secs = DefaultReadinessTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// chaosSystemConfigManager is now a stateless façade over Viper — every call
// decodes the current `injection.system.*` subtree on demand. Keeping the
// manager type lets existing call sites (`config.GetChaosSystemConfigManager().Get(...)`)
// continue to compile without change.
//
// Previously this held an in-memory cache that only refreshed when the etcd
// watcher fired `Reload`. That produced two bugs:
//  1. Producer processes — which don't register the consumer watch handler
//     — saw a frozen snapshot from startup.
//  2. Consumer had a narrow race between a Viper update and the Reload call.
//
// Reading Viper on demand removes both hazards. The decode is cheap (a map
// lookup + mapstructure on a handful of string keys).
type chaosSystemConfigManager struct{}

// GetChaosSystemConfigManager returns the singleton façade. The old cached
// implementation is gone; this is kept so existing callers compile.
func GetChaosSystemConfigManager() *chaosSystemConfigManager {
	return &chaosSystemConfigManager{}
}

// Get returns the configuration for a specific system, reading fresh from
// Viper on every call.
func (m *chaosSystemConfigManager) Get(system chaos.SystemType) (ChaosSystemConfig, bool) {
	all := readChaosSystemConfigs()
	cfg, ok := all[system.String()]
	return cfg, ok
}

// GetAll returns a fresh snapshot of every configured system.
func (m *chaosSystemConfigManager) GetAll() map[string]ChaosSystemConfig {
	return readChaosSystemConfigs()
}

// readChaosSystemConfigs decodes the `injection.system.*` subtree from Viper
// into the strongly-typed aggregate shape. Invalid entries (decode errors)
// are dropped so a single malformed key can't blow up the whole read.
func readChaosSystemConfigs() map[string]ChaosSystemConfig {
	raw := GetMap(ConfigKeyChaosSystem)
	out := make(map[string]ChaosSystemConfig, len(raw))
	for name, entry := range raw {
		var cfg ChaosSystemConfig
		if err := mapstructure.Decode(entry, &cfg); err != nil {
			continue
		}
		cfg.System = name
		out[name] = cfg
	}
	return out
}

// ExtractNsPrefixAndNumber extracts the number from a namespace string
// using the system-specific extract pattern from dynamic config
func (s *ChaosSystemConfig) ExtractNsNumber(namespace string) (int, error) {
	if s.ExtractPattern == "" {
		return 0, fmt.Errorf("extract pattern not defined for system %s", s.System)
	}

	re, err := regexp.Compile(s.ExtractPattern)
	if err != nil {
		return 0, fmt.Errorf("invalid extract pattern for system %s: %w", s.System, err)
	}

	matches := re.FindStringSubmatch(namespace)
	if len(matches) >= 3 {
		var number int
		_, err = fmt.Sscanf(matches[2], "%d", &number)
		if err != nil {
			return 0, fmt.Errorf("failed to parse number from namespace '%s': %w", namespace, err)
		}
		return number, nil
	}

	return 0, fmt.Errorf("namespace '%s' does not match extract pattern for system %s", namespace, s.System)
}

// IsEnabled reports whether the system is enabled (status == CommonEnabled).
func (s *ChaosSystemConfig) IsEnabled() bool {
	return s.Status == consts.CommonEnabled
}

// GetAllNamespaces generates a list of all namespaces based on the system count map
func GetAllNamespaces() ([]string, error) {
	systemConfigMap := GetChaosSystemConfigManager().GetAll()
	namespaces := make([]string, 0)
	for _, cfg := range systemConfigMap {
		if !cfg.IsEnabled() {
			continue
		}
		template := convertPatternToTemplate(cfg.NsPattern)
		if template == "" {
			return nil, fmt.Errorf("failed to convert ns_pattern to template: %s", cfg.NsPattern)
		}

		// Generate namespaces from 0 to count-1
		for idx := 0; idx < cfg.Count; idx++ {
			ns := fmt.Sprintf(template, idx)
			namespaces = append(namespaces, ns)
		}
	}

	return namespaces, nil
}

// convertPatternToTemplate converts a regex pattern to a sprintf template
// Convert ns_pattern to a generation template
//
// e.g., "^ts\d+$" -> "ts%d"
//
// e.g., "^app-\d+$" -> "app-%d"
//
// e.g., "^test_\d+_suffix$" -> "test_%d_suffix"
func convertPatternToTemplate(pattern string) string {
	// Remove anchors ^ and $
	template := pattern
	if len(template) > 0 && template[0] == '^' {
		template = template[1:]
	}
	if len(template) > 0 && template[len(template)-1] == '$' {
		template = template[:len(template)-1]
	}

	// Replace \d+ with %d
	template = regexp.MustCompile(`\\d\+`).ReplaceAllString(template, "%d")

	return template
}
