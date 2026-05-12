# RFC: Configuration center (`module/configcenter` + `cmd/aegis-configcenter`)

- Status: **Draft**
- Owners: aegis-backend
- Stakeholders: every service (sso, notify, blob, monolith, gateway)
- Related: `infra/etcd/Gateway` (the KV primitive this builds on),
  `config/config.go` (viper TOML+env layer this extends),
  `module/notification` (the standalone-service pattern this mirrors)

---

## Summary

Promote etcd from "a KV some modules happen to use" into the
platform's dynamic configuration source, fronted by a first-class
service. Ship:

- `module/configcenter` — in-process implementation on top of
  `infra/etcd.Gateway`: layered merge (etcd > env > TOML), typed
  `Bind[T]`, hot reload via Watch, audit trail.
- `module/configcenterclient` — local + remote dual-mode SDK, matching
  the `notificationclient` pattern.
- `cmd/aegis-configcenter` — standalone microservice, default `:8087`,
  composed identically to `aegis-notify` / `sso`. **It does NOT
  replace etcd**; etcd remains the storage backend. The service owns
  the typed admin API, audit log, schema validation, watch fanout,
  and the only place that holds write credentials to etcd.

Consumers go through `configcenterclient`. They never touch etcd
directly for *configuration*. (Distributed-lock / lease use of etcd
in `module/injection/alloc.go` is unrelated and stays on
`infra/etcd.Gateway`.)

## Motivation

Today, configuration is split:

- `config/config.go` — viper, reads TOML, applies env overrides via
  `SetEnvKeyReplacer` (added when SSO landed).
- `infra/etcd/Gateway` — used by `module/system`, `chaossystem`,
  `injection/alloc` as a free-form KV store. Each consumer parses
  its own values, no schema, no typed access, no hot reload.

The gap:

| Need | Today | Gap |
| --- | --- | --- |
| Read static config (DB DSN, ports) | TOML + env via viper | ✅ |
| Read dynamic config (feature flags, ramp percentages) | nowhere | ❌ |
| Watch for changes (no restart) | etcd Watch exists but no typed API | ⚠ raw KV only |
| Audit who changed a value | none | ❌ |
| Namespace per service | informal key naming | ⚠ |
| Schema validation | none | ❌ — wrong type = runtime panic |
| Override TOML at runtime | only env, not etcd | ❌ |

Without a unified layer, every new dynamic-config consumer reinvents
the same parsing + watch + reload boilerplate.

## Non-goals

- Replace TOML files. TOML stays the canonical static-config source
  for "things that need to be set before the process starts" (DB
  DSN, JWT keys path, etcd endpoints themselves).
- Multi-cluster / multi-tenant config federation. Single etcd
  cluster per environment in v1.
- A general-purpose config-as-a-service GUI. We expose admin endpoints;
  the portal can build a UI later.
- Secrets manager. Secrets stay in env / sealed secrets / driver
  credentials; this is for plain config.

## Proposed design

### Layered merge order

```
etcd  (highest — runtime override, hot)
  ↓ env vars
  ↓ TOML file
defaults in code (lowest)
```

Rationale: TOML is checked-in baseline, env is per-deploy override
(matches the SSO env-replacer change), etcd is the runtime override
that can be flipped without redeploy. This matches the implicit
order developers already expect — etcd just slots above env.

### Typed API

```go
package configcenter

type Center interface {
    // Bind reads the value at namespace+key, decodes into out (a pointer to
    // a struct or scalar), applies the layered merge, and registers a
    // watcher so subsequent etcd changes update out atomically.
    Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error)

    // Get reads the current resolved value as JSON (for admin / debug).
    Get(ctx context.Context, namespace, key string) (raw []byte, layer string, err error)

    // Set writes a value to etcd. Always at the etcd layer (top).
    Set(ctx context.Context, namespace, key string, value any, opts ...SetOpt) error

    // Delete removes the etcd-layer entry; lower layers reappear.
    Delete(ctx context.Context, namespace, key string) error

    // List returns all keys under a namespace, with their current values
    // and which layer each resolves from.
    List(ctx context.Context, namespace string) ([]Entry, error)
}

type Handle interface {
    // Reload forces a re-decode (useful in tests).
    Reload(ctx context.Context) error
    // Subscribe registers a callback fired on every change.
    Subscribe(fn func()) (unsubscribe func())
    Close() error
}

type BindOpt func(*bindOptions)

func WithDefault(v any) BindOpt
func WithValidator(fn func(any) error) BindOpt
func WithSchemaVersion(v int) BindOpt
```

Concrete consumer pattern:

