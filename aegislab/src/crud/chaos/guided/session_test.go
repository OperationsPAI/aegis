package guided

import (
	"context"
	"testing"
)

// Zero-instance + bootstrap: a registered system with no live namespace must
// still resolve to ready_to_apply when the app is provided and --auto /
// --allow-bootstrap are set. Without the bypass this hit live pod discovery
// against the un-allocated namespace and failed with "no labels found for key
// app in namespace <ns>" (the #166 chicken-and-egg). The namespace is left
// empty so the server allocates it at apply.
func TestResolveBootstrapSkipsLivePodDiscovery(t *testing.T) {
	cfg := GuidedConfig{
		System:         "sn",
		App:            "user-service",
		ChaosType:      "PodKill",
		Auto:           true,
		AllowBootstrap: true,
	}

	resp, err := Resolve(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resp.Stage != "ready_to_apply" || !resp.CanApply {
		t.Fatalf("expected ready_to_apply/can_apply, got stage=%q can_apply=%v errors=%v", resp.Stage, resp.CanApply, resp.Errors)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("expected no resolver errors, got %v", resp.Errors)
	}
	if resp.Config.Namespace != "" {
		t.Fatalf("expected namespace to stay empty for server-side allocation, got %q", resp.Config.Namespace)
	}
	if resp.Config.App != "user-service" {
		t.Fatalf("expected app to be preserved, got %q", resp.Config.App)
	}
	if resp.ApplyPayload == nil {
		t.Fatal("expected a non-nil apply payload")
	}
}

func TestMergeConfigClearsDownstreamWhenAppChanges(t *testing.T) {
	fileCfg := &ConfigFile{
		GuidedSession: GuidedSession{
			Config: GuidedConfig{
				System:        "ts",
				SystemType:    "ts",
				Namespace:     "ts",
				App:           "ts-auth-service",
				ChaosType:     "JVMRuntimeMutator",
				Class:         "auth.Jwt",
				Method:        "createToken",
				MutatorConfig: "string:reverse",
				Duration:      intPtr(9),
			},
		},
	}

	merged := MergeConfig(fileCfg, GuidedConfig{App: "ts-order-service"})
	if merged.App != "ts-order-service" {
		t.Fatalf("expected app override to apply, got %q", merged.App)
	}
	if merged.ChaosType != "" {
		t.Fatalf("expected chaos type to be cleared, got %q", merged.ChaosType)
	}
	if merged.Class != "" || merged.Method != "" || merged.MutatorConfig != "" {
		t.Fatalf("expected downstream JVM selections to be cleared, got class=%q method=%q mutator=%q", merged.Class, merged.Method, merged.MutatorConfig)
	}
	if merged.Duration == nil || *merged.Duration != 9 {
		t.Fatalf("expected duration to be preserved, got %#v", merged.Duration)
	}
}

func TestMergeConfigResetsRootWhenNamespaceChanges(t *testing.T) {
	fileCfg := &ConfigFile{
		GuidedSession: GuidedSession{
			Config: GuidedConfig{
				System:     "ts",
				SystemType: "ts",
				Namespace:  "ts",
				App:        "ts-auth-service",
				ChaosType:  "PodKill",
				Duration:   intPtr(5),
			},
		},
	}

	merged := MergeConfig(fileCfg, GuidedConfig{Namespace: "ts0"})
	if merged.Namespace != "ts0" {
		t.Fatalf("expected namespace override to apply, got %q", merged.Namespace)
	}
	if merged.System != "" || merged.SystemType != "" {
		t.Fatalf("expected saved root fields to be cleared before normalization, got system=%q systemType=%q", merged.System, merged.SystemType)
	}
	if merged.App != "" || merged.ChaosType != "" {
		t.Fatalf("expected downstream selections to be cleared, got app=%q chaosType=%q", merged.App, merged.ChaosType)
	}
}

func TestApplyNextSelectionUsesRequiredField(t *testing.T) {
	response := &GuidedResponse{
		Config: GuidedConfig{
			System:     "ts",
			SystemType: "ts",
			Namespace:  "ts",
			App:        "ts-auth-service",
		},
		Next: []FieldSpec{{
			Name:     "chaos_type",
			Kind:     "enum",
			Required: true,
			Options: []FieldOption{
				{Value: "PodKill", Label: "PodKill"},
				{Value: "PodFailure", Label: "PodFailure"},
			},
		}},
	}

	cfg, err := ApplyNextSelection(response, "PodKill")
	if err != nil {
		t.Fatalf("ApplyNextSelection returned error: %v", err)
	}
	if cfg.ChaosType != "PodKill" {
		t.Fatalf("expected chaos type to be set, got %q", cfg.ChaosType)
	}
}

func TestApplyNextSelectionParsesObjectRef(t *testing.T) {
	response := &GuidedResponse{
		Config: GuidedConfig{
			System:     "ts",
			SystemType: "ts",
			Namespace:  "ts",
			App:        "ts-auth-service",
			ChaosType:  "HTTPRequestDelay",
		},
		Next: []FieldSpec{{
			Name:      "endpoint",
			Kind:      "object_ref",
			Required:  true,
			KeyFields: []string{"http_method", "route"},
			Options: []FieldOption{{
				Value: "POST /api/v1/orders",
				Label: "POST /api/v1/orders",
				Metadata: map[string]any{
					"http_method": "POST",
					"route":       "/api/v1/orders",
				},
			}},
		}},
	}

	cfg, err := ApplyNextSelection(response, "POST /api/v1/orders")
	if err != nil {
		t.Fatalf("ApplyNextSelection returned error: %v", err)
	}
	if cfg.HTTPMethod != "POST" || cfg.Route != "/api/v1/orders" {
		t.Fatalf("expected endpoint selection to be populated, got method=%q route=%q", cfg.HTTPMethod, cfg.Route)
	}
}

func TestApplyNextSelectionRejectsGroupedStage(t *testing.T) {
	response := &GuidedResponse{
		Config: GuidedConfig{ChaosType: "CPUStress"},
		Next: []FieldSpec{{
			Name:     "params",
			Kind:     "group",
			Required: true,
			Fields: []FieldSpec{
				requiredNumberField("cpu_load", "CPU load", 1, 100, 1, "%"),
			},
		}},
	}

	if _, err := ApplyNextSelection(response, "80"); err == nil {
		t.Fatal("expected grouped stage to reject --next")
	}
}
