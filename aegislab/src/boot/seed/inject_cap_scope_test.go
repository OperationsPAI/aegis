package initialization

import (
	"testing"

	"aegis/platform/consts"
)

// TestInjectCapSeededAsGlobalScope guards the load-path contract for
// rate_limiting.max_concurrent_injections: checkSystemCapacity runs in the
// aegis-chaos process, whose boot only activates EnsureScope(Global). A row
// seeded with any non-Global scope is invisible to that process — config.GetInt
// returns 0 and the cap silently falls back, so the dynamic_config is dead.
// This pins every shipped data.yaml to Global scope so a scope flip-back can't
// regress the feature unnoticed.
func TestInjectCapSeededAsGlobalScope(t *testing.T) {
	files := []string{
		"../../../manifests/byte-cluster/initial-data/data.yaml",
		"../../../data/initial_data/prod/data.yaml",
		"../../../data/initial_data/staging/data.yaml",
	}
	for _, f := range files {
		data, err := loadInitialDataFromFile(f)
		if err != nil {
			t.Fatalf("%s: load: %v", f, err)
		}
		var found bool
		for _, cfg := range data.DynamicConfigs {
			if cfg.Key != consts.MaxConcurrentInjectionsKey {
				continue
			}
			found = true
			if cfg.Scope != consts.ConfigScopeGlobal {
				t.Errorf("%s: %s scope = %d, want Global (%d) so aegis-chaos EnsureScope(Global) loads it",
					f, cfg.Key, cfg.Scope, consts.ConfigScopeGlobal)
			}
		}
		if !found {
			t.Errorf("%s: missing %s seed row", f, consts.MaxConcurrentInjectionsKey)
		}
	}
}
