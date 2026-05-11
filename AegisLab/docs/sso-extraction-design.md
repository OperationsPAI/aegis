# Aegis SSO Extraction — Design

Status: design (PR-1)
Author: 2026-05-11

This doc is the contract for splitting AegisLab's `module/{user,auth,rbac}` into
a standalone `aegis-sso` service. Every other PR-1 task references this file
for table shapes, API shapes, and module boundaries.

## 1. Goals & non-goals

**Goals (PR-1):**
1. `aegis-sso` runs as a separate process inside the AegisLab monorepo
   (`cmd/aegis-sso`), exposing OIDC + admin REST.
2. AegisLab backend consumes SSO via HTTP only; never reads SSO-owned tables
   directly.
3. Any other internal service (any language) can authenticate users and check
   permissions by speaking OIDC + 3 REST endpoints.
4. JWT tokens are RS256-signed; consumers verify locally via JWKS — SSO is not
   on the critical path of every request.

**Non-goals (deferred to later PRs):**
- Physical DB split. PR-1 keeps a single MySQL instance; SSO and AegisLab
  agree by convention which tables each owns.
- Multi-tenant / org hierarchy in OIDC.
- Webhook-based permission invalidation. PR-1 ships 30s LRU cache; if you
  revoke a role, downstream sees it within 30s.

## 2. Table ownership

**SSO-owned tables** (only `aegis-sso` process writes; only SSO repository
code reads):

| Table | Purpose |
|---|---|
| `users` | identity |
| `api_keys` | personal-access tokens |
| `oidc_clients` (**new**) | OIDC client registrations |
| `roles` | named role |
| `permissions` | named permission, scoped to a `service` + `scope_type` |
| `role_permissions` | role → permission |
| `user_roles` | global role grants |
| `user_scoped_roles` (**new, replaces 4 tables**) | scoped role grants |

**Tables collapsed → `user_scoped_roles`**: `user_project_roles`,
`user_team_roles`, `user_container_roles`, `user_dataset_roles` are all
deleted. They're replaced with:

```go
type UserScopedRole struct {
    ID        int               `gorm:"primaryKey;autoIncrement"`
    UserID    int               `gorm:"not null;index:idx_usr_scope"`
    RoleID    int               `gorm:"not null;index:idx_usr_scope"`
    ScopeType string            `gorm:"not null;size:64;index:idx_usr_scope"` // "aegis.project" | "aegis.team" | "aegis.container" | "aegis.dataset" | future "yourservice.workspace" | ...
    ScopeID   string            `gorm:"not null;size:64;index:idx_usr_scope"` // business-ID as string (handles non-int IDs from other services)
    Status    consts.StatusType `gorm:"not null;default:1;index"`
    CreatedAt time.Time         `gorm:"autoCreateTime"`
    UpdatedAt time.Time         `gorm:"autoUpdateTime"`
    // unique active grant
    Active string `gorm:"type:varchar(160) GENERATED ALWAYS AS (CASE WHEN status >= 0 THEN CONCAT(user_id,':',role_id,':',scope_type,':',scope_id) ELSE NULL END) VIRTUAL;uniqueIndex:idx_active_user_scoped_role"`
}
```

`WorkspaceConfig` (currently on `UserProject`) does NOT belong here — it's
AegisLab business data. Move it to a new AegisLab table
`user_project_workspaces(user_id, project_id, config_json)` owned by AegisLab.

**Permissions table — new `Service` column**: distinguishes which downstream
"owns" the permission, so multiple services can register permissions without
name collisions:

```go
type Permission struct {
    ...
    Service   string `gorm:"not null;size:64;default:'aegis';index"` // "aegis" | "yourservice" | ...
    ScopeType string `gorm:"size:64;index"` // empty = global; else "aegis.project" / etc
    // drop Scope (consts.ResourceScope), ResourceID, Resource — superseded by Service+ScopeType
}
```

