package chaos

import (
	"errors"
	"strings"
	"testing"
)

// Import target validation: a manifest with target missing the required
// `app` key must fail at points[0].target with a leaf-level message; the
// happy baseline (covered by import_test.go) already exercises the OK case.
func TestImportPoints_TargetSchemaViolation(t *testing.T) {
	mgr := newImportTestManager(t)
	m := baseManifest()
	m.Spec.Points = []PointManifestEntry{
		{Capability: "pod_kill", Target: map[string]any{"namespace": "ts"}},
	}
	_, err := mgr.ImportPoints(t.Context(), "ts", m, true)
	if err == nil {
		t.Fatalf("expected schema validation error, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("want ErrSchemaValidation, got %v", err)
	}
	var sve *SchemaValidationError
	if !errors.As(err, &sve) {
		t.Fatalf("expected SchemaValidationError, got %T", err)
	}
	if len(sve.Leaves) == 0 || !strings.HasPrefix(sve.Leaves[0].Path, "points[0].target") {
		t.Fatalf("leaf path must start with points[0].target; got %+v", sve.Leaves)
	}
}

// Import param_overrides validation: extra unknown key trips
// additionalProperties:false (subset still enforces it). A required-key
// omission must NOT trip subset validation — overrides are partial by
// design.
func TestImportPoints_ParamOverridesSubsetRejectsUnknownKey(t *testing.T) {
	mgr := newImportTestManager(t)
	m := baseManifest()
	m.Spec.Points = []PointManifestEntry{
		{
			Capability:     "pod_kill",
			Target:         map[string]any{"namespace": "ts", "app": "frontend"},
			ParamOverrides: map[string]any{"unknown_knob": 1},
		},
	}
	_, err := mgr.ImportPoints(t.Context(), "ts", m, true)
	if err == nil {
		t.Fatalf("expected schema validation error, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("want ErrSchemaValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "points[0].param_overrides") {
		t.Fatalf("error must surface points[0].param_overrides; got %v", err)
	}
}

func TestImportPoints_ParamOverridesPartialAllowed(t *testing.T) {
	mgr := newImportTestManager(t)
	m := baseManifest()
	m.Spec.Points = []PointManifestEntry{
		{
			Capability:     "pod_kill",
			Target:         map[string]any{"namespace": "ts", "app": "frontend"},
			ParamOverrides: map[string]any{"duration_s": 5},
		},
	}
	if _, err := mgr.ImportPoints(t.Context(), "ts", m, true); err != nil {
		t.Fatalf("partial param_overrides should pass subset validation: %v", err)
	}
}

// Out-of-range integer must fail with field path `params.duration_s`.
func TestCreateInjection_ParamSchemaViolation(t *testing.T) {
	mgr, _, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)

	_, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID: pointID, Namespace: "ns0", IdempotencyKey: "k-bad-params",
		Params: map[string]any{"duration_s": 9999},
	})
	if err == nil {
		t.Fatalf("expected schema validation error, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("want ErrSchemaValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "params") {
		t.Fatalf("error must surface params path; got %v", err)
	}

	var count int64
	if err := db.Model(&Injection{}).Where("idempotency_key = ?", "k-bad-params").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected injection must not persist; got %d rows", count)
	}
}

// Precedence: a Point that pins duration_s=30 must override a caller
// that requests 9999, even though 9999 alone would fail param_schema
// validation. The pinned value reaches the executor and the persisted row.
func TestCreateInjection_PointOverrideWins(t *testing.T) {
	mgr, exec, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)
	if err := db.Model(&Point{}).Where("id = ?", pointID).
		Update("param_overrides", JSONMap{"duration_s": 30}).Error; err != nil {
		t.Fatalf("set override: %v", err)
	}

	inj, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID: pointID, Namespace: "ns0", IdempotencyKey: "k-override-wins",
		Params: map[string]any{"duration_s": 9999},
	})
	if err != nil {
		t.Fatalf("override should clobber caller value; got err %v", err)
	}
	if v := exec.lastParams["duration_s"]; toInt(v) != 30 {
		t.Fatalf("Apply got duration_s=%v; want 30 (override wins)", v)
	}
	if v := inj.Params["duration_s"]; toInt(v) != 30 {
		t.Fatalf("persisted Params duration_s=%v; want 30", v)
	}
}

// Deep merge: caller fills a sibling key the override didn't pin while
// the override's pinned subtree wins.
func TestMergeParams_DeepMerge(t *testing.T) {
	caller := map[string]any{
		"http": map[string]any{"timeout_ms": 100, "retries": 5},
		"tag":  "caller-tag",
	}
	override := map[string]any{
		"http": map[string]any{"timeout_ms": 250},
	}
	got := mergeParams(caller, override)
	httpMap, _ := got["http"].(map[string]any)
	if toInt(httpMap["timeout_ms"]) != 250 {
		t.Fatalf("override should pin nested timeout_ms; got %v", httpMap["timeout_ms"])
	}
	if toInt(httpMap["retries"]) != 5 {
		t.Fatalf("unpinned nested retries should survive from caller; got %v", httpMap["retries"])
	}
	if got["tag"] != "caller-tag" {
		t.Fatalf("unpinned top-level tag should survive from caller; got %v", got["tag"])
	}
}

// Strict-mode injection: a seed schema that does NOT declare
// additionalProperties:false must still reject unknown keys, because
// the server injects the keyword at every object-schema position.
func TestSchemaCompiler_InjectsAdditionalPropertiesFalse(t *testing.T) {
	sc := newSchemaCompiler()
	cap := &Capability{
		Name: "loose",
		TargetSchema: JSONMap{
			"type": "object",
			"properties": map[string]any{
				"app": map[string]any{"type": "string"},
			},
		},
	}
	schema, err := sc.forTarget(cap)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := validateInstance(schema, map[string]any{"app": "x"}, "target"); err != nil {
		t.Fatalf("known key should pass; got %v", err)
	}
	if err := validateInstance(schema, map[string]any{"app": "x", "unknown": 1}, "target"); err == nil {
		t.Fatalf("unknown key must be rejected after additionalProperties injection")
	}
}

// `required` keyword stripping must not eat a user property literally
// named "required". A schema whose object has a `properties.required`
// child still validates the child's type after subset cloning.
func TestParamsSubset_PreservesUserPropertyNamedRequired(t *testing.T) {
	sc := newSchemaCompiler()
	cap := &Capability{
		Name: "weird",
		ParamSchema: JSONMap{
			"type":     "object",
			"required": []any{"required"},
			"properties": map[string]any{
				"required": map[string]any{"type": "boolean"},
			},
		},
	}
	subset, err := sc.forParamsSubset(cap)
	if err != nil {
		t.Fatalf("compile subset: %v", err)
	}
	// Required keyword stripped → omitting the field is fine.
	if err := validateInstance(subset, map[string]any{}, "params"); err != nil {
		t.Fatalf("subset should drop required; got %v", err)
	}
	// Type check on the user-named property survives.
	if err := validateInstance(subset, map[string]any{"required": "not-a-bool"}, "params"); err == nil {
		t.Fatalf("type check on properties.required must survive subset strip")
	}
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case float32:
		return int(t)
	}
	return -1
}
