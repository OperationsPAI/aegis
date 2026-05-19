# aegis-chaos: Pluggable Fault-Injection Service for Aegislab

Status: draft / discussion
Owner: TBD
Last updated: 2026-05-19

## 1. Background & Motivation

Today `chaos-experiment` ships as a Go SDK that aegislab imports in-process
(`replace github.com/OperationsPAI/chaos-experiment => ../chaos-experiment` in
`aegislab/src/go.mod`). Three structural problems have accumulated:

1. **Static per-system catalog.** The list of injectable positions for each
   benchmark (services, HTTP endpoints, DB operations, gRPC methods, JVM class
   methods, runtime mutators) lives as Go packages under
   `chaos-experiment/internal/<sys>/*`, registered via `init()` in
   `internal/adapter/adapter.go:36-50`. Onboarding a new benchmark requires
   editing Go source in two repos plus DB/etcd seed. Five recent benchmark
   integrations (sockshop, hotelreservation, mediamicroservices, socialnetwork,
   teastore) all paid this cost.

2. **Executor coupling.** `chaos-experiment/controllers/*` hardcodes Chaos-Mesh
   CRDs. Augmenting or replacing with another tool (ChaosBlade, Istio fault
   injection, custom probes) is structurally impossible without rewriting the
   construction layer.

3. **Microservice ↔ point binding is implicit.** Points currently reference
   services as strings inside system-wide arrays. There is no first-class
   notion of "this microservice (at this version) owns these injection points,
   and when the microservice changes, its points follow." Newly added services
   surface only after a Go source edit + redeploy of aegis-backend.

This document designs `aegis-chaos`: an HTTP-only service deployed inside
aegislab that owns the chaos catalog + injection lifecycle, abstracts the
execution backend, and binds points to microservice versions.

### Core responsibility, in one sentence

**aegis-chaos translates a per-service injection request into the concrete
surface of whichever execution backend will carry it out.** The translation
happens in two hops:

```
User request (point_id + params)
        ↓     aegis-chaos resolves Point → (capability, target, params)
Capability + target + params           ← backend-agnostic inter-lingua
        ↓     Executor.Apply: pure rendering, no business semantics
Backend surface (Chaos-Mesh CR / ChaosBlade exp / Istio fault patch / ...)
```

The first hop is aegis-chaos's job — it owns the catalog (which services,
which versions, which points exist) and turns "the point the user pointed
at" into a standardized Capability call. The second hop is each Executor's
job — it knows how to render a Capability into its own backend's surface
but knows nothing about benchmarks or services.

This split is what makes "adding a new backend" and "adding a new service"
two independently varying changes. New backend = implement ~18 Capability
renderers. New service = HTTP POST data, no executor code changes.

## 2. Goals & Non-goals

**Goals**
- Clear boundary: aegis-backend talks to aegis-chaos over HTTP only.
- Onboarding a new benchmark or new microservice is a data operation (HTTP +
  scripts), not a source edit.
- Multiple execution backends pluggable behind a single Capability vocabulary.
- Reproducibility: historical injections remain interpretable even after
  services, capabilities, or executors evolve.
- Backend loses Chaos-Mesh CRD RBAC entirely.

**Non-goals**
- RCA reasoning, datapack collection, trace pipeline — unchanged.
- Experiment campaign orchestration stays in aegis-backend; aegis-chaos is a
  primitive, not a campaign runner.
- Replacing Chaos-Mesh. It remains the first executor; the point is the
  design stops being *about* Chaos-Mesh.
- **No first-class Workflow API.** aegis-chaos applies one Capability
  instance per Injection. Sequencing, conditional steps, and gating live
  in the caller (aegis-backend campaign engine, aegisctl scripts). If a
  chaos-mesh injection happens to be implemented internally as a
  single-step Workflow CR, that is an Executor implementation detail, not
  API surface.
- **No central multi-cluster catalog.** Each cluster runs its own
  aegis-chaos instance with its own catalog. aegis-backend keeps a
  `chaos_endpoints` registry mapping cluster name to aegis-chaos URL.
  Cluster identity lives in the endpoint URL, not in any API path.
  Identical benchmarks in different clusters legitimately have different
  catalogs (different chaos-mesh versions, fork patches, available CNI
  features) and forcing them to share would hide real differences.
- **Pedestal restart is not in this design's scope.**
  `core/orchestrator/restart_pedestal.go` is a separate lifecycle (helm
  install/uninstall of benchmark workloads) that happens to share the
  k8s client wiring with chaos-experiment today. Backend keeps its
  Deployment / Service / Helm RBAC after migration. Only the
  `chaos-mesh.org` CRD RBAC is removed (at step 5c, see §11). PRs that
  tighten backend's k8s RBAC during the migration MUST explicitly
  carve out pedestal's surface.
- **aegis-chaos does not discover Points.** The catalog is fed by
  external Point Manifests (see §6 and ADR-0008). There is no
  in-process k8s scanner, OpenAPI parser, ClickHouse trace probe, or
  JVM agent inside aegis-chaos. The `:discover` endpoint that existed
  in earlier drafts of this design is removed.

## 3. Conceptual Model

```
System            a benchmark (ts, otel-demo, ...)
  └─ Service      a microservice within a system, versioned
       └─ Point   an injection target on that service version
            (uses)
              └─ Capability   declarative perturbation kind (http_latency,
                              pod_kill, ...) — backend-agnostic
            (executed by)
              └─ Executor     backend implementing the Capability
                              (chaos-mesh, chaos-blade, ...)

Orthogonal:
- Injection      a specific live application of (Point, params)
- GuidedSession  interactive resolver for "help me pick a Point and params"
```

### System
A logical benchmark. Fields: `name`, `ns_pattern`, `app_label_key`, `enabled`.
Operator-managed via `PUT /systems/{name}`. Contains many Services.
Cross-service Points (e.g. network partition between A and B) attach to the
System, not to a single Service.

### Service
A microservice within a System. **Identified by the 4-tuple
`(system, name, instance, chart_version)`** — ADR-0011. `instance`
is the helm release name (or a normalised derivative); two
concurrently deployed instances of the same chart (e.g. `hs0` and
`hs1` on the same cluster) are distinct Service rows. `instance`
defaults to `default` for single-instance charts. Fields: `system`,
`name`, `instance`, `chart_version`, `status` (active/retired),
`metadata` (image, replicas, ports, language), `discovered_at`,
`last_seen_at`. Populated from the chart's Point Manifest on
`helm install / helm upgrade` (§6).