**AegisLab-owned business tables**: keep `UserID int` / `RoleID int` integer
columns, but drop the GORM `*User` / `*Role` preload pointers in
`model/entity.go`. Compile-time enforcement that business code can no longer
JOIN on user/role.

## 3. JWT — RS256 + JWKS

**Migration**: drop HS256 shared secret in `utils/jwt.go`. SSO signs with
RSA private key (PEM, mounted as k8s Secret / file). All consumers fetch the
public key from `GET /.well-known/jwks.json` at startup, refresh every 10min.

**Two token kinds, same RS256 signer:**

```jsonc
// User access token (audience=aegis-backend or per-client)
{
  "iss": "https://sso.aegis.local",
  "sub": "42",                 // user_id (string per OIDC spec)
  "aud": ["aegis-backend"],
  "exp": 1700000000,
  "iat": 1699996400,
  "username": "alice",
  "email": "alice@example.com",
  "roles": ["admin", "user"],  // global role names (NOT scoped roles)
  "is_active": true,
  "is_admin": false,
  "token_type": "access"
}

// Service token (machine-to-machine, audience=aegis-sso for management API)
{
  "iss": "https://sso.aegis.local",
  "sub": "service:aegis-backend",
  "aud": ["aegis-sso"],
  "exp": ...,
  "service": "aegis-backend",
  "scopes": ["users:read", "check"],
  "token_type": "service"
}
```

**Claims do not include scoped roles** — those are too volatile and too many.
For scoped checks, consumers call `POST /v1/check`.

## 4. OIDC endpoints (port 8083)

