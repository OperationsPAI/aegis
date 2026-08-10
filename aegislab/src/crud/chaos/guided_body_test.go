package chaos

import (
	"errors"
	"strings"
	"testing"

	guidedcli "aegis/platform/chaos"
)

func intPtr(v int) *int { return &v }

// The delay/latency-class faults regressed with an opaque 400 because the
// param renderer never emitted the schema-required severity key. The agent's
// chosen severity (--delay-duration for HTTP, --latency-duration for JVM) must
// reach the chaos param `delay_ms` verbatim.
func TestGuidedToChaosParams_DelaySeverityPassthrough(t *testing.T) {
	cases := []struct {
		capability string
		cfg        guidedcli.GuidedConfig
		want       int
	}{
		{"http_request_delay", guidedcli.GuidedConfig{DelayDuration: intPtr(500)}, 500},
		{"http_response_delay", guidedcli.GuidedConfig{DelayDuration: intPtr(500)}, 500},
		{"jvm_method_latency", guidedcli.GuidedConfig{LatencyDuration: intPtr(800)}, 800},
		{"jvm_mysql_latency", guidedcli.GuidedConfig{LatencyDuration: intPtr(800)}, 800},
	}
	for _, tc := range cases {
		params, err := GuidedToChaosParams(tc.capability, tc.cfg)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.capability, err)
		}
		if got := params["delay_ms"]; got != tc.want {
			t.Fatalf("%s: delay_ms=%v, want %d", tc.capability, got, tc.want)
		}
	}
}

// A delay/latency fault submitted without an agent-chosen severity must fail
// fast — never get a silent default — so the puzzle difficulty stays a
// deliberate choice.
func TestGuidedToChaosParams_MissingSeverityFailsFast(t *testing.T) {
	for _, capability := range []string{"http_request_delay", "http_response_delay", "jvm_method_latency"} {
		if _, err := GuidedToChaosParams(capability, guidedcli.GuidedConfig{}); err == nil {
			t.Fatalf("%s: expected error when severity is omitted, got nil", capability)
		}
	}
}

// http_request_abort requires no params, so an empty config still dispatches.
func TestGuidedToChaosParams_AbortNeedsNoSeverity(t *testing.T) {
	params, err := GuidedToChaosParams("http_request_abort", guidedcli.GuidedConfig{})
	if err != nil {
		t.Fatalf("abort should not require params: %v", err)
	}
	if _, ok := params["delay_ms"]; ok {
		t.Fatalf("abort must not carry delay_ms; got %v", params)
	}
}

// Drift guard: every seeded capability whose param_schema declares a required
// key must have a matching emitter entry in requiredParamsByCapability (and no
// stale entries). This is the exact class of drift that let the http delay
// subfamily ship without a delay_ms emitter.
func TestRequiredParamsByCapability_MatchesSeeds(t *testing.T) {
	seeds := []Capability{}
	seeds = append(seeds, SeedsNetwork...)
	seeds = append(seeds, SeedsPodChaosExtra...)
	seeds = append(seeds, SeedsStress...)
	seeds = append(seeds, SeedsTime...)
	seeds = append(seeds, SeedsDNS...)
	seeds = append(seeds, SeedsHTTP...)
	seeds = append(seeds, SeedsJVM...)

	fromSeeds := map[string][]string{}
	for _, c := range seeds {
		req := requiredParamKeys(c.ParamSchema)
		if len(req) > 0 {
			fromSeeds[c.Name] = req
		}
	}

	for name, want := range fromSeeds {
		got, ok := requiredParamsByCapability[name]
		if !ok {
			t.Fatalf("capability %q has required param_schema keys %v but no emitter entry in requiredParamsByCapability", name, want)
		}
		if !sameStringSet(got, want) {
			t.Fatalf("capability %q: emitter required keys %v != seed param_schema required %v", name, got, want)
		}
	}
	for name := range requiredParamsByCapability {
		if _, ok := fromSeeds[name]; !ok {
			t.Fatalf("requiredParamsByCapability has stale entry %q not backed by any seed param_schema required", name)
		}
	}
}

func requiredParamKeys(schema JSONMap) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestGuidedToChaosTarget_JVMMysql pins the contract that guided submit
// for jvm_mysql_* emits a target shape that passes the renderer's
// ValidateTarget — the same gate the production POST path runs. Before
// the schema fix the case branch shared with jvm_method_* produced
// {namespace, app, class, method}, which the new jvmMySQLTargetSchema
// rejects (additionalProperties:false + db_name/table required).
func TestGuidedToChaosTarget_JVMMysql(t *testing.T) {
	cfg := guidedcli.GuidedConfig{
		System:    "ts",
		App:       "ts-order-service",
		Database:  "ts",
		Table:     "orders",
		Operation: "SELECT",
	}
	for _, cap := range []string{"jvm_mysql_latency", "jvm_mysql_exception"} {
		target, err := GuidedToChaosTarget(cap, cfg)
		if err != nil {
			t.Fatalf("%s: GuidedToChaosTarget: %v", cap, err)
		}
		if _, hasClass := target["class"]; hasClass {
			t.Errorf("%s: target must not carry class (schema rejects)", cap)
		}
		if target["db_name"] != "ts" || target["table"] != "orders" || target["sql_type"] != "select" {
			t.Errorf("%s: target = %v", cap, target)
		}
		r, err := lookupRenderer(cap)
		if err != nil {
			t.Fatalf("%s: lookupRenderer: %v", cap, err)
		}
		if err := r.ValidateTarget(target); err != nil {
			t.Errorf("%s: ValidateTarget rejected guided-emitted target: %v", cap, err)
		}
	}

	missing := guidedcli.GuidedConfig{System: "ts", App: "ts-order-service"}
	if _, err := GuidedToChaosTarget("jvm_mysql_latency", missing); err == nil {
		t.Error("expected error when database/table missing")
	}
}

// TestValidateGuidedConfig is the #538 submit-time guard: the same
// required-param + per-capability schema checks the FaultInjection worker
// stage runs must reject a malformed config here, before the submit handler
// persists any trace/task. A valid config must pass unchanged.
func TestValidateGuidedConfig(t *testing.T) {
	base := guidedcli.GuidedConfig{System: "ts", App: "ts-order-service"}

	t.Run("missing required param is rejected", func(t *testing.T) {
		cfg := base
		cfg.ChaosType = "HTTPResponseReplaceCode"
		cfg.Route = "/"
		cfg.HTTPMethod = "GET"
		err := ValidateGuidedConfig(cfg)
		if err == nil {
			t.Fatal("expected rejection for http_response_replace_code without status_code")
		}
		if !strings.Contains(err.Error(), "status_code") {
			t.Errorf("error should name the missing param, got: %v", err)
		}
	})

	t.Run("schema-rejected param is rejected", func(t *testing.T) {
		cfg := base
		cfg.ChaosType = "CPUStress"
		cfg.CPULoad = intPtr(200)
		err := ValidateGuidedConfig(cfg)
		if err == nil {
			t.Fatal("expected schema rejection for cpu_stress load_pct=200")
		}
		if !errors.Is(err, ErrSchemaValidation) {
			t.Errorf("schema failures must wrap ErrSchemaValidation, got: %v", err)
		}
	})

	t.Run("valid config passes", func(t *testing.T) {
		cfg := base
		cfg.ChaosType = "CPUStress"
		cfg.CPULoad = intPtr(50)
		if err := ValidateGuidedConfig(cfg); err != nil {
			t.Errorf("valid cpu_stress config should pass, got: %v", err)
		}
	})
}