```go
type RateLimitConfig struct {
    DefaultRPS   int            `mapstructure:"default_rps"`
    PerRoute     map[string]int `mapstructure:"per_route"`
    Enabled      bool           `mapstructure:"enabled"`
}

var cfg RateLimitConfig
h, err := center.Bind(ctx, "ratelimiter", "default", &cfg,
    configcenter.WithDefault(RateLimitConfig{DefaultRPS: 100, Enabled: true}),
    configcenter.WithValidator(validateRateLimit),
)
defer h.Close()
h.Subscribe(func() { logrus.Info("rate limit config reloaded") })
```

All `cfg` reads from the same goroutine see a coherent snapshot
because `Bind` swaps the pointer target atomically via reflect — or,
for advanced cases, callers can use a sync.Value-style accessor.

### Key naming

```
/aegis/<env>/<namespace>/<key>
```

- `env` — `dev` | `staging` | `prod`. Comes from process env at startup.
- `namespace` — module-owned. e.g. `notification`, `blob`, `gateway`,
  `injection`, `system`.
- `key` — module-defined. Lowercase, dot-separated.

Examples:

```
/aegis/prod/notification/dedup_window_seconds
/aegis/prod/notification/channels.email.enabled
/aegis/prod/gateway/rate_limit.default_rps
/aegis/prod/blob/buckets.dataset-artifacts.retention_days
```

### Storage format

Values are JSON, regardless of original type. Decoder uses
`mapstructure` so struct tags from existing config types work
unchanged. This lets `Set` accept either typed Go values or raw
JSON (admin API).

### Watch + atomic swap

`Bind` opens a single etcd watch per Center instance (multiplexed
internally). On change:

1. Re-decode value.
2. Run validator.
3. If invalid: keep previous value, log + bump
   `configcenter_invalid_updates_total{namespace,key}`.
4. If valid: atomic pointer swap; fire subscribers.

The merge is recomputed on every change. If the etcd value is
deleted, the resolved value falls back to env → TOML → default.

### HTTP surface (owned by `aegis-configcenter`)

All routes JWT-required. Read endpoints accept any authenticated
service token or human user; write endpoints require role = `admin`
(human) or scope = `config:write` (service token).

| Method & path | Description |
| --- | --- |
| `GET /api/v2/config/:namespace` | List all keys + values + resolving layer. |
| `GET /api/v2/config/:namespace/:key` | Get one. |
| `PUT /api/v2/config/:namespace/:key` | Set etcd layer. Body: JSON value. Writes audit row. |
| `DELETE /api/v2/config/:namespace/:key` | Remove etcd layer. Writes audit row. |
| `GET /api/v2/config/:namespace/:key/history` | Read recent revisions (capped). |
| `GET /api/v2/config/:namespace/watch` | SSE stream of changes under a namespace. The `configcenterclient` remote impl subscribes here instead of opening its own etcd watch. |
| `GET /healthz` | Liveness. |
| `GET /readyz` | Etcd reachable + last successful watch event. |

`aegis-configcenter` is the **only** process that holds etcd write
credentials in v1. Etcd is locked down at the network layer to
accept connections only from this service (admin) and from a
read-only role used by services that fall back to direct watches
during a configcenter outage (see Failure modes).

### Audit

```sql
CREATE TABLE config_audit (
  id          BIGINT       PRIMARY KEY AUTO_INCREMENT,
  namespace   VARCHAR(64)  NOT NULL,
  key_path    VARCHAR(256) NOT NULL,
  action      VARCHAR(16)  NOT NULL,   -- set | delete
  old_value   JSON,
  new_value   JSON,
  actor_id    INT,                      -- users.id, null for service tokens
  actor_token VARCHAR(64),              -- token sub for service writes
  reason      VARCHAR(256),
  created_at  DATETIME(3) NOT NULL,
  INDEX idx_ns_key (namespace, key_path, created_at DESC)
);
```

Written synchronously on every Set/Delete by the admin handler. The
typed in-process `Set` is only callable from the admin handler — no
backdoor.

### Producer SDK (`module/configcenterclient`)

Mirrors `module/notificationclient` exactly: same `Client`
interface, two impls, mode selected by config.

```go
package configcenterclient

type Client interface {
    Bind(ctx context.Context, namespace, key string, out any, opts ...BindOpt) (Handle, error)
    Get(ctx context.Context, namespace, key string) (raw []byte, layer string, err error)
    Set(ctx context.Context, namespace, key string, value any, opts ...SetOpt) error
    Delete(ctx context.Context, namespace, key string) error
    List(ctx context.Context, namespace string) ([]Entry, error)
}
```

- `LocalClient` — wraps `module/configcenter` in-process. Used by
  `aegis-configcenter` itself and by any binary that wants to skip
  the hop (e.g. dev builds where everything runs in-process).
- `RemoteClient` — HTTP client to `aegis-configcenter`. Subscribes to
  the SSE watch stream for hot reload. Carries a service token from
  `ssoclient`. This is what every other service uses by default.

