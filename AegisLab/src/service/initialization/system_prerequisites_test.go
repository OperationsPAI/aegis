package initialization

import (
	"testing"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newPrereqTestDB spins up an in-memory sqlite with just the
// system_prerequisites table — enough to exercise the upsert / idempotency
// contract without dragging in the full schema.
func newPrereqTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.SystemPrerequisite{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestInitializeSystemPrerequisites_SeedsAndIsIdempotent(t *testing.T) {
	db := newPrereqTestDB(t)
	data := &InitialData{
		Containers: []InitialDataContainer{
			{
				Type: consts.ContainerTypePedestal,
				Name: "sockshop",
				Prerequisites: []InitialSystemPrerequisite{
					{
						Name:      "coherence-operator",
						Kind:      "helm",
						Chart:     "coherence/coherence-operator",
						Namespace: "coherence-test",
						Version:   ">=3.4",
					},
				},
			},
			// Non-pedestal containers must be ignored — prereqs only apply
			// to type=2 entries (issue #115).
			{
				Type: consts.ContainerTypeAlgorithm,
				Name: "noise",
				Prerequisites: []InitialSystemPrerequisite{
					{Name: "ignored", Kind: "helm", Chart: "x", Namespace: "y"},
				},
			},
		},
	}

	if err := initializeSystemPrerequisites(db, data); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	var rows []model.SystemPrerequisite
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (non-pedestal ignored), got %d", len(rows))
	}
	if rows[0].SystemName != "sockshop" || rows[0].Name != "coherence-operator" || rows[0].Status != model.SystemPrerequisiteStatusPending {
		t.Fatalf("unexpected row: %+v", rows[0])
	}

	// Simulate a reconcile marking the row reconciled, then re-run the
	// seed with a *changed* chart version — spec must update, but status
	// must NOT be stomped back to pending (contract for boot-time reseed).
	if err := db.Model(&model.SystemPrerequisite{}).
		Where("id = ?", rows[0].ID).
		Update("status", model.SystemPrerequisiteStatusReconciled).Error; err != nil {
		t.Fatalf("mark reconciled: %v", err)
	}
	data.Containers[0].Prerequisites[0].Version = ">=3.5"
	if err := initializeSystemPrerequisites(db, data); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var after []model.SystemPrerequisite
	if err := db.Find(&after).Error; err != nil {
		t.Fatalf("find2: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 row after re-seed (idempotent), got %d", len(after))
	}
	if after[0].Status != model.SystemPrerequisiteStatusReconciled {
		t.Fatalf("status regressed on re-seed: got %q, want reconciled", after[0].Status)
	}
	// `>=` gets HTML-escaped to `>=` by the default encoder.
	if !containsSubstr(after[0].SpecJSON, "3.5") {
		t.Fatalf("spec_json did not update on re-seed: %s", after[0].SpecJSON)
	}
}

func TestBuildPrerequisiteRow_DefaultsKindToHelm(t *testing.T) {
	row, err := buildPrerequisiteRow("sockshop", InitialSystemPrerequisite{
		Name:      "coherence-operator",
		Chart:     "coherence/coherence-operator",
		Namespace: "coherence-test",
		Version:   ">=3.4",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if row.Kind != model.SystemPrerequisiteKindHelm {
		t.Fatalf("want default kind=helm, got %q", row.Kind)
	}
	if !containsSubstr(row.SpecJSON, `"chart":"coherence/coherence-operator"`) {
		t.Fatalf("spec_json missing chart: %s", row.SpecJSON)
	}
}

func TestBuildPrerequisiteRow_RejectsHelmWithoutChart(t *testing.T) {
	if _, err := buildPrerequisiteRow("sockshop", InitialSystemPrerequisite{
		Name:      "coherence-operator",
		Kind:      "helm",
		Namespace: "coherence-test",
	}); err == nil {
		t.Fatalf("expected error for kind=helm with empty chart")
	}
}

func TestBuildPrerequisiteRow_StoresHelmValues(t *testing.T) {
	row, err := buildPrerequisiteRow("sockshop", InitialSystemPrerequisite{
		Name:      "coherence-operator",
		Kind:      "helm",
		Chart:     "coherence/coherence-operator",
		Namespace: "coherence-test",
		Values: []InitialHelmSetValue{
			{Key: "image.registry", Value: "pair-cn-shanghai.cr.volces.com/opspai"},
			{Key: "image.name", Value: "coherence-operator"},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !containsSubstr(row.SpecJSON, `"values":[`) {
		t.Fatalf("spec_json missing values: %s", row.SpecJSON)
	}
	if !containsSubstr(row.SpecJSON, `"key":"image.registry"`) {
		t.Fatalf("spec_json missing values key: %s", row.SpecJSON)
	}
	if !containsSubstr(row.SpecJSON, `"value":"pair-cn-shanghai.cr.volces.com/opspai"`) {
		t.Fatalf("spec_json missing values value: %s", row.SpecJSON)
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
