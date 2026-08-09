package container

import (
	"strconv"
	"testing"

	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestContainerScope_PredicateEnforced guards the PR 4a retrofit: a non-admin
// caller must only see containers that are public OR explicitly granted via
// user_scoped_roles. Before the retrofit listContainers returned the full
// table regardless of caller identity (code review on container/repository.go).
func TestContainerScope_PredicateEnforced(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Container{}, &model.UserScopedRole{}); err != nil {
		t.Skipf("sqlite AutoMigrate unsupported: %v", err)
	}

	mkC := func(name string, public bool) *model.Container {
		c := &model.Container{
			Name:     name,
			Type:     consts.ContainerTypeBenchmark,
			IsPublic: public,
			Status:   consts.CommonEnabled,
		}
		if err := db.Omit(containerCommonOmitFields, containerModelOmitFields).Create(c).Error; err != nil {
			t.Fatalf("seed container %s: %v", name, err)
		}
		return c
	}

	publicC := mkC("public-c", true)
	ownedC := mkC("owned-c", false)
	otherC := mkC("other-c", false)

	const callerID int64 = 42
	if err := db.Create(&model.UserScopedRole{
		UserID:    int(callerID),
		RoleID:    1,
		ScopeType: consts.ScopeTypeContainer,
		ScopeID:   strconv.Itoa(ownedC.ID),
		Status:    consts.CommonEnabled,
	}).Error; err != nil {
		t.Fatalf("seed user_scoped_role: %v", err)
	}

	repo := NewRepository(db)

	// admin scope: sees all three
	all, total, err := repo.listContainers(authz.SystemScope(), 100, 0, nil, nil, nil)
	if err != nil {
		t.Fatalf("admin listContainers: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("admin: expected 3 containers, got total=%d len=%d", total, len(all))
	}

	// scoped non-admin: sees public + owned only
	scoped := authz.CallerScope{UserID: callerID}
	visible, total, err := repo.listContainers(scoped, 100, 0, nil, nil, nil)
	if err != nil {
		t.Fatalf("scoped listContainers: %v", err)
	}
	if total != 2 || len(visible) != 2 {
		t.Fatalf("scoped: expected 2 containers, got total=%d len=%d", total, len(visible))
	}
	seen := map[int]bool{}
	for _, c := range visible {
		seen[c.ID] = true
	}
	if !seen[publicC.ID] || !seen[ownedC.ID] || seen[otherC.ID] {
		t.Fatalf("scoped: unexpected visibility: %v", seen)
	}

	// cross-tenant id load: must return gorm.ErrRecordNotFound (handler maps to 404)
	if _, err := repo.getContainerByIDScoped(otherC.ID, scoped); err == nil {
		t.Fatalf("scoped get of foreign container returned success, expected not-found")
	}
	// owned id load: must succeed
	if _, err := repo.getContainerByIDScoped(ownedC.ID, scoped); err != nil {
		t.Fatalf("scoped get of owned container failed: %v", err)
	}
}