### Point
An injection target. Belongs to either a Service (most common) or a System
(cross-service / topology-level). Fields:
- `id` — content-addressed (see §4)
- `service_id` or `system_id`
- `capability` (FK to Capability)
- `target` (JSON, validated by capability's `target_schema`)
- `param_overrides` (JSON, capability-allowed param narrowing — e.g. "this
  endpoint only supports 100–2000 ms latency")
- `source` (manual / k8s / otel-trace / openapi / jvm-introspect / import)
- `status` (active / superseded / deprecated)

### Capability
Backend-agnostic perturbation kind. Bootstrap data, ~15–20 in total. Adding a
Capability is a Go code change + DB seed; adding *Points* of an existing
Capability is data only. Fields:
- `name` (canonical: `http_latency`, `http_abort`, `network_delay`,
  `network_drop`, `network_partition`, `pod_kill`, `pod_failure`,
  `container_pause`, `dns_chaos`, `jvm_method_delay`, `jvm_method_exception`,
  `jvm_method_return`, `io_latency`, `io_fault`, `cpu_stress`, `memory_stress`,
  `time_chaos`, ...)
- `target_schema` (JSON Schema — what target type binds: endpoint? pod? db op?
  jvm method?)
- `param_schema` (JSON Schema — what params accepted)
- `observable_contract` (machine-checkable trace assertion — §9)
- `status` (stable / experimental / deprecated)

### Executor
A registered backend implementing some subset of Capabilities. Fields: `name`,
`version`, `endpoint`, `supported_capabilities` (with maturity flag per
capability), `health`, `last_heartbeat_at`. aegis-chaos is the router; the
Executor is the implementer.

### Injection
A live application. Fields: `id` (ULID), `point_id`, `params`,
`idempotency_key`, `executor`, `executor_handle` (opaque), `status`
(pending/running/succeeded/failed/cleaning/cleaned), `groundtruth`,
`diagnostics`, `started_at`, `finished_at`.

## 4. Microservice ↔ Point Binding & Lifecycle

This is the section most divergent from today's model. The headline:

**Points are bound to Service versions, not Service names.**

Three reasons:

1. **API evolution.** otel-demo v1.10 has `/api/checkout`; v1.12 adds
   `/api/checkout/v2`. Points must reflect what's actually injectable in the
   currently-running version.
2. **Reproducibility.** A reasoning eval on otel-demo v1.10 must be replayable
   in 6 months even after v1.20 drops some endpoints. Historical Point ids
   must still resolve.
3. **Discovery hygiene.** When a Service is redeployed, discovery re-runs. If
   we updated Points in place we couldn't distinguish "this endpoint
   disappeared" from "we forgot to rediscover this service."

### Point identity (content-addressed)

Two hash recipes, one per Point flavour:

```
service-bound Point (point.service_id IS NOT NULL):
  point_id = SHA256(
      system_name + "/" +
      service_name + "/" +
      instance + "/" +
      chart_version + "/" +
      capability + "/" +
      canonical_json(target)
  )[:16]

cross-service Point (point.service_id IS NULL):
  point_id = SHA256(
      system_name + "/" +
      capability + "/" +
      canonical_json(target)
  )[:16]
```

`param_overrides` is NOT in either hash recipe. The same Point
(same target shape) can have its `param_overrides` adjusted via a
later `:import` without changing its `point_id` — UPSERT semantics
make the update transparent. Only the `target` structure or
membership in the identity tuple regenerates the id.

The cross-service variant deliberately omits service_name/version
because its target already enumerates the participating services
(e.g. `{from: "frontend", to: "cart"}`) and its lifecycle is bound to
the System, not to any individual Service version. Including a
sentinel like `*` would defeat reproducibility for the network-* family.

Consequences:
- Re-discovering the *same* Point yields the *same* id → UPSERT no-op.
- Changing target structure yields a *different* id → old Point marked
  `superseded`, new one inserted. Historical Injections still resolve their
  old id via the immutable Points row.
- Cross-Service / cross-System reuse impossible (system/service name is in
  the hash), which is intentional — Points are not portable.

### Lifecycle rules

Service identity is the 4-tuple
`(system, name, instance, chart_version)` (ADR-0011). All rules
below use that tuple — `instance` and `chart_version` are first-class.

**On Point Manifest import (`:import replace_scope=service`):**
- `(system, name, instance, chart_version)` unknown → INSERT Service
  + INSERT manifest Points.
- `(system, name, instance, chart_version)` known → UPSERT Points
  by content hash. Points present in DB but missing from the
  payload transition to `superseded`.
- `(system, name, instance, chart_version=v1)` exists and a new
  manifest arrives with `chart_version=v2` → both rows live in
  parallel; v1's Points untouched, v2 gets its own Point set.
- Two manifests racing on the same `(system, name, instance)`
  (e.g. `helm upgrade` post-delete of v1 vs post-install of v2)
  serialise via `import_locks` (§7); they never interleave.

**On Service redeployment with no `chart_version` change:** same as
"known tuple" above — the import is idempotent at content hash.

**On Service removal:** Service marked `retired`. Its `active` Points
stay queryable for historical reference; new Injections on them are
rejected. Historical Injection rows keep `point_id` reference
indefinitely (`ON DELETE RESTRICT`). Two retire triggers:
(i) chart's helm `post-delete` hook posts an empty `:import
replace_scope=service`; (ii) the liveness reporter (§12.4) retires
rows whose tuple is missing from K consecutive cluster-side helm
listings.

**On Capability deprecation:** Capability marked `deprecated`. Existing Points
using it stay queryable but cannot be the basis of new Injections. After grace
period, Capability removed; its Points cascade to `deprecated`.

### Cross-service Points

Network partitions, topology faults, multi-service workflows belong to the
System, not a Service:
- `point.service_id = NULL`, `point.system_id = X`
- `target` references multiple services by name: `{from: "frontend", to: "cart"}`
- Lifecycle bound to System — survives single-service redeployment.

### Catalog feed

Catalog mutation happens via `:import` only (see §6 for the manifest
format and ADR-0008 for the rationale). There is no `:discover`
endpoint — aegis-chaos does not derive Points from cluster state.

Onboarding flow: deploy benchmark → `PUT /systems/X` → chart's helm
post-install Job calls `aegisctl manifest import` against the chart's
Point Manifest → operator reviews via `GET /systems/X/points` →
activate campaigns.

## 5. HTTP API

API versioning: `/v1beta` for the first ~2 months while shape is in motion;
freeze to `/v1` once the first real campaign uses it.

All requests carry `Authorization: Bearer <token>` (see §10.1). The webhook
direction (aegis-chaos → backend) uses a separately-provisioned token
configured per cluster. System resources expose a `max_concurrent_injections`
field (see §12.1).

```
# System
GET    /v1beta/systems
PUT    /v1beta/systems/{name}
DELETE /v1beta/systems/{name}

# Service
GET    /v1beta/systems/{sys}/services
GET    /v1beta/systems/{sys}/services/{svc}             current version
GET    /v1beta/systems/{sys}/services/{svc}/versions    all versions

# Point
GET    /v1beta/systems/{sys}/points                     flat across services
GET    /v1beta/systems/{sys}/services/{svc}/points
GET    /v1beta/points/{id}                              stable id lookup
POST   /v1beta/systems/{sys}/services/{svc}/points      manual add (single)
DELETE /v1beta/points/{id}                              marks deprecated
POST   /v1beta/systems/{sys}/points:import              Point Manifest import
       ?dry_run=true                                    validate without commit
       body: PointManifest YAML/JSON (see §6)

GET    /v1beta/manifest-schema.json                     authoritative JSON Schema

# Capability
GET    /v1beta/capabilities
GET    /v1beta/capabilities/{name}                      includes schemas + contract
GET    /v1beta/capabilities/{name}/matrix               executor support matrix

# Executor
GET    /v1beta/executors                                registered + health
POST   /v1beta/executors/{name}:probe                   force health re-check

# Injection (singleton)
POST   /v1beta/injections                               {point_id, params,
                                                         idempotency_key,
                                                         caller_metadata,
                                                         [executor_pin]}
GET    /v1beta/injections/{id}
DELETE /v1beta/injections/{id}                          early cleanup → cancelled
POST   /v1beta/injections:preview                       dry-run; returns
                                                         rendered exec plan +
                                                         groundtruth

# Injection batch (ADR-0001)
POST   /v1beta/injection-batches                        {idempotency_key,
                                                         children: [{point_id,
                                                                     params,
                                                                     caller_metadata}],
                                                         batch_caller_metadata}
GET    /v1beta/injection-batches/{id}
DELETE /v1beta/injection-batches/{id}                   cancel all non-terminal
                                                         children

# Guided session (replaces pkg/guidedcli)
POST   /v1beta/guided-sessions                          {system}
POST   /v1beta/guided-sessions/{tok}/step               {field, value}
POST   /v1beta/guided-sessions/{tok}:commit             → injection_id or batch_id

# Webhooks (aegis-chaos → backend); see §10.2 for full payload
backend:  POST /hooks/chaos                             singleton-injection terminal
backend:  POST /hooks/chaos-batch                       batch terminal
```

### Endpoint × scope matrix

| Endpoint                                              | Required scope            |
|-------------------------------------------------------|---------------------------|
| `GET /systems` / `/services` / `/points` / `/points/{id}` / `/capabilities*` / `/manifest-schema.json` | `read:catalog` |
| `PUT/DELETE /systems/{name}`                          | `admin`                   |
| `POST /points` (single add) / `:import` (commit)      | `write:catalog`           |
| `:import?dry_run=true`                                | `read:catalog`            |
| `DELETE /points/{id}`                                 | `write:catalog`           |
| `GET /executors`                                      | `read:executor-health`    |
| `POST /executors/{name}:probe`                        | `read:executor-health`    |
| Executor registration / deregistration                | `admin`                   |
| `POST /injections` / `POST /injection-batches`        | `inject`                  |
| `GET /injections/{id}` / `GET /injection-batches/{id}` | `read:catalog`           |
| `DELETE /injections/{id}` / `DELETE /injection-batches/{id}` | `inject` (cancel is part of injection lifecycle) |
| `POST /injections:preview`                            | `preview:injection`       |
| `POST /guided-sessions` / `/step` / `:commit`         | `manage:guided-session` (commit ALSO requires `inject`) |
| Backend `/hooks/chaos` and `/hooks/chaos-batch` receivers | `webhook:chaos-receiver` (validated on backend side) |

## 6. Point Manifests and External Delivery

aegis-chaos does not discover Points. Its catalog is fed exclusively by
external **Point Manifests**, each shipped with the helm chart of the
microservice it describes and applied at install time. The chart
version is the binding unit: deploying `frontend@v3.2.0` ships exactly
the Points listed in that version's manifest. ADR-0008 captures why.

### Manifest format

```yaml
apiVersion: aegis-chaos/v1beta
kind: PointManifest
metadata:
  system: ts
  service: frontend
  instance: default              # helm release name; "default" if single
  chart_version: v3.2.0
spec:
  replace_scope: service         # service | system | none
  points:
    - capability: http_latency
      target: { endpoint: /api/login, method: POST }
      param_overrides:
        delay_ms: { min: 100, max: 2000 }
        duration_s: { max: 60 }
    - capability: pod_kill
      target: {}
      param_overrides: {}
```

- `metadata.{system, service, instance, chart_version}` MUST match
  the chart's identity (ADR-0011). `service` is omitted only when
  the manifest contains cross-service Points exclusively.
- `spec.replace_scope` governs atomicity:
  - `service` — manifest is the complete catalog for
    `(system, service, instance, chart_version)`. Points present in
    DB but absent from the payload transition to `superseded`. This
    is the value Helm hooks use. `:import` serialises per
    `(system, service, instance)` via an `import_locks` row so
    concurrent helm hooks (e.g. post-delete of v3.2.0 against
    post-install of v3.3.0) cannot interleave.
  - `system` — same but scoped to the whole System. Reserved for
    rare bulk re-seeding.
  - `none` — append-only; never marks anything `superseded`. Useful
    for ad-hoc operator additions and for editing
    `param_overrides` on an existing Point (same `target` → same
    `point_id` → UPSERT updates overrides in place).

### Delivery: Helm post-install / post-delete Job (ADR-0009)

Each chart includes a hook Job that runs:

```bash
aegisctl manifest validate /manifest.yaml --system "$SYSTEM"
aegisctl manifest import   /manifest.yaml --system "$SYSTEM" --dry-run
aegisctl manifest import   /manifest.yaml --system "$SYSTEM" \
    --replace-scope=service
```

Validate failure → Job failure → chart install/upgrade failure.
`post-delete` runs with an empty `spec.points` payload to retire all
Points for that Service version atomically.

For benchmark charts we don't own (upstream social-network, hotel-
reservation), the manifest + hook is added in the LGU fork. This is
consistent with current practice of forking these charts for OTel
wiring.

### Validation (ADR-0010)

Two layers, single JSON Schema, no duplication of validation logic:

- **Offline** — `aegisctl manifest validate <file> [--system X]`.
  Structural + intra-manifest + capability-schema conformance, using
  a JSON Schema bundled in aegisctl. Editors can autoload via
  `$schema`. Runs without any cluster involvement; chart authors can
  `helm template | aegisctl manifest validate -`.
- **Online** — `POST /v1beta/systems/{sys}/points:import?dry_run=true`.
  Adds cluster-state checks: System registered + enabled, Capability
  supported by at least one healthy Executor in this cluster, plus a
  supersede-impact preview ("if you commit, N Points transition to
  `superseded`"). Transaction always rolls back.
- **Schema discovery** — `GET /v1beta/manifest-schema.json` returns the
  authoritative live schema. aegisctl uses its bundled copy by default;
  `--fetch-schema` pulls from the server for environments where the
  CLI version lags Capability additions.

### What lives outside aegis-chaos

Anything that *produces* a manifest is somebody else's tool:

- k8s scanner script (enumerate Pods/Services → pod-level Points)
- OpenAPI spec parser (spec → http_* Points)
- ClickHouse trace miner (observed endpoints/operations → Points)
- JVM introspection helper (port-forward → class methods → jvm_* Points)
- benchmark onboarding pipeline that merges multiple sources

These may live as aegisctl helper subcommands, standalone scripts in
chart repos, or pieces of the `register-aegis-system` skill workflow.
aegis-chaos sees only the resulting manifest.

## 7. Data Model (SQL sketch)

```sql
CREATE TABLE systems (
  name VARCHAR(64) PRIMARY KEY,
  ns_pattern VARCHAR(128) NOT NULL,
  app_label_key VARCHAR(64) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  max_concurrent_injections INT NOT NULL DEFAULT 5,    -- §12.1
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE services (                              -- ADR-0011
  id BIGSERIAL PRIMARY KEY,
  system_name VARCHAR(64) NOT NULL REFERENCES systems(name),
  name VARCHAR(128) NOT NULL,
  instance VARCHAR(128) NOT NULL DEFAULT 'default',  -- helm release name
  chart_version VARCHAR(128) NOT NULL,
  status ENUM('active','retired') NOT NULL DEFAULT 'active',
  metadata JSON,
  discovered_at TIMESTAMP NOT NULL,
  last_seen_at TIMESTAMP NOT NULL,
  UNIQUE(system_name, name, instance, chart_version),
  INDEX(system_name, status)
);

CREATE TABLE import_locks (                          -- ADR-0011: serialise
  system_name VARCHAR(64) NOT NULL,                  -- :import per
  service_name VARCHAR(128) NOT NULL,                -- (system,service,instance)
  instance VARCHAR(128) NOT NULL,                    -- across concurrent
  locked_by VARCHAR(64),                             -- helm hooks
  locked_at TIMESTAMP,
  PRIMARY KEY (system_name, service_name, instance)
);

CREATE TABLE capabilities (
  name VARCHAR(64) PRIMARY KEY,
  target_schema JSON NOT NULL,
  param_schema JSON NOT NULL,
  observable_contract JSON NOT NULL,
  status ENUM('stable','experimental','deprecated') NOT NULL,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE points (
  id CHAR(16) PRIMARY KEY,                           -- truncated SHA256
  system_name VARCHAR(64) NOT NULL REFERENCES systems(name),
  service_id BIGINT NULL REFERENCES services(id),   -- NULL = cross-service
  capability_name VARCHAR(64) NOT NULL REFERENCES capabilities(name),
  target JSON NOT NULL,
  param_overrides JSON,
  source VARCHAR(32) NOT NULL,
  status ENUM('active','superseded','deprecated') NOT NULL DEFAULT 'active',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  INDEX(system_name, status),
  INDEX(service_id, status)
);

CREATE TABLE executors (
  name VARCHAR(64) PRIMARY KEY,
  version VARCHAR(32),
  endpoint VARCHAR(256) NOT NULL,
  supported_caps JSON NOT NULL,                      -- [{capability, maturity}]
  health ENUM('healthy','degraded','down'),
  last_heartbeat_at TIMESTAMP
);

CREATE TABLE injection_batches (
  id CHAR(26) PRIMARY KEY,                           -- ULID; ADR-0001
  idempotency_key VARCHAR(64) NOT NULL UNIQUE,
  aggregated_status ENUM('pending','running','succeeded','partial',
                         'failed','cancelled') NOT NULL DEFAULT 'pending',
  batch_caller_metadata JSON,
  ts TIMESTAMP NOT NULL,
  started_at TIMESTAMP,
  finished_at TIMESTAMP,
  INDEX(aggregated_status, ts)
);

CREATE TABLE injections (
  id CHAR(26) PRIMARY KEY,                           -- ULID
  batch_id CHAR(26) NULL REFERENCES injection_batches(id)
                       ON DELETE RESTRICT,           -- NULL = singleton
  point_id CHAR(16) NOT NULL REFERENCES points(id) ON DELETE RESTRICT,
  params JSON NOT NULL,
  idempotency_key VARCHAR(64) NOT NULL UNIQUE,       -- ADR-0004
  executor_name VARCHAR(64) NOT NULL REFERENCES executors(name),
  executor_handle TEXT NOT NULL,                     -- ADR-0004: persisted
                                                     -- BEFORE Apply, derived
                                                     -- from idempotency_key
  status ENUM('pending','running','succeeded','failed','cancelled')
        NOT NULL,                                    -- ADR-0003
  groundtruth JSON,
  diagnostics JSON,
  caller_metadata JSON,                              -- ADR-0005: opaque
  destroyed_at TIMESTAMP NULL,                       -- ADR-0003: Destroy ok
  destroy_error TEXT NULL,                           -- ADR-0003: last failure
  ts TIMESTAMP NOT NULL,
  started_at TIMESTAMP,
  finished_at TIMESTAMP,
  INDEX(status, ts),
  INDEX(point_id),
  INDEX(batch_id, status)
);
```

## 8. Executor Abstraction

Internal Go interface — not exposed externally until a second executor lands
and forces the shape to settle.

```go
type Executor interface {
    Name() string
    SupportedCapabilities() []CapabilitySupport

    // Apply is idempotent w.r.t. the (capability, target, params, key) tuple
    // when the executor supports keys; aegis-chaos enforces idempotency at
    // its own layer regardless.
    Apply(ctx context.Context,
          capability string,
          target, params map[string]any) (handle string, err error)

    Status(ctx context.Context,
           handle string) (state ExecState,
                           diagnostics map[string]any,
                           err error)

    Destroy(ctx context.Context, handle string) error
}

type CapabilitySupport struct {
    Capability string
    Maturity   string // "stable" | "experimental"
}

type ExecState int
const (
    StatePending ExecState = iota
    StateRunning
    StateSucceeded
    StateFailed
)
```

Critical: `handle` is **opaque**. aegis-chaos persists it and round-trips it,
but does not parse it. Each Executor encodes whatever it needs (Chaos-Mesh:
`kind/namespace/name`; ChaosBlade: experiment uid; Istio: VirtualService UID +
patch generation).

### Routing

Config (cluster-scoped):
```yaml
capability_routes:
  http_latency:    [chaos-mesh, istio]      # preferred order
  http_abort:      [chaos-mesh, istio]
  pod_kill:        [chaos-mesh, chaos-blade]
  jvm_method_*:    [chaos-blade]            # mesh's JVMChaos is finicky
  network_delay:   [chaos-mesh]
  default:         [chaos-mesh]
```

On Apply: pick first preferred executor that is healthy and supports the
Capability at `stable` maturity. Fall back through the list. Fail with
`ExecutorUnavailable` if none match. Caller may override with
`executor_pin` for debugging / research-comparison runs.

### Diagnostics normalization

Executors return raw `diagnostics` (backend-specific words) plus a normalized
error class: `TargetNotFound` | `BackendUnavailable` | `Timeout` |
`Unsupported` | `Internal`. aegis-chaos uses the class for retry/route
decisions; the raw payload is for humans.

## 9. Capability Conformance

Each Capability defines an **observable contract**: a machine-checkable
assertion about how the perturbation must manifest in observable telemetry.

Example for `http_latency`:

```json
{
  "name": "http_latency",
  "contract": {
    "trace_assertions": [
      {"assertion": "span.duration_on_target_endpoint >= injected_latency_ms * 0.9"},
      {"assertion": "span.status_code unchanged from baseline distribution"}
    ],
    "baseline_window_s": 60,
    "injection_window_s": 60,
    "tolerance": {"false_positive_rate": 0.05}
  }
}
```

### Conformance harness

1. Deploy minimal target workload (echo HTTP server + OTel SDK) in test ns.
2. Collect baseline traces.
3. For each (Executor, Capability) pair the Executor claims to support:
   - Apply via that Executor.
   - Collect injection-window traces.
   - Evaluate contract assertions.
4. Produce report: which pairs are conformant, which are not, diffs from
   contract.

Triggers:
- Each Executor version bump.
- Each Capability schema change.
- Nightly regression.

Non-conformant pairs are marked `experimental` and excluded from production
campaigns by default. **This conformance suite is the differentiating asset**:
without it, swapping executors silently changes what "the same fault" means,
which destroys cross-experiment comparability.

## 10. Integration with aegis-backend

Backend talks to aegis-chaos via HTTP only.

### 10.1 Authentication

All requests carry `Authorization: Bearer <token>`. Two token classes:

- **Service tokens** — issued out-of-band, stored as k8s Secrets, used by
  aegis-backend → aegis-chaos. Rotation = update Secret + rolling restart.
- **User tokens** — issued by aegis-backend's existing user/auth system,
  used by aegisctl-from-laptop and ad-hoc scripts. Tied to a user identity
  for audit.

Both token classes carry a `scope` claim from the set:

```
read:catalog            read any non-sensitive resource (Systems, Services,
                        Points, Capabilities, manifest-schema, injection
                        status, batch status, dry-run import)
write:catalog           mutate Points (single add, :import commit,
                        DELETE /points/{id})
inject                  submit Injections / Batches; cancel them
preview:injection       :preview dry-run for injection planning
manage:guided-session   open/step/commit guided-sessions
                        (commit additionally requires `inject`)
read:executor-health    GET /executors, force health probe
admin                   System lifecycle (PUT/DELETE), executor
                        registration, capability deprecation
webhook:chaos-receiver  backend-side scope on /hooks/chaos and
                        /hooks/chaos-batch handlers
```

Read paths accept any valid token with `read:catalog` (implied by any
higher non-admin scope). The full endpoint × scope mapping lives in
the matrix at the end of §5.

mTLS is deliberately not used in v1beta — cert rotation across the
parallel kind / VKE / nuke-and-redeploy cadence is operationally
expensive without commensurate benefit. Revisit if a real cross-tenant
deployment appears.

Scopes are **catalog-wide** in v1beta: a token with `write:catalog`
can mutate any System's catalog, not just one. Per-System ACLs are
explicitly deferred until tenant separation appears. Single-tenant
research workload makes this acceptable today; revisit before any
multi-team / multi-tenant deployment.

The webhook direction (aegis-chaos → backend) uses a separately
provisioned service token; backend's `/hooks/chaos` handler validates
it and is idempotent on `(injection_id, new_status)`. Webhook delivery
is **cluster-internal only** in v1beta — backend's hook endpoint MUST
NOT be exposed beyond the cluster (no ingress, no public LB). HMAC
signing of the webhook body is deferred; the single Bearer token plus
network isolation is the v1beta threat-model.

### 10.2 Typical injection flow

For a single Injection (most experiments) or a Batch (hybrid /
multi-fault rounds), the shape is the same; the Batch path is the
common case so it's described here.

1. Backend picks N (Point id, params) tuples from campaign / Guided
   session / policy. Builds `caller_metadata` per child (task_id,
   task_type, group_id, project_id, user_id, parent_task_id — same
   fields today's CRD labels carry).
2. `POST /v1beta/injection-batches`
   ```jsonc
   {
     "idempotency_key": "<ulid>",
     "children": [
       {"point_id": "…", "params": {…}, "caller_metadata": {…}},
       …
     ],
     "batch_caller_metadata": {…}
   }
   ```
   Headers: `Authorization: Bearer <inject-scope-token>`,
   `traceparent: 00-<trace-id>-<span-id>-01` (standard W3C OTel
   propagation, ADR-0005). Response: `202` with `batch_id`.
3. Backend stores `batch_id` linked to its campaign/task row.
4. aegis-chaos applies each child via the routed Executor (Chaos-Mesh
   for now), tracks N-of-N completion under a row lock on the batch
   (ADR-0006), then fires one webhook:
   ```jsonc
   // POST /hooks/chaos-batch
   // Headers: Authorization: Bearer <webhook:chaos-receiver token>
   //          traceparent: 00-<trace-id>-<span-id>-01
   {
     "batch_id": "…",
     "prev_status": "running",
     "new_status": "terminal",
     "aggregated_status": "succeeded",  // succeeded | partial | failed | cancelled
     "started_at": "…",
     "finished_at": "…",
     "child_results": [
       {
         "injection_id": "…",
         "point_id": "…",
         "status": "succeeded",         // succeeded | failed | cancelled
         "started_at": "…",
         "finished_at": "…",
         "groundtruth": {…},
         "diagnostics_brief": "…",
         "caller_metadata": {…}         // round-tripped verbatim
       },
       …
     ],
     "batch_caller_metadata": {…}
   }
   ```
5. Backend's handler validates webhook token, continues the W3C trace
   from `traceparent`, recovers task context from each child's
   `caller_metadata`, and submits BuildDatapack with the correct
   ParentTaskID per child. Handler is idempotent on
   `(batch_id, new_status)` and uses a uniqueness gate on
   `(injection_id, downstream_task_type)` when submitting downstream
   work, so duplicate firings (whether via webhook retry or the
   shadowed CRD watcher during migration step 5b) become no-ops.
6. Backend periodically polls `GET /v1beta/injection-batches/{id}` for
   any in-flight batch whose webhook hasn't arrived; same handler is
   idempotent so duplicates are free.

For the singleton path (`POST /v1beta/injections`), replace `batch_id`
with `injection_id`, drop the `children` envelope, and the webhook
becomes `POST /hooks/chaos` carrying the single child's body
inline.

After cutover, backend no longer needs:
- `chaos-experiment` import.
- `client.InitWithConfig` k8s setup for chaos-mesh.
- Chaos-Mesh / ChaosBlade CRD RBAC in the chaos ns.
- `pkg/guidedcli` (replaced by thin HTTP client).

What stays in backend: campaign scheduling, datapack collection, RCA
evaluation, the `injection` table row that links a campaign step to an
aegis-chaos `injection_id`.

### 10.3 Multi-cluster routing

aegis-chaos is **per-cluster**. Backend keeps a `chaos_endpoints` registry:

```
chaos_endpoints (
  cluster_name VARCHAR(64) PRIMARY KEY,
  http_endpoint VARCHAR(256) NOT NULL,
  service_token_secret_ref VARCHAR(128) NOT NULL,
  health ENUM('healthy','degraded','down')
)
```

Backend picks the cluster (from campaign / task config) and routes the HTTP
call to the right aegis-chaos endpoint. Cluster name does not appear in
aegis-chaos's own URL paths — each instance is unaware of any cluster but
its own. Catalogs may legitimately diverge across clusters (different
chaos-mesh fork, different CNI capabilities); cross-cluster catalog sync
is explicitly out of scope.

## 11. Migration Path

Each step is independently mergeable. Reversibility holds through 5b;
5c is the single irreversible step and is deferred to the end.

1. **Skeleton + first Capability.** Stand up `aegis-chaos` deployment + DB
   schema (per §7). Implement Chaos-Mesh executor for `pod_kill`
   end-to-end. Land conformance harness shell with one Capability.
2. **Port Capability set.** Implement the remaining Capabilities — list
   regenerated programmatically from
   `chaos-experiment/handler/handler.go:90-134` `ChaosTypeMap`
   (see §14). Each Capability ships with `target_schema`,
   `param_schema`, `observable_contract`, and a Chaos-Mesh executor
   renderer; marked `stable` only after passing conformance.
3. **Port catalog data.** Dump `chaos-experiment/internal/<sys>/*` +
   aegislab `system_metadata` rows into Point Manifests and import
   via `:import replace_scope=service`. Seed manifests use
   `instance=seed` and `chart_version=seed-<timestamp>` sentinels.
   These sentinel rows are *not* superseded by future real chart
   hooks (different `(instance, chart_version)` key); they coexist
   harmlessly until the real chart's first install posts the
   authoritative manifest, after which campaigns naturally select
   the live Service row over the seed. The seed can be retired by
   a one-shot `aegisctl service retire --instance=seed` once all
   benchmarks have at least one real chart-hook run.
4. **Cut over Guided + add webhook receivers.** Two parallel things:
   (i) aegisctl `inject_guided*.go` becomes a thin HTTP client for
   `/guided-sessions`; (ii) backend adds `/hooks/chaos` +
   `/hooks/chaos-batch` receivers replicating today's
   `HandleCRDSucceeded` post-processing (ParentTaskID linkage,
   BuildDatapack submission, W3C OTel context continuation).
   Receivers are dead code at this point; lands them before backend
   takes any runtime dependency on aegis-chaos. CI unit tests cover
   the receiver against canned webhook payloads. **Prerequisite for
   this step**: backend's downstream submission path (BuildDatapack
   and friends) MUST be idempotent on `(injection_id, task_type)`.
4.5. **Catalog read cutover.** Backend's `BatchCreate` path reads the
   catalog from aegis-chaos over HTTP but still creates CRDs locally
   via chaos-experiment's controllers. By the time backend takes
   this dependency, the webhook receiver of step 4 already exists
   as a safety net for step 5 — the dependency order is the easier
   one. Catalog read failure short-circuits to today's in-process
   read for a brief grace period (gated by an etcd flag) so a
   transient aegis-chaos outage doesn't halt all injections.
5. **Cut over execution.** Split into two sub-steps (the
   receiver-add part of original 5a now lives in step 4). ADR-0007.
   - **5b.** Add per-system etcd flag
     `injection.system.<sys>.executor_authoritative =
     chaos-mesh-direct | aegis-chaos` (default `chaos-mesh-direct`).
     Flipping a system to `aegis-chaos` routes `BatchCreate` through
     `POST /injection-batches`; aegis-chaos creates the CR, webhooks
     back, and the receiver from step 4 triggers BuildDatapack. The
     CRD watcher remains live; its `HandleCRDSucceeded` call hits
     the `(injection_id, task_type)` uniqueness gate and no-ops.
     Soak each system (ts → otel-demo → … → teastore) before
     advancing.
   - **5c.** Once every system has soaked, delete chaos GVRs from
     `platform/k8s/controller.go:109` and the chaos branches of
     `HandleCRDSucceeded`. This is the only irreversible step in the
     migration.
6. **Delete chaos-experiment.** The repo / `replace` directive
   disappears from `aegislab/src/go.mod`. `SystemType` Go enum becomes
   `type SystemType string` (existing constants kept as aliases for
   compatibility). **Carve-out:** `core/orchestrator/restart_pedestal.go`
   is NOT in chaos-experiment; its helm install/uninstall paths and
   k8s client wiring stay in backend.
7. **Second executor (ChaosBlade).** This step *defines* the
   externally documented `Executor` interface — having a real second
   implementation is what surfaces the abstraction's right shape.
   Conformance harness runs ChaosBlade against the same Capability
   set; per-Capability maturity flags reflect actual pass rate.

Reversibility: any system in 5b can be rolled back by flipping its
etcd flag to `chaos-mesh-direct` — no redeploy required. After 5c, the
only rollback is restoring the watcher code from git.

## 12. Invariants, Limits, Failure Modes

### 12.1 Limits & safety valves

Single safety valve, deliberately minimal:

- **Per-system `max_concurrent_injections`** (default 5). Each System row
  carries this integer. `POST /injections` against a System already at its
  cap returns `429 Too Many Requests` with `Retry-After`. Operator raises
  the cap per-system as needed.

No per-request rate limit, no per-tenant quota, no global concurrency cap.
The deployment is single-tenant research workload; the realistic foot-gun
is "loop forgot to wait for cleanup" and max-concurrent stops that. Add
more knobs only when a concrete abuse pattern appears.

### 12.2 Param override UX

Point `param_overrides` are not advisory — they form the **effective
schema** for that Point:

```
effective_schema(point) = capability.param_schema ⋂ point.param_overrides
```

Guided sessions present the narrowed schema directly (no separate
"advanced" mode). `POST /injections` with out-of-range params returns
`400 Bad Request` carrying the violated constraint. **Never auto-clamp** —
silently rewriting a research operator's intent is a worse failure than
rejecting it.

### 12.3 Invariants

#### Invariants
- **I1.** Every `injection.point_id` resolves to a Point that existed when
  the Injection was created, even if that Point is now `deprecated`.
  (`ON DELETE RESTRICT`.)
- **I2.** A POST to `/injections` with a duplicate `idempotency_key` returns
  the existing `injection_id`; the executor's `Apply` is called at most once
  per key.
- **I3.** An injection in `running` state has a non-empty `executor_handle`
  valid on its `executor`.
- **I4.** `systems.enabled = false` rejects new Injections but does not
  affect in-flight ones.
- **I5.** `capabilities.status = deprecated` rejects new Injections; allows
  Status/Destroy on existing ones.
- **I6.** A `point_id` is content-addressed; the
  `(system, service, instance, chart_version, capability, target)`
  tuple uniquely determines it for service-bound Points, and
  `(system, capability, target)` for cross-service Points (§4).
- **I7. Idempotency key is burned after terminal.** Once an
  Injection (or Batch) reaches a terminal `aggregated_status`,
  future POSTs to `/injections` or `/injection-batches` with the
  same `idempotency_key` return the historical record and **never**
  re-invoke Executor.Apply. Callers that want a fresh run MUST mint
  a fresh key.
- **I8. Terminal `aggregated_status` is sticky** (ADR-0006). Once
  written to a terminal value, child status transitions do not
  recompute it or fire a second batch webhook. Per-child outcomes
  remain accurate via the children's own status columns.

### 12.4 Failure modes

aegis-chaos side:
- **Executor crash mid-Apply.** Caller key is the executor's primary
  key (ADR-0004), so the persisted handle is valid even if Apply was
  interrupted before returning. Reconciler calls `Status(handle)`;
  if the resource exists, transition the row; if it does not, retry
  Apply (idempotent on key).
- **Webhook delivery loss.** Backend pulls `GET /injection-batches/{id}`
  with bounded backoff for any batch it cares about. Both sides
  idempotent on `(batch_id, new_status)`; webhook is optimisation,
  polling is correctness.
- **Manifest UPSERT collisions.** Content-addressed point_id → re-import
  of the same Point is a UPSERT no-op. A Point whose target shape
  changes produces a different point_id, so the old row is marked
  `superseded` by the next `:import replace_scope=service` payload;
  historical injections are unaffected.
- **Cluster CRD upgrade changes Chaos-Mesh schema.** Caught by nightly
  conformance run; non-conformant Capabilities marked `experimental`;
  campaigns referencing them get warned at submit, blocked at run.
- **Executor reports succeeded but trace shows no effect.** Conformance
  contract violation; surfaces in nightly report; (Executor, Capability)
  pair gets demoted from `stable`. Injection itself stays `succeeded`
  since the executor's contract was met; the per-run anomaly is
  captured in Injection's `diagnostics`.
- **Executor.Destroy failure.** Recorded in `injection.diagnostics`
  but does not block the terminal status transition (ADR-0003).
  Periodic cleanup job sweeps `destroyed_at IS NULL AND status IN
  terminal` rows and retries Destroy.
- **`helm uninstall --no-hooks` leaks Service rows.** The post-delete
  hook (ADR-0009) is the only signal aegis-chaos has for retiring
  a Service. Operators using `--no-hooks` (common in
  `seed-update-cycle` nuke flows) bypass that signal entirely. A
  per-cluster reconciler — `aegisctl orchestrator-side reporter` —
  periodically lists live helm releases via the cluster's helm CLI
  and POSTs the live `(instance, chart_version)` set to aegis-chaos
  (`POST /v1beta/systems/{sys}/services:liveness`). Service rows
  whose tuple is absent from this set for K consecutive reports
  transition to `retired`. K and the reporter cadence settle in
  step 3 of migration.

Backend side:
- **Backend crash with in-flight injections.** Today the chaos CRD
  list-watch re-fires `HandleCRDSucceeded` for any CRD it sees on
  re-list. After 5c that fallback is gone. Replacement: backend
  startup runs a reconciler that scans `FaultInjection` rows in
  non-terminal states and calls
  `GET /v1beta/injection-batches/{id}` (or `/injections/{id}`) per
  row. Idempotent webhook receiver handles whatever state changes
  happened while backend was down.
- **Webhook receiver double-fire from migration step 5b.** During 5b
  the CRD watcher and the webhook receiver both reach
  `HandleCRDSucceeded`. The prerequisite uniqueness gate on
  `(injection_id, downstream_task_type)` makes the second submission a
  no-op (ADR-0007).

## 13. Resolved Decisions Log

| Topic                              | Decision                                                                                                             | ADR | Lives in       |
|------------------------------------|----------------------------------------------------------------------------------------------------------------------|-----|----------------|
| Batch primitive ownership          | aegis-chaos owns Batches (durable N-of-N tracking, single webhook per batch)                                          | 0001 | §3, §5, §10.2  |
| Batch webhook aggregation          | Three-state `aggregated_status` + full `child_results` per terminal webhook                                           | 0002 | §10.2          |
| Terminal-state cleanup             | Terminal transition calls `Executor.Destroy`; cluster artifact removed; new `cancelled` status alongside succeeded/failed | 0003 | §12.4          |
| Idempotency model                  | Caller-supplied idempotency key IS the executor's primary key (CR `metadata.name` etc.); enables crash recovery via plain Status call | 0004 | §8             |
| Webhook context                    | OTel via W3C `traceparent` headers; campaign-side fields opaque `caller_metadata` JSON                                | 0005 | §10.1, §10.2   |
| Batch aggregated_status storage    | Persisted column under `SELECT ... FOR UPDATE` row lock; cancel of mixed-result batch → `cancelled` (not `partial`)   | 0006 | §10.2, §12.3   |
| Migration step 5 split             | 5a (receiver) → 5b (per-system soak, watcher shadowed) → 5c (delete watcher, irreversible)                            | 0007 | §11            |
| Discovery responsibility           | Outside aegis-chaos; helm chart version binds Point Manifest; `:discover` endpoint removed                            | 0008 | §2, §5, §6     |
| Manifest delivery                  | Helm post-install / post-delete Job hook calls aegisctl manifest validate + dry-run + import                          | 0009 | §6             |
| Manifest validation                | Offline (aegisctl + bundled JSON Schema) + online (`:import?dry_run=true`); `GET /manifest-schema.json` for fetch     | 0010 | §6             |
| Service identity                   | 4-tuple `(system, name, instance, chart_version)`; `import_locks` table serialises per-instance imports; liveness reporter retires orphaned rows | 0011 | §3, §4, §6, §7, §12.4 |
| Batch terminal stickiness          | Terminal `aggregated_status` is sticky; late child terminal updates child row only, no recompute, no second webhook | 0006 (amended) | §12.3 I8 |
| Idempotency key burn               | After terminal, same key returns history and never re-runs Apply                                                      | — | §12.3 I7       |
| Multi-cluster                      | per-cluster aegis-chaos; backend `chaos_endpoints` registry; no central catalog                                        | —   | §2, §10.3      |
| Workflow API                       | Not exposed; sequencing belongs to caller                                                                              | —   | §2             |
| Authentication                     | Bearer tokens with scope claims; no mTLS in v1beta; webhook uses separate token                                       | —   | §10.1, §5 matrix |
| Resource limits                    | Per-system `max_concurrent_injections` only                                                                            | —   | §12.1          |
| Param override UX                  | Effective schema = `capability ∩ point.overrides`; 400 on out-of-range; never auto-clamp                              | —   | §12.2          |

Items intentionally deferred (not blocking v1beta):

- Cross-cluster Point sync — only if shared catalogs become a real need.
- Tenant-aware quotas — only if a second tenant appears.
- Sidecar-agent JVM introspection — only after a 3rd Java benchmark
  or chronic "forgot to reintrospect" failures.
- v1beta → v1 freeze — triggered by first production campaign on the
  API; until then breaking changes are allowed.

## 14. Appendix: Capability Set — placeholder

**This appendix is a placeholder. The authoritative Capability set is
regenerated programmatically during migration step 2.**

The seed list MUST be derived from `ChaosTypeMap` at
`chaos-experiment/handler/handler.go:90-134`. The earlier hand-curated
table that lived here under-counted real chaos types in the codebase
today — notably `ContainerKill`, `JVMRuntimeMutator`, the network
variants `NetworkLoss / NetworkDuplicate / NetworkCorrupt /
NetworkBandwidth`, JVM resource stressors (`JVMGarbageCollector`,
`JVMCPUStress`, `JVMMemoryStress`, `JVMMySQLLatency`,
`JVMMySQLException`), and HTTP request-rewrite variants
(`HTTPRequestReplacePath/Method`, `HTTPResponseReplaceCode`). Any
hand-edited list will drift.

Migration step 2 deliverable: a generated SQL seed for the
`capabilities` table that mirrors `ChaosTypeMap` 1:1, with hand-written
`target_schema` / `param_schema` / `observable_contract` per
Capability. The generator + schemas live under
`aegis-chaos/cmd/capgen/` and run in CI to detect upstream
`ChaosTypeMap` changes.