Implemented with `github.com/zitadel/oidc/v3`:

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/openid-configuration` | discovery |
| `GET /.well-known/jwks.json` | public keys |
| `GET /authorize` | login UI + auth code |
| `POST /token` | code → token, refresh, client_credentials |
| `GET /userinfo` | profile from access token |
| `POST /v1/logout` | revoke session |

`oidc_clients` seeded at startup: `aegis-backend` (authorization_code) and one
client_credentials entry per service that needs to call /v1/check.

## 5. Admin REST API (`/v1/*`)

All `/v1/*` require a service_token (client_credentials JWT,
`aud=aegis-sso`). Returned as standard `{data, error}` envelope to match
existing AegisLab conventions.

### 5.1 `GET /v1/users/{id}`

```jsonc
// 200
{
  "data": {
    "id": 42, "username": "alice", "email": "alice@x.com",
    "full_name": "Alice", "avatar": "...", "is_active": true,
    "status": 1, "created_at": "2026-...", "last_login_at": "2026-..."
  }
}
// 404 if not found
```

### 5.2 `POST /v1/users:batch`

```jsonc
// req
{ "ids": [1, 2, 42, 99] }
// 200 — missing ids are simply absent
{ "data": { "1": {...}, "2": {...}, "42": {...} } }
```

### 5.3 `POST /v1/check`

```jsonc
// req
{
  "user_id": 42,
  "permission": "injection.create",
  "scope_type": "aegis.project",   // optional; omit for global perm
  "scope_id": "proj-7"             // string per UserScopedRole.ScopeID
}
// 200
{ "data": { "allowed": true, "reason": "role:project_admin" } }
```

Internally runs the equivalent of today's `middleware/deps.go` UNION query
against `user_roles ⋃ user_scoped_roles` joined through `role_permissions`.

### 5.4 `POST /v1/permissions:register`

Called by each consuming service at startup (idempotent upsert):

```jsonc
{
  "service": "aegis-backend",
  "permissions": [
    { "name": "injection.create",  "display_name": "Create injection", "scope_type": "aegis.project" },
    { "name": "system.admin.read", "display_name": "Read sys admin",   "scope_type": "" }
  ]
}
```

### 5.5 (Optional) `POST /v1/check:batch`

Same shape as `/v1/check`, takes array, returns array. Saves N HTTP calls
when middleware needs to check multiple permissions per request. Implement
if AegisLab perf needs it; skip otherwise.

## 6. `module/ssoclient` (AegisLab side)

```go
package ssoclient

type Client interface {
    // middleware.TokenVerifier
    VerifyToken(ctx context.Context, raw string) (*utils.Claims, error)
    VerifyServiceToken(ctx context.Context, raw string) (*utils.ServiceClaims, error)

    // user lookup
    GetUser(ctx context.Context, id int) (*UserInfo, error)
    GetUsers(ctx context.Context, ids []int) (map[int]*UserInfo, error)

    // permission check
    Check(ctx context.Context, p CheckParams) (bool, error)

    // bootstrap
    RegisterPermissions(ctx context.Context, perms []PermissionSpec) error
}

type CheckParams struct {
    UserID    int
    Permission string
    ScopeType string // "" for global
    ScopeID   string
}
```

Internals:
- JWKS fetched on `OnStart` lifecycle hook; refreshed every 10min (background goroutine).
- LRU cache for `Check`: `hashicorp/golang-lru/v2`, 10k entries, 30s TTL.
  Key = `userID|permission|scopeType|scopeID`.
- Service token: also fetched on OnStart from `/token` with
  `client_credentials`; refreshed at exp - 60s.

## 7. Module boundary in code

| Concern | Lives in (PR-1) |
|---|---|
| `model.User`, `model.APIKey`, `model.Role`, `model.Permission`, `model.RolePermission`, `model.UserRole`, `model.UserScopedRole`, `model.OIDCClient` | `module/user/model.go` (moved out of central `model/entity.go`) |
| `*User`, `*Role` GORM preloads in business tables | **deleted** |
| `module/user`, `module/auth`, `module/rbac` | only loaded by `cmd/aegis-sso`'s fx graph; removed from `app/http_modules_gen.go` |
| `middleware.TokenVerifier` + permissionChecker | implemented by `module/ssoclient` |
| `framework.PermissionRegistrar` fx group | still used inside `cmd/aegis-sso` for SSO-internal modules; for AegisLab consumers, `module/ssoclient` collects them at startup and calls `/v1/permissions:register` once. |

## 8. Process / deployment topology

```
docker-compose / helm:
  aegis-sso       :8083   (cmd/aegis-sso)
  aegis-backend   :8082   (cmd: rcabench both)
  mysql, redis    shared
```

Helm: new chart `helm/aegis-sso/`. Backend chart's values gain:

```yaml
ssoclient:
  baseURL: http://aegis-sso:8083
  clientID: aegis-backend
  clientSecret: # k8s Secret ref
  jwksURL: http://aegis-sso:8083/.well-known/jwks.json
```

## 9. Migration plan (destructive, no data preservation)

User confirmed no production data → destructive migration is acceptable.

```sql
-- run once when shipping PR-1
DROP TABLE IF EXISTS user_project_roles;
DROP TABLE IF EXISTS user_team_roles;
DROP TABLE IF EXISTS user_container_roles;
DROP TABLE IF EXISTS user_dataset_roles;
-- new table created by GORM AutoMigrate from UserScopedRole
ALTER TABLE permissions ADD COLUMN service VARCHAR(64) NOT NULL DEFAULT 'aegis';
ALTER TABLE permissions ADD COLUMN scope_type VARCHAR(64) NOT NULL DEFAULT '';
-- existing default-seeded users (admin/test) are recreated fresh by SSO init
TRUNCATE TABLE users;
TRUNCATE TABLE user_roles;
TRUNCATE TABLE api_keys;
```

Init seeds (`module/user.bootstrap.go`): `admin` / `admin@aegis.local` /
default password from config.

## 10. E2E smoke

Goes in `regression/sso_smoke.py`:

1. `docker compose up -d sso backend mysql redis`
2. `curl /token` with `grant_type=password` (resource-owner flow for test
   convenience) → access_token
3. `curl backend/api/v1/projects` with `Authorization: Bearer <token>` → 200
4. `curl backend/api/v1/admin/users` with non-admin token → 403
5. `curl sso/v1/check -d '{...}'` with service_token → `{allowed:true}`

## 11. PR sequence

| PR | Scope | Tasks |
|---|---|---|
| PR-1a | schema collapse + RS256 keys + scaffold cmd/aegis-sso (no behavior yet) | #3, #4, #2 |
| PR-1b | OIDC + /v1 endpoints on SSO server | #5, #6 |
| PR-1c | AegisLab consumer: module/ssoclient + middleware swap | #7, #8 |
| PR-1d | AegisLab cleanup: remove modules, refactor *User preloads | #9 |
| PR-1e | Deployment + frontend OIDC + e2e | #10, #11, #12 |

Each PR keeps AegisLab buildable + e2e green.

## 12. Delegated admin (service_admin) — Task #13

Beyond the global `system_admin` role and the pre-existing service token
(machine-to-machine), `aegis-sso` supports a third principal: a
**delegated service admin** scoped to one downstream service.

**Concept.** Granting role `service_admin` with `scope_type =
"aegis-sso.service"` and `scope_id = "<downstream service>"` makes the
target user admin of everything `aegis-sso` exposes for that one service.
Global admins always win (bypass every service filter); a service admin
sees and edits only their service's slice.

**Grant call.**

```jsonc
POST /v1/grants
{
  "user_id": 42,
  "role": "service_admin",
  "scope_type": "aegis-sso.service",
  "scope_id": "aegis-backend"
}
```

**Permissions packaged into the role** (seeded at startup;
`service = aegis-sso`, `scope_type = aegis-sso.service`):

| Name | Used by |
|---|---|
| `sso.users.read` / `sso.users.write` | `/v1/users/{id}`, `/v1/users:batch`, `/v1/users:list` |
| `sso.roles.read` | `/v1/roles` (when added) |
| `sso.grants.read` / `sso.grants.write` | `/v1/grants` POST/DELETE, `/v1/users/{id}/grants` |
| `sso.clients.read` / `sso.clients.write` | `/v1/clients` and `/v1/clients/{id}` |
| `sso.permissions.register` | `/v1/permissions:register` |

**Filtering semantics per handler:**

- `/v1/users:list`, `/v1/users:batch`, `/v1/users/{id}` — restricted to
  users who have **at least one user_scoped_roles grant** on a role whose
  permissions include the caller's admin service. A user that has no
  grant in any of the caller's services is invisible (404 on GET, absent
  from list/batch).

- `/v1/clients` (list / get / create / update / rotate / delete) —
  - `?service=…` must be a service the caller admins, else 403.
  - No filter → fan-out per admin service; clients owned by other
    services are filtered out.
  - GET/UPDATE/ROTATE/DELETE on a client owned by an out-of-scope
    service → 403.

- `/v1/grants` POST / DELETE — three rules, OR'd:
  1. Global admin → allowed.
  2. Request `scope_type = "aegis-sso.service"` → `scope_id` must be in
     the caller's admin set.
  3. Request `scope_type = aegis.project` / `aegis.team` / etc → the
     role being granted must only carry permissions whose `service` is
     in the caller's admin set. (Looked up via
     `ListRolePermissionServices`.)

- `/v1/permissions:register` — `req.service` must be in the caller's
  admin set (or caller is global admin).

**Permission resolution.** `rbac.Repository.CheckPermission` accepts a
service-admin grant as "allowed to attempt" when called with an empty
`scope_type` — i.e. a service-admin user passing the gate. The handler
remains responsible for filtering the response to the user's services.

**Recovery from over-grants.** Revoke with a matching DELETE on
`/v1/grants` (same body). A service admin cannot remove their own
service-admin grant — only a global admin can.

