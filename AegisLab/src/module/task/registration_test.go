package task

import (
	"testing"

	"aegis/consts"
	"aegis/framework"
	"aegis/model"

	"github.com/stretchr/testify/require"
)

func TestPermissions(t *testing.T) {
	p := Permissions()
	require.Equal(t, "task", p.Module)
	require.NotEmpty(t, p.Rules)

	expected := []consts.PermissionRule{
		consts.PermTaskReadAll,
		consts.PermTaskCreateAll,
		consts.PermTaskUpdateAll,
		consts.PermTaskDeleteAll,
		consts.PermTaskExecuteAll,
		consts.PermTaskStopAll,
	}
	require.Equal(t, expected, p.Rules)
}

func TestRoleGrants(t *testing.T) {
	g := RoleGrants()
	require.Equal(t, "task", g.Module)
	require.Contains(t, g.Grants, consts.RoleAdmin)
	require.Len(t, g.Grants[consts.RoleAdmin], 6)
}

func TestRoleGrantsMatchPermissions(t *testing.T) {
	p := Permissions()
	g := RoleGrants()
	adminGrants := g.Grants[consts.RoleAdmin]
	for _, rule := range p.Rules {
		require.Contains(t, adminGrants, rule, "admin should have all task permissions")
	}
}

func TestMigrations(t *testing.T) {
	m := Migrations()
	require.Equal(t, "task", m.Module)
	require.Len(t, m.Entities, 1)
	_, ok := m.Entities[0].(*model.Task)
	require.True(t, ok, "migration entity should be *model.Task")
}

func TestMigrationsIntegrateWithFramework(t *testing.T) {
	m := Migrations()
	entities := framework.FlattenMigrations([]framework.MigrationRegistrar{m})
	require.Len(t, entities, 1)
}

func TestRoutesPortalReturnsCorrectRegistrar(t *testing.T) {
	r := RoutesPortal(nil)
	require.Equal(t, framework.AudiencePortal, r.Audience)
	require.Equal(t, "task", r.Name)
	require.NotNil(t, r.Register)
}
