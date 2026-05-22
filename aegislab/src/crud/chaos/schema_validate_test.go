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
	if !strings.Contains(err.Error(), "points[0].target") {
		t.Fatalf("error must surface field path points[0].target; got %v", err)
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

// CreateInjection happy path with valid params is covered by
// TestCreateInjection_IdempotentReplay; this exercises the failure side:
// out-of-range integer must fail with field path `params.duration_s`.
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
