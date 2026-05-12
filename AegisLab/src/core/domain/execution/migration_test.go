package execution

import (
	"testing"

	"aegis/platform/consts"
	"aegis/platform/framework"

	"github.com/stretchr/testify/require"
)

func TestRoutesPortalReturnsPortalAudience(t *testing.T) {
	reg := RoutesPortal(nil)

	require.Equal(t, framework.AudiencePortal, reg.Audience)
	require.NotEmpty(t, reg.Name)
	require.NotNil(t, reg.Register)
}

func TestRoutesSDKReturnsSDKAudience(t *testing.T) {
	reg := RoutesSDK(nil)

	require.Equal(t, framework.AudienceSDK, reg.Audience)
	require.NotEmpty(t, reg.Name)
	require.NotNil(t, reg.Register)
}

func TestPermissionsReturnsCorrectModule(t *testing.T) {
	reg := Permissions()

	require.Equal(t, "execution", reg.Module)
	require.NotEmpty(t, reg.Rules)

	// Verify all rules reference the execution resource.
	for _, rule := range reg.Rules {
		require.Equal(t, consts.ResourceExecution, rule.Resource,
			"expected permission rule to reference execution resource")
	}
}

func TestPermissionsContainsExpectedRules(t *testing.T) {
	reg := Permissions()

	expected := []consts.PermissionRule{
		consts.PermExecutionReadProject,
		consts.PermExecutionCreateProject,
		consts.PermExecutionUpdateProject,
		consts.PermExecutionDeleteProject,
		consts.PermExecutionExecuteProject,
		consts.PermExecutionStopProject,
	}
	require.ElementsMatch(t, expected, reg.Rules)
}

func TestRoleGrantsReturnsCorrectModule(t *testing.T) {
	reg := RoleGrants()

	require.Equal(t, "execution", reg.Module)
	require.NotEmpty(t, reg.Grants)
}

func TestRoleGrantsContainsAdminRole(t *testing.T) {
	reg := RoleGrants()

	adminGrants, ok := reg.Grants[consts.RoleAdmin]
	require.True(t, ok, "expected grants for RoleAdmin")
	require.NotEmpty(t, adminGrants)
}

func TestRoleGrantsAdminHasAllPermissions(t *testing.T) {
	grants := RoleGrants()
	perms := Permissions()

	adminGrants := grants.Grants[consts.RoleAdmin]
	require.ElementsMatch(t, perms.Rules, adminGrants,
		"admin role should have all execution permissions")
}

func TestRoleGrantsViewerHasReadOnly(t *testing.T) {
	reg := RoleGrants()

	viewerGrants, ok := reg.Grants[consts.RoleProjectViewer]
	require.True(t, ok, "expected grants for RoleProjectViewer")
	require.Equal(t, []consts.PermissionRule{consts.PermExecutionReadProject}, viewerGrants)
}

func TestMigrationsReturnsCorrectModule(t *testing.T) {
	reg := Migrations()

	require.Equal(t, "execution", reg.Module)
	require.NotEmpty(t, reg.Entities)
}

func TestMigrationsContainsFourEntities(t *testing.T) {
	reg := Migrations()

	require.Len(t, reg.Entities, 4)
	for i, entity := range reg.Entities {
		require.NotNil(t, entity, "entity at index %d should not be nil", i)
	}
}

func TestServiceSatisfiesReaderInterface(t *testing.T) {
	var _ Reader = (*Service)(nil)
}

func TestServiceSatisfiesWriterInterface(t *testing.T) {
	var _ Writer = (*Service)(nil)
}

func TestAsReaderReturnsNonNil(t *testing.T) {
	svc := &Service{}
	result := AsReader(svc)

	require.NotNil(t, result)
	require.Same(t, svc, result)
}

func TestAsWriterReturnsNonNil(t *testing.T) {
	svc := &Service{}
	result := AsWriter(svc)

	require.NotNil(t, result)
	require.Same(t, svc, result)
}
