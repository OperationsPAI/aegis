# Contributing to aegislab

This document captures the rules that keep the codebase maintainable as
more modules are added.

> **Adding a new module must NOT require editing any other module's
> source code.**

Read [`README.md`](README.md) first for the repo layout and design
model. This doc only covers the *rules* and the per-PR checklist.

## Module self-registration

Modules live in either `src/core/domain/<name>/` (business pipeline
data) or `src/crud/<group>/<name>/` (supporting CRUD). Every module
exposes a single `fx.Module` and contributes to framework plugin points
via `fx` value-groups. Aggregation sites iterate those groups at
startup — adding a module never means editing a central list.

Plugin points (defined in `src/platform/framework/`):

| Group               | Type                                | Consumed by                              |
| ------------------- | ----------------------------------- | ---------------------------------------- |
| `routes`            | `framework.RouteRegistrar`          | `platform/router.New`                    |
| `permissions`       | `framework.PermissionRegistrar`     | `crud/iam/rbac.AggregatePermissions`     |
| `role_grants`       | `framework.RoleGrantsRegistrar`     | `crud/iam/rbac.AggregatePermissions`     |
| `migrations`        | `framework.MigrationRegistrar`      | `platform/db.NewGormDB`                  |
| `task_executors`    | `framework.TaskExecutorRegistrar`   | `core/orchestrator/common.NewTaskRegistry` |

Middleware is **not** a plugin point — it's global policy, centralized
intentionally.

### Reference module

`src/crud/iam/label/` is the smallest end-to-end example. Copy its
layout:

```
src/crud/iam/label/
  module.go         # fx.Module + fx.Annotate ResultTags
  routes.go         # Routes() returns framework.RouteRegistrar
  permissions.go    # Permissions() + RoleGrants()
  migrations.go     # Migrations() returns framework.MigrationRegistrar
  handler.go        # HTTP handlers
  handler_service.go
  repository.go
  service.go
  api_types.go
```

`module.go` shape:

```go
var Module = fx.Module("label",
    fx.Provide(NewRepository, NewService, NewHandler),
    fx.Provide(AsHandlerService),
    fx.Provide(
        fx.Annotate(Routes,       fx.ResultTags(`group:"routes"`)),
        fx.Annotate(Permissions,  fx.ResultTags(`group:"permissions"`)),
        fx.Annotate(RoleGrants,   fx.ResultTags(`group:"role_grants"`)),
        fx.Annotate(Migrations,   fx.ResultTags(`group:"migrations"`)),
    ),
)
```

### Route audiences

`framework.RouteRegistrar` carries an `Audience` field
(`AudiencePublic | AudienceSDK | AudiencePortal | AudienceAdmin`). A
module that exposes routes to multiple audiences (e.g.
`core/domain/injection` has portal + sdk) contributes **one
`RouteRegistrar` per audience** from separate constructors, all tagged
`group:"routes"`.

### After creating a module

Regenerate the HTTP module index so the monolith/aegis-api picks it up:

```bash
python3 src/scripts/generate_http_modules.py
```

That rewrites `src/boot/http_modules_gen.go`. **Never hand-edit that
file** — it walks the directory tree.

If a split-process binary (e.g. `cmd/aegis-gateway`, `cmd/aegis-blob`)
also needs the module, add it to that binary's
`src/boot/<role>/options.go` Options list.

## Cross-module access rule

When module A needs data that module B owns:

- ✅ Module A imports `B.Reader` / `B.Writer` interfaces (defined in
  `b/reader.go` / `b/writer.go`) and calls methods through the
  interface.
- ✅ Module B provides the concrete implementation via
  `fx.Provide(AsReader)` / `fx.Provide(AsWriter)` so the consumer
  binds the interface, not the struct.
- ❌ Never import another module's `repository.go` directly.
- ❌ Never run SQL against another module's tables.

This keeps the dependency graph a DAG. See `core/domain/injection`
(`InjectionWriter`) and `core/domain/execution` (`ExecutionOwner`) for
the pattern, and `core/orchestrator/owner_adapter.go` for how the
orchestrator consumes those interfaces.

### Example

```go
// core/domain/dataset/reader.go  (provider)
type Reader interface {
    GetDataset(ctx context.Context, id int) (*DatasetItem, error)
}

func AsReader(s *Service) Reader { return s }

// core/domain/dataset/module.go
var Module = fx.Module("dataset",
    fx.Provide(NewService),
    fx.Provide(AsReader),       // exposes interface; concrete type stays internal
    ...
)

// core/domain/execution/service.go  (consumer)
type Service struct {
    datasetReader dataset.Reader
    ...
}
```

## Layer rules

`src/core/domain/boundary_test.go` enforces import direction at CI
time. The rule is:

```
cmd       -> boot, clients, platform        (thin main only)
boot      -> core, crud, clients, platform  (wiring only)
core      -> clients, platform              (no boot, no cmd)
crud      -> clients, platform              (no boot, no cmd, no core)
clients   -> platform                       (leaf clients)
platform  -> (no internal deps)             (framework + infra)
```

`core` and `crud` are siblings; neither imports the other. If you find
yourself wanting to, the dependency probably belongs in `platform/dto`
or in a shared `Reader/Writer` interface.

## Verification

Before opening a PR, the workspace-level gates from `../CLAUDE.md`
apply:

```bash
cd src
go build -tags duckdb_arrow -o /dev/null ./main.go
golangci-lint run
go test ./platform/... -count=1
go test ./boot       -count=1
go test ./core/domain -run TestDomainAvoidsBootImports -count=1
```

If you changed any module that contributes to plugin groups, also:

```bash
python3 src/scripts/generate_http_modules.py
git diff --exit-code src/boot/http_modules_gen.go
```

For HTTP behavior changes, run the dev-mode smoke:

```bash
docker compose up -d redis mysql etcd
cd src
go run -tags duckdb_arrow . both -conf ./config.dev.toml -port 8082
```

For deployment-affecting changes (chart, manifests, SSO/auth wiring):

```bash
just sso-keys                  # one-time
skaffold run -p local
```

## Schema-diff gate

Any change to `aegisctl`'s CLI surface (flags, command tree, help
text) is detected by `.github/workflows/aegisctl-schema-diff.yml`. If
the gate fires, add `schema-changes-acknowledged: true` to the PR body
to acknowledge the change and re-trigger CI (an empty commit or a body
edit both work).

## Commits

- Conventional commits: `feat(scope):`, `fix(scope):`, `refactor(scope):`,
  `chore(scope):`.
- Comments default to none. Add one only when the *why* is non-obvious
  (a hidden constraint, a specific bug workaround). Never explain
  *what* the code does, and never reference the PR / issue / caller in
  source comments — that belongs in the commit message.
- Tests: be skeptical of every new `*_test.go`. The bar is "this
  catches a bug that other tests don't", not "this exercises code
  path X". Prefer deleting weak tests over adding new ones.
