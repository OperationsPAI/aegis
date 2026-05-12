package dataset

import (
	"testing"

	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/model"
)

func TestMigrationsRegistrar(t *testing.T) {
	reg := Migrations()
	if reg.Module != "dataset" {
		t.Fatalf("expected module name 'dataset', got %q", reg.Module)
	}
	expected := map[string]bool{
		"*model.Dataset":                 false,
		"*model.DatasetVersion":          false,
		"*model.DatasetLabel":            false,
		"*model.DatasetVersionInjection": false,
	}
	for _, e := range reg.Entities {
		switch e.(type) {
		case *model.Dataset:
			expected["*model.Dataset"] = true
		case *model.DatasetVersion:
			expected["*model.DatasetVersion"] = true
		case *model.DatasetLabel:
			expected["*model.DatasetLabel"] = true
		case *model.DatasetVersionInjection:
			expected["*model.DatasetVersionInjection"] = true
		default:
			t.Errorf("unexpected entity type: %T", e)
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing entity: %s", name)
		}
	}
}

func TestPermissionsRegistrar(t *testing.T) {
	reg := Permissions()
	if reg.Module != "dataset" {
		t.Fatalf("expected module name 'dataset', got %q", reg.Module)
	}
	if len(reg.Rules) == 0 {
		t.Fatal("expected non-empty permission rules")
	}
	ruleSet := make(map[consts.PermissionRule]bool, len(reg.Rules))
	for _, r := range reg.Rules {
		ruleSet[r] = true
	}
	required := []consts.PermissionRule{
		consts.PermDatasetReadAll,
		consts.PermDatasetCreateAll,
		consts.PermDatasetUpdateAll,
		consts.PermDatasetDeleteAll,
		consts.PermDatasetManageAll,
		consts.PermDatasetVersionReadAll,
		consts.PermDatasetVersionCreateAll,
		consts.PermDatasetVersionUpdateAll,
		consts.PermDatasetVersionDeleteAll,
		consts.PermDatasetVersionDownloadAll,
	}
	for _, r := range required {
		if !ruleSet[r] {
			t.Errorf("missing required permission rule: %+v", r)
		}
	}
}

func TestRoleGrantsRegistrar(t *testing.T) {
	reg := RoleGrants()
	if reg.Module != "dataset" {
		t.Fatalf("expected module name 'dataset', got %q", reg.Module)
	}
	requiredRoles := []consts.RoleName{
		consts.RoleAdmin,
		consts.RoleUser,
		consts.RoleDatasetAdmin,
		consts.RoleDatasetDeveloper,
		consts.RoleDatasetViewer,
	}
	for _, role := range requiredRoles {
		if _, ok := reg.Grants[role]; !ok {
			t.Errorf("missing role grants for role: %s", role)
		}
	}
}

func TestRoleGrantsMergesIntoFramework(t *testing.T) {
	reg := RoleGrants()
	merged := framework.MergeRoleGrants(nil, []framework.RoleGrantsRegistrar{reg})
	if len(merged[consts.RoleAdmin]) == 0 {
		t.Fatal("expected admin role grants after merge")
	}
	if len(merged[consts.RoleUser]) == 0 {
		t.Fatal("expected user role grants after merge")
	}
}

func TestMigrationsFlattenIntoFramework(t *testing.T) {
	reg := Migrations()
	flat := framework.FlattenMigrations([]framework.MigrationRegistrar{reg})
	if len(flat) != 4 {
		t.Fatalf("expected 4 flattened entities, got %d", len(flat))
	}
}

func TestRoutesPortalRegistrar(t *testing.T) {
	// We can't call RoutesPortal without a *Handler, but we can verify
	// the function signature is compatible with the framework contract
	// by checking the type at compile time.
	var _ func(*Handler) framework.RouteRegistrar = RoutesPortal
}

func TestRoutesSDKRegistrar(t *testing.T) {
	var _ func(*Handler) framework.RouteRegistrar = RoutesSDK
}

func TestReaderInterfaceSatisfaction(t *testing.T) {
	var _ Reader = (*Repository)(nil)
}

func TestWriterInterfaceSatisfaction(t *testing.T) {
	var _ Writer = (*Repository)(nil)
}