Wiring:

```toml
[configcenter.client]
mode = "remote"
endpoint = "http://aegis-configcenter:8087"
```

### Standalone binary

`cmd/aegis-configcenter` mirrors `cmd/aegis-notify`:

```
package main
// cobra entry, default port :8087, --conf <dir>
fx.New(configcenter.Options(conf, port)).Run()
```

`app/configcenter/options.go` composes:

- `app.BaseOptions(conf)` + `ObserveOptions` + `DataOptions`
- `infra/etcd.Module` (this binary is the only one with etcd write
  creds)
- `module/auth` (JWT verifier — same as aegis-notify)
- `module/user` (audit log actor lookup)
- `module/configcenter`
- `httpapi.Module` + `/healthz`/`/readyz` decorator

### What stays in TOML

Per-process bootstrap that the configcenter itself needs:

- `etcd.endpoints`, `etcd.username`, `etcd.password`
- DB DSN (you need a DB before you can read config from a DB-backed
  fallback)
- JWT keys path
- Listen port

Everything else is fair game for migration to configcenter.

### Migration of existing consumers

In-place, one module per PR:

1. `module/system` — currently `etcd.Gateway.Get(...)`. Becomes
   `center.Bind(ctx, "system", "...", &sysCfg)`. Keep the etcd
   gateway as the low-level fallback for non-config KV uses (locks,
   leases — not config).
2. `module/chaossystem` — same.
3. `module/injection/alloc.go` — same. (alloc.go uses etcd for
   distributed coordination, NOT config; **do not migrate**. The
   distinction matters.)
4. New consumers (notification, blob, gateway) start on configcenter
   from day one.

### Failure modes

- `aegis-configcenter` unreachable at startup → remote client falls
  back to env → TOML → default. Service still boots. Retry loop
  reconnects when the configcenter returns. Optional: the client may
  open a **read-only direct etcd watch** as a degraded path (using a
  read-only etcd credential), shedding only the audit guarantee.
- etcd unreachable from `aegis-configcenter` → its `/readyz` fails;
  the gateway can shed traffic. In-process `module/configcenter` (in
  the configcenter binary itself) keeps last-known-good values in
  memory; serves reads, fails writes.
- etcd value malformed JSON → keep previous resolved value, alert
  via `configcenter_invalid_updates_total`.
- Validator rejects new value → same as malformed.

## Migration plan

1. **Phase A (this PR)**: land `module/configcenter` +
   `module/configcenterclient` + `cmd/aegis-configcenter` +
   `app/configcenter` + audit table migration. LocalClient wired in
   the configcenter binary; everything else still on TOML+env. No
   prod traffic yet.
2. **Phase B**: deploy `aegis-configcenter` in Helm chart. Migrate
   one consumer (e.g. notification's `retention_days`) to prove the
   remote SDK + SSE watch end-to-end.
3. **Phase C**: migrate `module/system` + `chaossystem` (existing
   raw `infra/etcd.Gateway` config consumers) onto
   `configcenterclient`. `infra/etcd.Gateway` stays in the tree but
   is only used for non-config concerns (locks, leases).
4. **Phase D**: every new module wires through `configcenterclient`
   by default.

## Alternatives considered

- **Adopt Nacos / Apollo / Consul KV.** Adds an operational
  component the team doesn't run. We already run etcd. The typed
  layer is ~400 LOC; the wrapper cost is less than the operational
  cost of adopting an external dependency.
- **Just use viper everywhere with `viper.WatchConfig`.** That only
  watches files, doesn't centralize, and gives no audit trail. No
  cross-process consistency.
- **Push config via env, restart pods on change.** Restart latency
  defeats the point. Rate-limit ramps want sub-second propagation.

## Open questions

1. **Per-pod overrides.** A canary pod might want a different
   rate-limit than the rest. Proposal: support
   `/aegis/<env>/<namespace>/<key>@<pod_hostname>` as an optional
   override key. Off by default.
2. **Secrets bleed.** What stops someone setting a "secret" in
   configcenter? Proposal: lint at Set time — reject if the key
   path contains `password|secret|key|token`. Force secrets into
   sealed secrets / env.
3. **History retention.** etcd's native revision history is
   compaction-bounded. For audit we have the DB table; do we ever
   need etcd revisions specifically? Probably no. Keep DB audit
   authoritative.

## Acceptance criteria

- `center.Bind(ctx, "notification", "retention_days", &n,
  configcenter.WithDefault(90))` returns 90 with no etcd entry,
  reflects new value within 1 s after `PUT /api/v2/config/...`.
- Admin endpoints require `admin` role; mutation writes audit row.
- Restart of any service preserves the etcd-layer value.
- `pnpm check` (frontend) unaffected (no frontend changes in this
  RFC).
- etcd outage does not block service boot.
