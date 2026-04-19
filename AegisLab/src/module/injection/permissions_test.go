package injection

import (
	"testing"

	"aegis/consts"
)

func TestRoleGrantsPreserveCentralizedInjectionBehavior(t *testing.T) {
	grants := RoleGrants()

	adminRules, ok := grants.Grants[consts.RoleAdmin]
	if !ok {
		t.Fatal("expected RoleAdmin injection grants")
	}

	expectedAdmin := []consts.PermissionRule{
		consts.PermInjectionReadProject,
		consts.PermInjectionCreateProject,
		consts.PermInjectionUpdateProject,
		consts.PermInjectionDeleteProject,
		consts.PermInjectionExecuteProject,
		consts.PermInjectionCloneProject,
		consts.PermInjectionDownloadProject,
	}

	if len(adminRules) != len(expectedAdmin) {
		t.Fatalf("expected %d admin injection grants, got %d", len(expectedAdmin), len(adminRules))
	}

	for i := range expectedAdmin {
		if adminRules[i] != expectedAdmin[i] {
			t.Fatalf("unexpected admin grant at index %d: got %v want %v", i, adminRules[i], expectedAdmin[i])
		}
	}

	if len(grants.Grants) != 1 {
		t.Fatalf("expected only admin injection grants from the centralized migration, got %d roles", len(grants.Grants))
	}
}
