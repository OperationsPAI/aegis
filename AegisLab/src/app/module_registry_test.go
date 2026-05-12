package app

// Framework-level aggregation tests. These replace ~40 tautological
// per-module "registrar shape" tests that just re-stated struct literals
// in permissions.go/routes.go/migrations.go. Each invariant below is a
// cross-module property that is NOT obvious from reading a single module
// file; verifying it at the aggregator boundary catches the silent-
// override/collision bugs that per-module shape tests cannot.

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/framework"
	buildkit "aegis/platform/buildkit"
	etcd "aegis/platform/etcd"
	harbor "aegis/platform/harbor"
	helm "aegis/platform/helm"
	"aegis/platform/jwtkeys"
	k8s "aegis/platform/k8s"
	loki "aegis/platform/loki"
	redisinfra "aegis/platform/redis"
	"aegis/module/ssoclient"
	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/fx"
	"gorm.io/gorm/schema"
)

// moduleRegistrySnapshot aggregates the four group-tagged outputs every
// module is expected to contribute via its fx.Module. It's captured via
// an fx.Invoke so we never need to actually start the app — the invoke
// fires at fx.New construction time.
type moduleRegistrySnapshot struct {
	Routes      []framework.RouteRegistrar
	Permissions []framework.PermissionRegistrar
	RoleGrants  []framework.RoleGrantsRegistrar
	Migrations  []framework.MigrationRegistrar
}

type moduleRegistryParams struct {
	fx.In

	Routes      []framework.RouteRegistrar      `group:"routes"`
	Permissions []framework.PermissionRegistrar `group:"permissions"`
	RoleGrants  []framework.RoleGrantsRegistrar `group:"role_grants"`
	Migrations  []framework.MigrationRegistrar  `group:"migrations"`
}

// allModuleOptions returns the generated list of HTTP-registering
// fx.Modules. This delegates to producerHTTPModules() (from the
// generated http_modules_gen.go) so the test automatically tracks
// additions/removals of modules without being edited — and so the
// TestProducerHTTPModuleRegistryAllowsDeletingOneModule scratch build
// still compiles after a module dir is removed.
func allModuleOptions() fx.Option {
	return fx.Options(producerHTTPModules()...)
}

// buildModuleRegistrySnapshot constructs an fx app spanning every
// HTTP-registering module with stubbed infra providers, captures the
// four group-tagged slices, and returns them. It also saves/restores
// consts.SystemRolePermissions because rbac.AggregatePermissions
// mutates it at invoke time.
func buildModuleRegistrySnapshot(t *testing.T) moduleRegistrySnapshot {
	t.Helper()

	// Snapshot and restore consts.SystemRolePermissions — rbac.Module's
	// fx.Invoke(AggregatePermissions) appends to it during fx.New.
	originalPerms := make(map[consts.RoleName][]consts.PermissionRule, len(consts.SystemRolePermissions))
	for role, rules := range consts.SystemRolePermissions {
		cp := make([]consts.PermissionRule, len(rules))
		copy(cp, rules)
		originalPerms[role] = cp
	}
	t.Cleanup(func() {
		consts.SystemRolePermissions = originalPerms
	})

	db := testutil.NewSQLiteGormDB(t)

	redisClient := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = redisClient.Close() })
	redisGateway := redisinfra.NewGateway(redisClient)

	etcdClient := &clientv3.Client{}
	etcdGateway := etcd.NewGateway(etcdClient)

	controller := &k8s.Controller{}
	k8sGateway := k8s.NewGateway(controller)

	var snapshot moduleRegistrySnapshot

	app := fx.New(
		fx.NopLogger,
		fx.Supply(
			db,
			redisGateway,
			redisClient,
			etcdGateway,
			etcdClient,
			&loki.Client{},
			controller,
			k8sGateway,
			harbor.NewGateway(),
			helm.NewGateway(),
			buildkit.NewGateway(),
		),
		allModuleOptions(),
		// Stub ssoclient.Client and JWT key types because the system / task
		// modules now depend on them. Real wiring lives in ssoclient.Module
		// and jwtkeys's Signer/Verifier modules respectively; we don't load
		// them here to keep the snapshot build offline.
		fx.Supply((*ssoclient.Client)(nil)),
		fx.Supply((*jwtkeys.Verifier)(nil)),
		fx.Supply((*jwtkeys.Signer)(nil)),
		fx.Provide(chaosSystemWriterAdapter),
		fx.Invoke(func(p moduleRegistryParams) {
			snapshot.Routes = p.Routes
			snapshot.Permissions = p.Permissions
			snapshot.RoleGrants = p.RoleGrants
			snapshot.Migrations = p.Migrations
		}),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("build module registry fx app: %v", err)
	}

	// Sanity: if the graph built but produced no contributions we're
	// inspecting an empty universe and every uniqueness assertion is
	// vacuous. Guard so the test can't silently no-op.
	if len(snapshot.Routes) == 0 {
		t.Fatal("registry snapshot has zero route registrars (aggregation is empty)")
	}
	if len(snapshot.Migrations) == 0 {
		t.Fatal("registry snapshot has zero migration registrars (aggregation is empty)")
	}

	return snapshot
}

