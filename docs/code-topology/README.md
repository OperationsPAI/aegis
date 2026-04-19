# AegisLab Code Topology — Recovered Index

Source-of-truth: code under `/home/ddq/AoyangSpace/aegis/AegisLab/src/` as of 2026-04-19 (commit 76c1b9e).
Docs were ignored — every claim below cites `file.go:line`.

## Documents

- **`README.md`** (this file) — system overview, module graph, per-package summary.
- **[`flows.md`](flows.md)** — end-to-end call paths for the ~7 critical flows (inject→collect, HTTP lifecycle, task queue, rate-limiter GC, OTLP logs, config hot-reload, SSE/WS).
- **[`reference.md`](reference.md)** — entity catalog, FK graph, Redis key catalog, K8s label/annotation schema, DTO↔entity map.
- **[`orphans.md`](orphans.md)** — dead code, stale routes, inversion bugs, magic numbers, security red flags.
- **[`slices/`](slices/)** — raw per-layer map reports (1000+ lines of detail per slice), produced by the parallel recovery pass. Authoritative for specific file:line citations.

## 1. Scope of the workspace

`workspace.yaml` declares four repos:

| Repo | Role | Stack | Relationship |
|---|---|---|---|
| `AegisLab/` | Backend — the subject of this topology. Single Go module `aegis`, 253 `.go` files, ~45k LoC. | Go + Python SDK | owns OpenAPI, writes datapack artifacts |
| `AegisLab-frontend/` | UI | TS/React (Vite + axios) | consumes `/api/v2/*` and SSE/WS endpoints |
| `chaos-experiment/` | Fault-injection library (OperationsPAI fork; `go.mod:63` `replace` to local path) | Go | AegisLab imports `handler`, `client`, `pkg/guidedcli` |
| `rcabench-platform/` | RCA eval platform | Python | reads the datapack dir written by AegisLab (`injection.json`, `env.json`, `{normal,abnormal}_{traces,metrics}.parquet`) |

> The stale CLAUDE.md reference to `LGU-SE-Internal/chaos-experiment` is **not** reflected in code — only the OperationsPAI fork is imported. See `slices/06-clients-seams.md` §4a.

## 2. Backend binary: three modes

The binary is `rcabench` (`main.go:73-79`). Startup differs per mode:

| Mode | `main.go` lines | Producer HTTP | Consumer loop | K8s controller | OTLP log receiver | Rate-limit GC |
|---|---|---|---|---|---|---|
| `producer` | 97-115 | ✓ | — | **skipped** | — | — |
| `consumer` | 118-155 | — | ✓ (`ConsumeTasks` blocks; `StartScheduler` goroutine) | ✓ | ✓ | ✓ (`runRateLimiterStartupGC`) |
| `both` (default dev) | 158-203 | ✓ | ✓ | ✓ | ✓ | ✓ |

Implication: a pure-producer deployment cannot observe CRD/Job lifecycle events — the workflow chain depends on running at least one consumer.

## 3. Layered architecture (single repo)

```
                                 ┌──────────────────────────────┐
                                 │ cmd/aegisctl/   (CLI client) │
                                 └──────────────┬───────────────┘
                                                │ HTTP REST / SSE / WS
                                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  router/ (gin) ── middleware/ (JWTAuth, RequireX perms, audit, CORS, …) │
│        │                                                                 │
│        ▼                                                                 │
│  handlers/v2/*   +  handlers/system/*                                    │
│        │                                                                 │
│        ▼                                                                 │
│  service/producer/*   (sync business logic; 30 files)                    │
│        │        ├── service/common/*   (SubmitTask, label helpers,      │
│        │        │                        config-listener, metadata)     │
│        │        └── service/analyzer/* (evaluation persistence)         │
│        ▼                                                                 │
│  repository/*   (25 files; GORM)                                         │
│        │                                                                 │
│        ▼                                                                 │
│  database/*   (entities, GORM bootstrap, detector views, sdk tables)     │
└───────────────────────────┬─────────────────────────────────────────────┘
                            │
            ┌───────────────┴────────────────┐
            ▼                                 ▼
      ┌─────────────┐            ┌────────────────────────────┐
      │ MySQL       │            │ Redis                       │
      │ (GORM)      │            │  - queues task:{delayed,    │
      └─────────────┘            │    ready,dead,index}        │
                                 │  - trace/group/notif streams│
                                 │  - monitor:ns:* locks       │
                                 │  - token_bucket:*           │
                                 │  - joblogs:<task> pub/sub   │
                                 └────────────┬────────────────┘
                                              │
┌─────────────────────────────────────────────┴───────────────────────────┐
│  service/consumer/* (async workers)                                      │
│    task.go            — ConsumeTasks BRPOP, StartScheduler (1s tick)    │
│    distribute_tasks.go — switch task.Type → 6 executors                 │
│    fault_injection.go, build_datapack.go, algo_execution.go,            │
│    collect_result.go, build_container.go, restart_pedestal.go           │
│    k8s_handler.go     — implements k8s.Callback (CRD + Job events)      │
│    monitor.go         — namespace lock manager                          │
│    rate_limiter.go    — token-bucket semaphores (SET-based)             │
│    config_handlers.go — etcd dynamic-config handlers                    │
│    trace.go           — infer Trace state from Task tree, group SSE     │
│                                                                          │
│  service/logreceiver/  — OTLP /v1/logs HTTP → Redis PubSub joblogs:*    │
│                                                                          │
│  client/k8s/*          — controller-runtime informers for               │
│                          Chaos Mesh CRDs (7 kinds) + Job/Pod            │
└──────────────────────────────────────────────────────────────────────────┘

External clients (all in client/):
   etcd (config state)  |  harbor (image tags)  |  helm (pedestal install)
   loki (historical logs)|  jaeger→OTLP (traces)|  redis (everything)
```

