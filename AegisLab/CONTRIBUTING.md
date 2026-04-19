# Contributing to AegisLab

This document captures the rules that keep the codebase maintainable as
more modules are added. The most important one:

> **Adding a new module must NOT require editing any other module's
> source code.**

## Module self-registration (Phase 3+)

Every AegisLab module lives in `src/module/<name>/` and contributes to
five framework plugin points via `fx` value-groups. The aggregation
sites iterate these groups at startup, so adding a module never means
editing a central list.

Plugin points (defined in `src/framework/`):

| Group               | Type                         | Consumed by                      |
| ------------------- | ---------------------------- | -------------------------------- |
| `routes`            | `framework.RouteRegistrar`   | `router.New`                     |
| `permissions`       | `framework.PermissionRegistrar` | `module/rbac.AggregatePermissions` |
| `role_grants`       | `framework.RoleGrantsRegistrar` | `module/rbac.AggregatePermissions` |
| `migrations`        | `framework.MigrationRegistrar` | `infra/db.NewGormDB`             |
| `task_executors`    | `framework.TaskExecutorRegistrar` | `service/consumer.NewTaskRegistry` |

Middleware is NOT a plugin point — it is global policy, centralized
intentionally (per issue #28).

### Reference module: `label`

The `label` module is the Phase 3 reference migration. Copy its layout
when migrating a new module in Phase 4:

```
src/module/label/
  module.go         # fx.Module wiring + fx.Annotate ResultTags for each group
  routes.go         # Routes() returns framework.RouteRegistrar
  permissions.go    # Permissions() + RoleGrants()
  migrations.go     # Migrations() returns framework.MigrationRegistrar
  handler.go        # HTTP handlers (unchanged from Phase 2)
  handler_service.go
  repository.go
  service.go
  core.go           # optional: low-level repo operations
  api_types.go
```

`module.go` shape:

```go
var Module = fx.Module("label",
    fx.Provide(NewRepository),
    fx.Provide(NewService),
    fx.Provide(AsHandlerService),
    fx.Provide(NewHandler),
    fx.Provide(
        fx.Annotate(Routes,       fx.ResultTags(`group:"routes"`)),
        fx.Annotate(Permissions,  fx.ResultTags(`group:"permissions"`)),
        fx.Annotate(RoleGrants,   fx.ResultTags(`group:"role_grants"`)),
        fx.Annotate(Migrations,   fx.ResultTags(`group:"migrations"`)),
    ),
)
```

### Routes audience

`framework.RouteRegistrar` carries an `Audience` tag
(`AudiencePublic | AudienceSDK | AudiencePortal | AudienceAdmin`)
matching the pre-Phase-3 file split in `router/`. A module that exposes
routes to multiple audiences (e.g. `injection` has portal + sdk)
contributes **one `RouteRegistrar` per audience** from separate
constructors, all tagged `group:"routes"`.

## Cross-module access rule

When module A needs data that module B owns:

- ✅ **Module A imports `B.Reader` / `B.Writer` interfaces** (defined
  in `module/b/reader.go` or `module/b/writer.go`) and calls methods
  via the interface.
- ✅ Module B provides the concrete implementation via
  `fx.Provide(fx.As(new(Reader)))` or `fx.Provide(AsReader)`.
- ❌ **Never import `module/b/repository.go` directly** from A.
- ❌ **Never run SQL against another module's tables** from A.

This keeps the dependency graph a DAG even when modules grow. The
Phase 4 migration for each module includes extracting its `Reader` /
`Writer` interfaces; see `module/execution/`, `module/injection/` for
examples already in place (their `ExecutionOwner` / `InjectionOwner`
interfaces in `service/consumer/owner_adapter.go` are the pattern —
generalized to `Reader` / `Writer` once Phase 4 lands).

### Example

```go
// module/dataset/reader.go  (provider side)
type Reader interface {
    GetDataset(ctx context.Context, id int) (*DatasetItem, error)
}

func AsReader(s *Service) Reader { return s }

// module/dataset/module.go
var Module = fx.Module("dataset",
    fx.Provide(NewService),
    fx.Provide(AsReader),  // exposes interface; concrete type stays private
    ...
)

// module/execution/service.go  (consumer side)
type Service struct {
    datasetReader dataset.Reader
    ...
}
```

## Phase 3 coexistence

During Phase 3 and 4, the central lists still exist as a baseline:

- `router/{public,sdk,portal,admin}.go`
- `consts/system.go` — `Perm*` vars + `SystemRolePermissions`
- `infra/db/migration.go` — `centralEntities()`

Each Phase 4 PR migrates ONE module by:

1. Creating `module/<name>/routes.go`, `permissions.go`, `migrations.go`
   (copy the `label` module layout).
2. Wiring them via `fx.Annotate(..., fx.ResultTags(...))` in the
   module's `module.go`.
3. **Removing** the corresponding entries from the central files (the
   routes group in `router/*.go`, the role entries in `SystemRolePermissions`,
   the entity in `centralEntities()`).
4. Keeping the `Perm*` vars in `consts/system.go` if middleware still
   references them.

The PR should compile and pass tests at every commit. `go build`, `go vet`,
and `go test ./router/... ./infra/db/... ./module/<name>/...` must stay
green.

## Verification

Before opening a PR:

```bash
cd src
go build -tags duckdb_arrow ./...
go vet -tags duckdb_arrow ./...
go test -tags duckdb_arrow ./module/<name>/... ./router/... ./service/consumer/... ./infra/db/...
```

If you touched HTTP behavior, also run the startup smoke test:

```bash
docker compose up redis mysql -d
ENV_MODE=dev go run -tags duckdb_arrow ./main.go both --port 8082
```