// TestRegistryPermissionTriplesUnique asserts that the flattened list
// of (Resource, Action, Scope) across every module's
// framework.PermissionRegistrar contains no duplicates. Duplicate
// triples silently merge at role-grant time and make it unclear which
// module owns a permission — a real correctness hazard we want loud.
//
// Two invariants are enforced:
//  1. Within a single module, no triple appears twice (pure bug).
//  2. Across modules, if the same triple is declared by more than one
//     module it must be listed in knownSharedPermissionTriples —
//     otherwise we fail. The allowlist is the explicit catalog of
//     legitimate cross-module reuse (e.g. chaossystem reusing the
//     system-scope permission trio to gate /systems endpoints); new
//     duplicates must be argued for here.
func TestRegistryPermissionTriplesUnique(t *testing.T) {
	snap := buildModuleRegistrySnapshot(t)

	type triple struct {
		Resource consts.ResourceName
		Action   consts.ActionName
		Scope    consts.ResourceScope
	}

	// Intra-module duplicates — never legitimate.
	for _, reg := range snap.Permissions {
		seen := make(map[triple]int)
		for _, rule := range reg.Rules {
			seen[triple{Resource: rule.Resource, Action: rule.Action, Scope: rule.Scope}]++
		}
		for k, count := range seen {
			if count > 1 {
				t.Errorf("module %q declares (%s,%s,%s) %d times in its own Rules",
					reg.Module, k.Resource, k.Action, k.Scope, count)
			}
		}
	}

	// Cross-module duplicates — legitimate only if explicitly
	// allow-listed. The allowlist maps each shared triple to the set
	// of modules we expect to co-own it.
	knownSharedPermissionTriples := map[triple]map[string]bool{
		// chaossystem's /systems/* endpoints gate on the same
		// system-scope perms that the system module itself owns.
		// Re-declaring here (rather than importing the rule) keeps
		// the module self-contained for RBAC introspection tooling.
		{Resource: consts.ResourceSystem, Action: consts.ActionRead, Scope: consts.ScopeAll}:      {"system": true, "chaossystem": true},
		{Resource: consts.ResourceSystem, Action: consts.ActionConfigure, Scope: consts.ScopeAll}: {"system": true, "chaossystem": true},
		{Resource: consts.ResourceSystem, Action: consts.ActionManage, Scope: consts.ScopeAll}:    {"system": true, "chaossystem": true},
	}

	owners := make(map[triple]map[string]bool)
	for _, reg := range snap.Permissions {
		for _, rule := range reg.Rules {
			k := triple{Resource: rule.Resource, Action: rule.Action, Scope: rule.Scope}
			if owners[k] == nil {
				owners[k] = make(map[string]bool)
			}
			owners[k][reg.Module] = true
		}
	}

	var violations []string
	for k, modules := range owners {
		if len(modules) < 2 {
			continue
		}
		allowed := knownSharedPermissionTriples[k]
		if allowed == nil {
			violations = append(violations, fmt.Sprintf("(%s,%s,%s) declared by %v (no allowlist entry)",
				k.Resource, k.Action, k.Scope, keysOf(modules)))
			continue
		}
		// Every module that declares it must be in the allowlist; and
		// every allowlist module must have declared it (catches
		// stale entries).
		for m := range modules {
			if !allowed[m] {
				violations = append(violations, fmt.Sprintf("(%s,%s,%s) declared by %q which is not in the allowlist",
					k.Resource, k.Action, k.Scope, m))
			}
		}
		for m := range allowed {
			if !modules[m] {
				violations = append(violations, fmt.Sprintf("(%s,%s,%s) allowlist lists %q but that module no longer declares it",
					k.Resource, k.Action, k.Scope, m))
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("permission triple violations:\n  %s", joinLines(violations))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestRegistryRouteRegistrarNamesUnique asserts that every
// framework.RouteRegistrar contributed across modules has a unique
// Name. Name collisions are silent at mount time (last-wins in
// tracing/debug logs) and make it impossible to tell which module
// registered which route.
func TestRegistryRouteRegistrarNamesUnique(t *testing.T) {
	snap := buildModuleRegistrySnapshot(t)

	seen := make(map[string]int)
	for _, reg := range snap.Routes {
		if reg.Name == "" {
			t.Errorf("route registrar has empty Name (audience=%q)", reg.Audience)
			continue
		}
		seen[reg.Name]++
	}

	var dupes []string
	for name, count := range seen {
		if count > 1 {
			dupes = append(dupes, fmt.Sprintf("%q appears %d times", name, count))
		}
	}
	if len(dupes) > 0 {
		sort.Strings(dupes)
		t.Fatalf("duplicate RouteRegistrar names:\n  %s", joinLines(dupes))
	}
}

// TestRegistryRoutePathsUniquePerAudience mounts every
// framework.RouteRegistrar against a fresh gin.Engine (one per audience
// bucket) and asserts that no (method, path) tuple is registered
// twice. Gin accepts duplicate routes with last-wins semantics, which
// silently shadows handlers across modules.
func TestRegistryRoutePathsUniquePerAudience(t *testing.T) {
	snap := buildModuleRegistrySnapshot(t)

	gin.SetMode(gin.TestMode)

	byAudience := make(map[framework.Audience][]framework.RouteRegistrar)
	for _, reg := range snap.Routes {
		byAudience[reg.Audience] = append(byAudience[reg.Audience], reg)
	}

	for _, audience := range []framework.Audience{
		framework.AudiencePublic,
		framework.AudienceSDK,
		framework.AudiencePortal,
		framework.AudienceAdmin,
	} {
		regs := byAudience[audience]
		if len(regs) == 0 {
			continue
		}

		engine := gin.New()
		v2 := engine.Group("/api/v2")
		for _, reg := range regs {
			reg.Register(v2)
		}

		type route struct {
			Method string
			Path   string
		}
		seen := make(map[route]int)
		for _, r := range engine.Routes() {
			seen[route{Method: r.Method, Path: r.Path}]++
		}

		var dupes []string
		for r, count := range seen {
			if count > 1 {
				dupes = append(dupes, fmt.Sprintf("%s %s appears %d times", r.Method, r.Path, count))
			}
		}
		if len(dupes) > 0 {
			sort.Strings(dupes)
			t.Errorf("duplicate routes on audience %q:\n  %s", audience, joinLines(dupes))
		}
	}
}

// TestRegistryMigrationsAutoMigrate asserts that every entity
// contributed via group:"migrations" has a valid GORM schema. This
// catches "forgot to add a tag" / "model has a broken embedded
// struct" issues at the framework boundary, which previously only
// surfaced in production when GORM hit the entity.
//
// We validate via schema.Parse rather than db.AutoMigrate because the
// real DB schema uses MySQL-specific idioms (e.g. the same
// `uniqueIndex:idx_active_version_unique` name is reused on both
// container_versions and dataset_versions — MySQL scopes index names
// per table, SQLite scopes them per schema). Parsing catches tag
// errors and invalid field types without tripping over that
// MySQL-vs-SQLite difference, and the testutil.NewSQLiteGormDB
// provides the schema cache context so the parse stays faithful to
// what GORM actually does at AutoMigrate time.
func TestRegistryMigrationsAutoMigrate(t *testing.T) {
	snap := buildModuleRegistrySnapshot(t)

	entities := framework.FlattenMigrations(snap.Migrations)
	if len(entities) == 0 {
		t.Fatal("no entities to migrate")
	}

	// Grab the naming strategy the production DB uses so parsed
	// table/column names match what AutoMigrate would produce.
	db := testutil.NewSQLiteGormDB(t)
	namer := db.NamingStrategy
	cache := &sync.Map{}

	for i, entity := range entities {
		owner := findMigrationOwner(snap.Migrations, i)
		s, err := schema.Parse(entity, cache, namer)
		if err != nil {
			t.Errorf("schema.Parse entity %T (module %q) failed: %v", entity, owner, err)
			continue
		}
		// Every registered entity must name a table; otherwise GORM
		// would migrate it as an empty string and silently no-op.
		if s.Table == "" {
			t.Errorf("entity %T (module %q) parsed to empty table name", entity, owner)
		}
		if len(s.Fields) == 0 {
			t.Errorf("entity %T (module %q) parsed to zero fields", entity, owner)
		}
	}
}

// findMigrationOwner returns the Module that contributed the entity at
// the given flattened index. Linear scan — entity counts are small.
func findMigrationOwner(contribs []framework.MigrationRegistrar, flatIdx int) string {
	cursor := 0
	for _, c := range contribs {
		next := cursor + len(c.Entities)
		if flatIdx < next {
			return c.Module
		}
		cursor = next
	}
	return "?"
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}