## 4. Inbound edges: who calls whom

| Target layer | Called from |
|---|---|
| `router/` | `main.go` only |
| `handlers/` | `router/v2.go`, `router/system.go` |
| `middleware/` | `router/*`, and internal: `handlers/v2/*` for `GetCurrentUserID` helpers |
| `service/producer/` | `handlers/v2/*` (all 24 domain handlers), `handlers/system/*`, `middleware/permission.go` (perm/team/project/public helpers), `middleware/audit.go` (`LogUserAction`/`LogFailedAction`) |
| `service/analyzer/` | `handlers/v2/evaluations.go` only |
| `service/common/` | `service/producer/*` (label + task helpers), `service/consumer/*` (task submission + config handlers), `service/initialization/*` |
| `service/consumer/` | `main.go` (`NewHandler`, `StartScheduler`, `ConsumeTasks`); NOT called from producer or handlers |
| `service/logreceiver/` | `main.go` only |
| `repository/` | `service/producer/*` (primary), `service/common/*` (task submit, metadata, label), `service/consumer/*` (task lifecycle), and 3 handler layer-leaks: `handlers/v2/tasks.go:208`, `handlers/v2/pedestal_helm.go` (whole file), `handlers/system/health.go` |
| `database.DB` | `repository/*`, `service/consumer/trace.go`, `service/producer/metrics.go` (raw GORM), `handlers/v2/pedestal_helm.go`, `handlers/system/health.go` |
| `client/k8s/` | `main.go`, `service/consumer/*`, `handlers/system/health.go` |
| `client/helm.go` | `service/consumer/restart_pedestal.go`, `service/producer/container.go` (chart upload) |
| `client/etcd_client.go` | `service/common/{config_listener,config_registry}.go`, `service/producer/dynamic_config.go` |
| `client/harbor_client.go` | `service/producer/container.go` (tag lookup) |
| `client/loki.go` | `service/producer/{task,injection}.go` (historical log backfill) |
| `client/redis_client.go` | pervasive; every service subdir |
| `client/jaeger.go` (OTLP HTTP exporter despite the name) | `main.go:106,134,176` (InitTraceProvider) |
| `client/debug/` | `handlers/debug.go` only |
| `tracing/` | `service/consumer/*`, `client/k8s/job.go`, `client/helm.go` (only 3 callers in production code) |
| `utils/` | universal; every layer |
| `consts/` | universal |
| `dto/` | `handlers/*`, `service/*` |

## 5. Key invariants recovered from code

1. **`common.SubmitTask`** (`service/common/task.go:51`) is the single canonical async enqueue. No other path writes to Redis queues. All 5 producer enqueue functions and the consumer CRD/Job callbacks funnel through it.
2. **Workflow chain is driven by Kubernetes lifecycle callbacks**, not an in-process workflow engine. See `flows.md §1`.
3. **Soft-delete is NOT `gorm.DeletedAt`.** Every entity uses `consts.StatusType` (`-1=deleted, 0=disabled, 1=enabled`), typically backed by a virtual `Active*` column plus a unique index over non-deleted rows (`slices/05-data.md §1`).
4. **Rate-limit buckets are Redis SETs of taskIDs**, not counters. Capacity is `len(SMEMBERS) ≤ cap`. GC reclaims tokens whose task row is missing or in terminal state.
5. **Two inject-spec pipelines coexist**: legacy `Friendly→chaos.Node` (`utils/fault_translate.go` + `service/producer/spec_convert.go`) and new `pkg/guidedcli.GuidedConfig` (`service/producer/injection.go:1246`, `service/consumer/fault_injection.go:156`). The branch-point is `parseBatchGuidedSpecs` vs `parseBatchInjectionSpecs` in `service/producer/injection.go:806` → `ProduceRestartPedestalTasks`. The HTTP entrypoint is a single endpoint `POST /api/v2/projects/:pid/injections/inject`; the shape of each spec entry determines dispatch.
6. **`/api/v2/injections/metadata` and `/api/v2/injections/translate` are registered routes but return HTTP 410** (`handlers/v2/injections.go:130, 677`). The memory note was correct in spirit — agent 1 saw the router registration, agent 2 confirmed the handler bodies are 410.
7. **SSE/WS replay + tail**: all three SSE endpoints (`/groups/{id}/stream`, `/traces/{id}/stream`, `/notifications/stream`) and the WS `/tasks/{id}/logs/ws` read from the corresponding Redis streams (`group:%s:log`, `trace:%s:log`, `notifications:global`) or pub/sub channel (`joblogs:<task>`). All replay history then live-tail.
8. **OTLP log pipeline is pub/sub, not a stream** — `joblogs:<taskID>` uses `RedisPublish/Subscribe`, so a log emitted while no WS viewer is connected is dropped (`service/logreceiver/receiver.go:246`).

