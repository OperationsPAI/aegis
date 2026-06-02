package configcenter

import (
	"context"
	"testing"
)

// TestResolveBelowEtcdFallsThroughToDBDefault guards step 2 of the
// config-storage unification: /aegis must be self-sufficient. When no etcd
// override, env var, or toml entry exists, a seeded dynamic_configs.default_value
// resolves via the db_default layer rather than dropping to the static default.
func TestResolveBelowEtcdFallsThroughToDBDefault(t *testing.T) {
	c := &defaultCenter{
		env: "dev",
		dbDefault: func(_ context.Context, namespace, key string) (string, bool) {
			if namespace == "rate_limiting" && key == "max_concurrent_restarts_pedestal" {
				return "7", true
			}
			return "", false
		},
	}

	raw, layer, err := c.resolveBelowEtcd(context.Background(),
		"rate_limiting", "max_concurrent_restarts_pedestal", 2)
	if err != nil {
		t.Fatalf("resolveBelowEtcd: %v", err)
	}
	if layer != LayerDBDefault {
		t.Fatalf("expected layer %q, got %q", LayerDBDefault, layer)
	}
	if string(raw) != `"7"` {
		t.Fatalf("expected db default %q, got %q", `"7"`, string(raw))
	}
}

// TestResolveBelowEtcdStaticDefaultWhenNoDBRow confirms the db_default layer is
// skipped (falling to the static default) when the key has no dynamic_configs
// row, and that a nil provider (DB-less deployment) is handled.
func TestResolveBelowEtcdStaticDefaultWhenNoDBRow(t *testing.T) {
	for name, provider := range map[string]DefaultProvider{
		"miss": func(context.Context, string, string) (string, bool) { return "", false },
		"nil":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			c := &defaultCenter{env: "dev", dbDefault: provider}
			raw, layer, err := c.resolveBelowEtcd(context.Background(), "ns", "unknown", 42)
			if err != nil {
				t.Fatalf("resolveBelowEtcd: %v", err)
			}
			if layer != LayerDefault {
				t.Fatalf("expected layer %q, got %q", LayerDefault, layer)
			}
			if string(raw) != "42" {
				t.Fatalf("expected static default 42, got %q", string(raw))
			}
		})
	}
}
