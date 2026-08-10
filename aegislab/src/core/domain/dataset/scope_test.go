package dataset

import (
	"strconv"
	"testing"

	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestDatasetScope_PredicateEnforced guards the PR 4a retrofit: a non-admin
// caller must only see datasets that are public OR explicitly granted via
// user_scoped_roles. Before the retrofit listDatasets returned the full
// table regardless of caller identity (code review on dataset/repository.go).
func TestDatasetScope_PredicateEnforced(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Dataset{}, &model.UserScopedRole{}); err != nil {
		t.Skipf("sqlite AutoMigrate unsupported: %v", err)
	}

	mkDS := func(name string, public bool) *model.Dataset {
		ds := &model.Dataset{
			Name:     name,
			Type:     "microservice",
			IsPublic: public,
			Status:   consts.CommonEnabled,
		}
		if err := db.Omit(datasetCommonOmitFields).Create(ds).Error; err != nil {
			t.Fatalf("seed dataset %s: %v", name, err)
		}
		return ds
	}

	publicDS := mkDS("public-ds", true)
	ownedDS := mkDS("owned-ds", false)
	otherDS := mkDS("other-ds", false)

	const callerID int64 = 42
	if err := db.Create(&model.UserScopedRole{
		UserID:    int(callerID),
		RoleID:    1,
		ScopeType: consts.ScopeTypeDataset,
		ScopeID:   strconv.Itoa(ownedDS.ID),
		Status:    consts.CommonEnabled,
	}).Error; err != nil {
		t.Fatalf("seed user_scoped_role: %v", err)
	}

	repo := NewRepository(db)

	// admin scope: sees all three
	all, total, err := repo.listDatasets(authz.SystemScope(), 100, 0, "", nil, nil)
	if err != nil {
		t.Fatalf("admin listDatasets: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("admin: expected 3 datasets, got total=%d len=%d", total, len(all))
	}

	// scoped non-admin: sees public + owned only
	scoped := authz.CallerScope{UserID: callerID}
	visible, total, err := repo.listDatasets(scoped, 100, 0, "", nil, nil)
	if err != nil {
		t.Fatalf("scoped listDatasets: %v", err)
	}
	if total != 2 || len(visible) != 2 {
		t.Fatalf("scoped: expected 2 datasets, got total=%d len=%d", total, len(visible))
	}
	seen := map[int]bool{}
	for _, d := range visible {
		seen[d.ID] = true
	}
	if !seen[publicDS.ID] || !seen[ownedDS.ID] || seen[otherDS.ID] {
		t.Fatalf("scoped: unexpected visibility: %v", seen)
	}

	// cross-tenant id load: must return gorm.ErrRecordNotFound (handler maps to 404)
	if _, err := repo.getDatasetByIDScoped(otherDS.ID, scoped); err == nil {
		t.Fatalf("scoped get of foreign dataset returned success, expected not-found")
	}
	// owned id load: must succeed
	if _, err := repo.getDatasetByIDScoped(ownedDS.ID, scoped); err != nil {
		t.Fatalf("scoped get of owned dataset failed: %v", err)
	}
}