## 6. Per-package role map (one-screen reference)

| Package | Files | Role | Key outbound |
|---|---|---|---|
| `aegis` (main) | 1 | Cobra entry, mode dispatch | config, database, router, service/consumer, service/initialization, service/logreceiver, producer.GCRateLimiters, chaosCli.InitWithConfig |
| `aegis/config` | 2 | viper load + runtime-mutable chaos system singleton | consts; callers: main, middleware-free global state, service/initialization/systems |
| `aegis/consts` | 7 | enums, Redis key patterns, label keys, EventTypes, permission rules, validation whitelists | none; universally imported |
| `aegis/client` | 6 + debug/ | external deps (Redis, etcd, Harbor, Helm, Loki, OTLP exporter, debug registry) | respective SDKs |
| `aegis/client/k8s` | 5 | controller-runtime informer for Chaos Mesh CRDs + Jobs; `CreateJob` | chaosCli.GetCRDMapping |
| `aegis/database` | 6 | entities, `InitDB`, `createDetectorViews`, SDK table read-through | gorm/mysql, chaos.Groundtruth type |
| `aegis/repository` | 25 | GORM DAO layer; Redis queue I/O for tasks | gorm, clause, redis v9 |
| `aegis/dto` | 35 | request/response/validation + `UnifiedTask` + stream event types | chaos types, guidedcli types, consts |
| `aegis/router` | 3 | gin routing: `/system/*`, `/api/v2/*`, `/docs/*` | middleware, handlers |
| `aegis/middleware` | 5 | JWT, permission/role gates, audit post-flight, in-mem rate-limit, group-id tagger | producer perm helpers, consts |
| `aegis/handlers/v2` | 24 files + `pedestalhelm/` subpkg | REST handlers, SSE/WS | producer, analyzer, tasks.go leaks to repository |
| `aegis/handlers/system` | 4 | `/system/audit`, `/system/configs`, `/system/health`, `/system/monitor/*` | producer (ListConfigs, InspectLock, ListQueuedTasks…), database directly (health) |
| `aegis/service/producer` | 30 | sync business logic | repository, common, client, dto, config, chaos types, guidedcli.BuildInjection |
| `aegis/service/common` | 10 | SubmitTask, label dedup, parameter template render, config etcd watcher, DBMetadataStore | repository, client, dto, consts |
| `aegis/service/analyzer` | 1 | evaluation batch compute + persist | repository, common, database.DB |
| `aegis/service/consumer` | 14 (1 dead) | task queue loop + scheduler + executors + K8s callback impl + monitor + rate-limiter + trace state inference | common.SubmitTask, repository, client/k8s, client/helm, chaos.BatchCreate, guidedcli.BuildInjection, tracing.WithSpan |
| `aegis/service/logreceiver` | 2 | OTLP /v1/logs → Redis pub/sub `joblogs:<taskID>` | client.RedisPublish |
| `aegis/service/initialization` | 6 | per-mode bootstrap: seed DB, register chaos systems, register config handlers, warm monitor namespaces | database, common, producer, consumer.RegisterConsumerHandlers, chaos.RegisterSystem |
| `aegis/tracing` | 1 | OTel function-decorator helpers (not middleware) | go.opentelemetry.io/otel |
| `aegis/utils` | 17 | path guards, JWT issuance, SHA-256 password, image-ref parse, zip extract, generic helpers | distribution/reference, golang-jwt/v5, oklog/ulid |
| `aegis/cmd/aegisctl` | 40+ | Cobra CLI (`auth/context/project/container/dataset/inject/execute/task/trace/eval/wait/status/pedestal/cluster/rate-limiter`); out-of-band `cluster preflight` connects directly to k8s/MySQL/Redis/etcd/ClickHouse | aegisctl/client (hand-rolled), guidedcli (inject guided only) |

## 7. How to use this index

- For a **specific file's fan-out** → read the matching slice in `slices/` (table-of-calls is the primary artifact).
- For a **critical flow** → start from `flows.md`.
- For a **data shape** (entity / DTO / Redis key / label) → `reference.md`.
- Before assuming something works as the old docs say → check `orphans.md`. ~25 code-vs-docs drifts are documented there; they're the recovery's main "stale doc" finding.

Generated by a 6-agent map-reduce recovery pass on 2026-04-19. Agent prompts are preserved in the conversation transcript if re-running is needed.
