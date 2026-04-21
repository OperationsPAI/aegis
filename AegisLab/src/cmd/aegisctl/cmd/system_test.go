package cmd

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// sevenValidConfigs produces a synthetic dynamic_configs snippet with all
// seven injection.system.<name>.* rows correctly typed, using the staging
// data.yaml for `ts` as the model. Callers may drop or corrupt specific
// rows in-place to exercise negative cases.
func sevenValidConfigs(name string) []seedDynamicConfig {
	return []seedDynamicConfig{
		{Key: "injection.system." + name + ".count", DefaultValue: "1", ValueType: 2, Scope: 2, Category: "injection.system.count", Description: "Number of system"},
		{Key: "injection.system." + name + ".ns_pattern", DefaultValue: "^" + name + "\\d+$", ValueType: 0, Scope: 2, Category: "injection.system.ns_pattern"},
		{Key: "injection.system." + name + ".extract_pattern", DefaultValue: "^(" + name + ")(\\d+)$", ValueType: 0, Scope: 2, Category: "injection.system.extract_pattern"},
		{Key: "injection.system." + name + ".display_name", DefaultValue: "Test System", ValueType: 0, Scope: 2, Category: "injection.system.display_name"},
		{Key: "injection.system." + name + ".app_label_key", DefaultValue: "app", ValueType: 0, Scope: 2, Category: "injection.system.app_label_key"},
		{Key: "injection.system." + name + ".is_builtin", DefaultValue: "true", ValueType: 1, Scope: 2, Category: "injection.system.is_builtin"},
		{Key: "injection.system." + name + ".status", DefaultValue: "1", ValueType: 2, Scope: 2, Category: "injection.system.status"},
	}
}

func marshalSeed(t *testing.T, cfgs []seedDynamicConfig) *seedDoc {
	t.Helper()
	// Round-trip through YAML to exercise the real parse path and catch
	// tag-level regressions in seedDoc / seedDynamicConfig.
	doc := seedDoc{DynamicConfigs: cfgs}
	buf, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("marshal synthetic seed: %v", err)
	}
	var parsed seedDoc
	if err := yaml.Unmarshal(buf, &parsed); err != nil {
		t.Fatalf("unmarshal synthetic seed: %v", err)
	}
	return &parsed
}

func TestSystemSeedValidatesCleanly(t *testing.T) {
	doc := marshalSeed(t, sevenValidConfigs("ts"))
	seed, err := extractSystemSeed(doc, "ts")
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	if err := validateSystemSeed(seed); err != nil {
		t.Fatalf("validateSystemSeed: unexpected error: %v", err)
	}
	if seed.Count != 1 {
		t.Errorf("Count: want 1, got %d", seed.Count)
	}
	if !seed.IsBuiltin {
		t.Errorf("IsBuiltin: want true")
	}
	if seed.Status != 1 {
		t.Errorf("Status: want 1, got %d", seed.Status)
	}
	if seed.DisplayName != "Test System" {
		t.Errorf("DisplayName: want %q, got %q", "Test System", seed.DisplayName)
	}
	if seed.AppLabelKey != "app" {
		t.Errorf("AppLabelKey: want %q, got %q", "app", seed.AppLabelKey)
	}
}

func TestSystemSeedMissingStatusRejected(t *testing.T) {
	cfgs := sevenValidConfigs("ts")
	// Drop the status row (last element).
	cfgs = cfgs[:len(cfgs)-1]
	doc := marshalSeed(t, cfgs)

	seed, err := extractSystemSeed(doc, "ts")
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	err = validateSystemSeed(seed)
	if err == nil {
		t.Fatal("validateSystemSeed: expected error for missing status, got nil")
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("error should mention missing 'status' key; got: %v", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mark the key as missing; got: %v", err)
	}
}

func TestSystemSeedWrongValueTypeRejected(t *testing.T) {
	cfgs := sevenValidConfigs("ts")
	// Corrupt the count row: claim it's a string (0) instead of int (2).
	for i := range cfgs {
		if strings.HasSuffix(cfgs[i].Key, ".count") {
			cfgs[i].ValueType = 0
			break
		}
	}
	doc := marshalSeed(t, cfgs)

	seed, err := extractSystemSeed(doc, "ts")
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	err = validateSystemSeed(seed)
	if err == nil {
		t.Fatal("validateSystemSeed: expected error for wrong value_type on count, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "injection.system.ts.count") {
		t.Errorf("error should pinpoint the offending key; got: %v", err)
	}
	if !strings.Contains(msg, "value_type") {
		t.Errorf("error should mention value_type; got: %v", err)
	}
}
