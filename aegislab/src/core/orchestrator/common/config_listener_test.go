package common

import "testing"

// TestConfigCenterKeyForRoundTrips guards step 4 cutover: the listener's
// primary /aegis read key must use the same first-dot namespace split that the
// writers (aegisctl etcd put, system PATCH, seed mirror) use, and must round-trip
// back through parseConfigCenterKV to the original dotted dynamic_config key.
// A split mismatch means the loader reads the wrong /aegis key and silently
// falls back to the legacy tree, defeating the cutover.
func TestConfigCenterKeyForRoundTrips(t *testing.T) {
	l := &ConfigUpdateListener{}
	prefix := configCenterPrefix()

	for _, dotted := range []string{
		"rate_limiting.max_concurrent_restarts_pedestal",
		"orchestrator.pedestal.restart_timeout_seconds",
		"database.clickhouse.host",
	} {
		ccKey, ok := l.configCenterKeyFor(dotted)
		if !ok {
			t.Fatalf("%s: expected ok", dotted)
		}
		gotKey, _, gotOK := parseConfigCenterKV(prefix, ccKey, []byte(`"x"`))
		if !gotOK {
			t.Fatalf("%s: round-trip parse failed for %q", dotted, ccKey)
		}
		if gotKey != dotted {
			t.Errorf("%s: round-tripped to %q", dotted, gotKey)
		}
	}

	if _, ok := l.configCenterKeyFor("nodot"); ok {
		t.Error("expected !ok for key without namespace split")
	}
}

// TestParseConfigCenterKV is the regression guard for the disjoint-tree break:
// `aegisctl etcd put rate_limiting.max_concurrent_restarts_pedestal 10` writes
// /aegis/<env>/rate_limiting/max_concurrent_restarts_pedestal with the
// JSON-encoded value "10", while the orchestrator's handler registry is keyed
// on the dotted dynamic_config key and SetViperValue expects a plain decimal
// string. The bridge must rejoin namespace+key with a dot and unwrap the JSON
// scalar, or the operator's put never reaches the rate limiter.
func TestParseConfigCenterKV(t *testing.T) {
	const prefix = "/aegis/dev/"

	cases := []struct {
		name      string
		fullKey   string
		rawValue  string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{
			name:      "rate limiting int put",
			fullKey:   prefix + "rate_limiting/max_concurrent_restarts_pedestal",
			rawValue:  `"10"`,
			wantKey:   "rate_limiting.max_concurrent_restarts_pedestal",
			wantValue: "10",
			wantOK:    true,
		},
		{
			name:      "orchestrator pedestal dotted key",
			fullKey:   prefix + "orchestrator.pedestal/restart_timeout_seconds",
			rawValue:  `300`,
			wantKey:   "orchestrator.pedestal.restart_timeout_seconds",
			wantValue: "300",
			wantOK:    true,
		},
		{
			name:      "string value unwrapped",
			fullKey:   prefix + "database/clickhouse.host",
			rawValue:  `"ch.example"`,
			wantKey:   "database.clickhouse.host",
			wantValue: "ch.example",
			wantOK:    true,
		},
		{
			name:     "outside prefix",
			fullKey:  "/rcabench/config/consumer/rate_limiting.max_concurrent_restarts_pedestal",
			rawValue: `"10"`,
			wantOK:   false,
		},
		{
			name:     "no namespace split",
			fullKey:  prefix + "loneKey",
			rawValue: `"x"`,
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotKey, gotValue, gotOK := parseConfigCenterKV(prefix, tc.fullKey, []byte(tc.rawValue))
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if gotKey != tc.wantKey {
				t.Errorf("key = %q, want %q", gotKey, tc.wantKey)
			}
			if gotValue != tc.wantValue {
				t.Errorf("value = %q, want %q", gotValue, tc.wantValue)
			}
		})
	}
}
