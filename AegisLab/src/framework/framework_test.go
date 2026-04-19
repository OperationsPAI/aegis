package framework

import (
	"context"
	"errors"
	"testing"

	"aegis/consts"
	"aegis/dto"
)

func TestFlattenMigrations(t *testing.T) {
	contribs := []MigrationRegistrar{
		{Module: "a", Entities: []interface{}{&struct{ A int }{}, &struct{ B int }{}}},
		{Module: "b", Entities: []interface{}{&struct{ C int }{}}},
	}
	out := FlattenMigrations(contribs)
	if len(out) != 3 {
		t.Fatalf("expected 3 entities, got %d", len(out))
	}
}

func TestMergeRoleGrants(t *testing.T) {
	rule := consts.PermissionRule{Resource: "x", Action: "y", Scope: "own"}
	contribs := []RoleGrantsRegistrar{
		{Module: "a", Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleUser: {rule},
		}},
		{Module: "b", Grants: map[consts.RoleName][]consts.PermissionRule{
			consts.RoleUser: {rule},
		}},
	}
	out := MergeRoleGrants(nil, contribs)
	if got := len(out[consts.RoleUser]); got != 2 {
		t.Fatalf("expected 2 grants on RoleUser, got %d", got)
	}
}

func TestBuildTaskExecutorRegistry(t *testing.T) {
	sentinel := errors.New("sentinel")
	fn := func(ctx context.Context, task *dto.UnifiedTask, deps any) error { return sentinel }
	contribs := []TaskExecutorRegistrar{
		{Module: "a", Executors: map[consts.TaskType]TaskExecutor{
			consts.TaskTypeBuildContainer: fn,
		}},
	}
	reg := BuildTaskExecutorRegistry(contribs)
	got, ok := reg[consts.TaskTypeBuildContainer]
	if !ok {
		t.Fatalf("expected executor registered for TaskTypeBuildContainer")
	}
	if err := got(context.Background(), &dto.UnifiedTask{}, nil); err != sentinel {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildTaskExecutorRegistryDuplicatesPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate TaskType registration")
		}
	}()
	fn := func(ctx context.Context, task *dto.UnifiedTask, deps any) error { return nil }
	BuildTaskExecutorRegistry([]TaskExecutorRegistrar{
		{Module: "a", Executors: map[consts.TaskType]TaskExecutor{consts.TaskTypeBuildContainer: fn}},
		{Module: "b", Executors: map[consts.TaskType]TaskExecutor{consts.TaskTypeBuildContainer: fn}},
	})
}
