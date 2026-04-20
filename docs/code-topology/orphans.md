# AegisLab Code-Topology — Orphans, Bugs, Risks (v2)

> Archival note: this file was not fully revalidated after the phase-2 gRPC collapse and phase-6 module-wiring cleanup. Treat `docs/code-topology/README.md`, `docs/code-topology/slices/01-app-wiring.md`, and `docs/code-topology/slices/06-grpc-interfaces.md` as the current topology source of truth.

Findings from the 7-agent recovery pass on the refactored codebase (AegisLab submodule
`42282d0c`). Each item cites `file.go:line` so it can be re-checked against live code.
Divided by severity / kind so the list is actionable.

---

## A. Security red flags

| # | File:line | Risk |
|---|---|---|
| A1 | `utils/jwt.go:16` | **Hard-coded JWT secret** — `JWTSecret = "your-secret-key-change-this-in-production"`. No env override. Used by every token issuer (user / service / API-key exchange). |
| A2 | `utils/access_key_crypto.go:81` | **API-key AES-GCM key is derived from the SAME JWT secret** via `sha256(JWTSecret)`. Leaking the hard-coded string therefore leaks every user's API-key secret (stored in `api_keys.key_secret_ciphertext` column). |
| A3 | `utils/password.go:48-51` | Password hashing is **SHA-256(salt‖password)**, not bcrypt/argon2. Fast brute force is feasible. Unchanged since v1. |
| A4 | `router/router.go:17-22` | CORS is `AllowAllOrigins=true` + `AllowCredentials=true`. Per the CORS spec, gin-contrib/cors generally rejects this combo, but the library may silently accept it and fall back to `*` — validate in prod before assuming credentials flow to browsers. |
| A5 | `middleware/audit.go:22` | `AuditMiddleware()` is **defined but not wired** into `router.New` (router.go:25-32 includes only InjectService/RequestID/GroupID/SSEPath/CORS/Tracer). All mutating user actions are silently **not audited**, despite `dbBackedMiddlewareService.LogUserAction/LogFailedAction` being fully implemented. |
| A6 | `middleware/api_key_scope.go:75-97` | `RequireAPIKeyScopesAny` short-circuits with `c.Next()` when `auth_type != "api_key"` — intended, but means SDK scope gates are bypassed by any user-JWT bearer. Actual scope enforcement for API-key clients happens only at the token-exchange step (`ExchangeAPIKeyToken`). |
| A7 | `module/auth/handler.go` (WS) + `middleware/auth.go` | The `/api/v2/tasks/:id/logs/ws` endpoint authenticates via `?token=` query param (WebSocket can't carry custom headers). Token values end up in URLs and access logs. Unavoidable for WS, but worth knowing. |
| A8 | `module/auth/token_store.go` (JWT blacklist) | Blacklist lookup uses Redis SCAN (`blacklist:token:*`) — cost scales with blacklist size on every login/refresh. |
| A9 | `interface/grpc/*/lifecycle.go` | All 5 gRPC servers use plain TCP, no TLS. Config keys exist for `addr` and `reflection` toggle only. |

## B. Workflow correctness bugs

| # | File:line | Bug |
|---|---|---|
| B1 | `service/consumer/distribute_tasks.go:34-49` | **`TaskTypeCronJob` has no dispatch case.** `dispatchTask` defaults to `"unknown task type: 6"` if one reaches the worker. Only `processDelayedTasks` (scheduler) knows how to reschedule cron tasks. Old bug survived the refactor. |
| B2 | `service/consumer/k8s_handler.go:822-829` | `batchID` absence check is `if !ok && batchID == ""` — tautological (`ok=false` implies `batchID==""`), should be `||`. Currently harmless but easy to misread into a real bug. |
| B3 | `service/consumer/task.go:343` | `ctxWithCancel.cancel` is discarded via `_ = cancel` inside `executeTaskWithRetry` — context-cancel leak per retry attempt. Unchanged since v1. |
| B4 | `service/consumer/fault_injection.go:42-97` | `batchManager` state is **in-memory only** (no Redis persistence). Worker restart in the middle of a hybrid batch leaves the batch permanently pending — BuildDatapack never submits. |
| B5 | `module/injection/service.go:477-479` | `Service.GetMetadata` stub returns `(nil, nil)` — the endpoint still returns 410 Gone at the handler. Dead stub leftover from deprecation. |
| B6 | `service/consumer/redis.go:12-14` | `consumerDetachedContext()` returns `context.TODO()`. Worker context is intentionally lost on CRD/Job callbacks, so OTel carriers on annotations/labels are **load-bearing** — if they're ever missing, there's no span continuity. |
| B7 | `module/trace/service.go:80-83` | Filters out the detector algorithm from the "algorithms to wait for" set by calling `config.GetDetectorName()`. Tightly couples `module/trace` to the detector-name runtime var. Better placed in consumer. |
| B8 | `module/chaossystem/service.go:72,122` | `chaos.RegisterSystem(...)` errors are logged but the HTTP handler still returns 200. Caller thinks the system was registered with chaos-experiment when it may not be. |
| B9 | `module/container/core.go` | `NewService` receives `nil` redis. Any code path that dereferences the nil redis at runtime will panic. Time-bomb. |
| B10 | `module/container/build_gateway.go` | `GithubToken` is embedded in argv for `git clone` — leaks via `/proc/<pid>/cmdline` and `ps`. Should be via env or credential helper. |
| B11 | `module/evaluation/service.go` `persistEvaluations` | Swallows all write errors with warn-only logging — silent data loss. |

## C. Data-layer / validation bugs (mostly inherited)

| # | File:line | Problem |
|---|---|---|
| C1 | `model/view.go` (`addDetectorJoins`) | **Hard-coded `algorithm_id = 1`** in the detector-results JOIN used by `fault_injection_{no_issues,with_issues}` views. If the detector's `container_versions.id` isn't 1, the analysis views show no data. Magic number, no comment. |
| C2 | `consts/validation.go:85` | `ValidConfigScopes` rejects `ConfigScope.Global` even though Global is declared and populated in `ConfigScopeMap`. Any POST carrying `scope=global` fails validation. |
| C3 | `consts/validation.go:90-96` | `ValidDatapackStates` omits `DatapackDetectorFailed` / `DatapackDetectorSuccess` — states the consumer pipeline actually writes. Validation would reject values the system produces. |
| C4 | `consts/validation.go:203` | `ValidTaskTypes` omits `TaskTypeCronJob`. Combined with B1, cron tasks fail validation AND dispatch. |
| C5 | `repository` across modules (`project_statistics.go`, `repository.go` in many modules) | `database.Sort("dataset_id desc")` applied to `fault_injection_no_issues/with_issues` views which expose `datapack_id`, not `dataset_id`. Rename leftover or bug — left as-is in v2. |
| C6 | `consts.ValidTaskTypes`, `ValidTraceTypes`, etc. | Several `ValidX` maps are declared in `consts/validation.go` but have no importers grep-able — stale whitelists that either need wiring or deletion. |

## D. Dead / declared-but-not-wired code

| # | Location | What |
|---|---|---|
| D1 | `middleware/audit.go:22` | `AuditMiddleware()` defined + `dbBackedMiddlewareService.LogUserAction/LogFailedAction` implemented, but **never registered** in `router.New`. Audit is silently disabled. |
| D2 | `middleware/ratelimit.go` | In-memory rate-limiter struct + `GeneralRateLimit`/`AuthRateLimit`/`StrictRateLimit`/`UserRateLimit` vars — **mounted on no route**. Actual rate-limiting is the Redis `module/ratelimiter` (which enforces runtime token buckets, not HTTP rate limits). |
| D3 | `service/consumer/jvm_runtime_mutator.go` | 102 lines, not referenced by the dispatcher. Possibly used only indirectly via `chaos-experiment` plumbing in `fault_injection.go`'s InjectionConf — confirm it's still active. |
| D4 | `service/consumer/task.go:42` (`LastBatchInfoKey`) | Constant declared; **never written or read** anywhere. Dead. |
| D5 | `internalclient/runtimeclient/client.go` | Wraps only 2 of the 6 runtime RPCs (`GetNamespaceLocks`, `GetQueuedTasks`). `GetRuntimeStatus/GetQueueStatus/GetLimiterStatus/Ping` have **no Go client**. If the system-service is the only caller, half the runtime gRPC surface is effectively dead. |
| D6 | `module/docs/` | Module exists and compiles but **is never added to any fx options** — declared to hold Swagger `@Success` annotations, runtime-dead. |
| D7 | `module/systemmetric/repository.go` | Empty stub repository — no methods used anywhere. |
| D8 | `module/ratelimiter/` | Breaks the module convention (no `HandlerService` interface, handler holds `*Service` directly). Cannot be decorated by gateway → runs local-only in every mode that includes it. Not inherently dead, but architecturally anomalous. |
| D9 | `module/pedestal/` and `module/sdk/` | Same convention break as D8. These three modules (`pedestal`, `ratelimiter`, `sdk`) are the 3 untouched by gateway fx.Decorate in `app/gateway/options.go:57-177`. |
| D10 | `/api/v2/injections/metadata` (`module/injection/handler.go:286`) and `/translate` (handler.go:335) | Handlers always return `http.StatusGone`. Swagger `@Router` annotations still advertise them as live endpoints — doc drift. |
| D11 | `/api/v2/notifications/stream` | Backend wired (portal.go:151), writes exist (module/system, ratelimiter GC), but **no frontend caller** (grep confirms). Either awaiting frontend or dead. |
| D12 | `/api/v2/sdk/*` | Backend wired in `router/sdk.go:15,22`, but **frontend never calls any `/sdk/*` path**. SDK surface is machine-only; the UI calls the dual-mounted portal equivalents. |

## E. Naming drift / surprises / partial migration

| # | File | Notes |
|---|---|---|
| E1 | `infra/tracing/` vs `tracing/` (top-level) | `infra/tracing` is the provider module; decorator helpers (`WithSpan`, `WithSpanReturnValue[T]`, etc.) live in the TOP-LEVEL `aegis/tracing` package. Not in `infra/`. |
| E2 | `client/jaeger.go` removed, but config key `jaeger.endpoint` still used — now points to an **OTLP-HTTP** exporter (`infra/tracing/provider.go:18-48` via `otlptracehttp.New`). Name is legacy. |
| E3 | `infra/config/` is a 17-line shim over top-level `aegis/config.Init`. All runtime viper state + the atomic `detectorName` + the chaos system singleton still live in the top-level package (`config/config.go`, `config/chaos_system.go`). |
| E4 | `utils/docker.go:10` | `ParseFullImageRefernce` (spelling error) — exported. Unchanged since v1. |
| E5 | `utils/file.go:20` | Production code imports YAML via `stretchr/testify/assert/yaml`. Also uses `sigs.k8s.io/yaml` in `infra/helm/`. Inconsistent; testify internal subpath in non-test code is odd. |
| E6 | `service/consumer/owner_adapter.go:29-171` | Half-done microservice split. Local `executionOwnerAdapter`/`injectionOwnerAdapter` fall back to in-process service calls; `RemoteOwnerOptions()` decorates to require orchestrator RPC. Both paths ship — the DI seam is the `orchestrator.Enabled()` check. |
| E7 | `interface/grpc/orchestrator/service.go:83-210,464-472,697-705` | `taskQueueController` (`CancelTask`/`RetryTask`/`ListDeadLetterTasks`) lives **inside** the gRPC server file rather than in a module. Orchestration logic that belongs in `module/task/`. |
| E8 | `module/trace/` SSE vs `orchestrator-service` RPC | The gRPC `ReadTraceStreamMessages` etc. are unary polling with `block_millis` (XREAD block), not server-streaming. Clients must poll even though the name suggests streaming. |
| E9 | `runtime-worker-service` config key drift | Uses `clients.runtime.target` / `runtime_worker.grpc.target` — underscore form, unlike `iam.grpc.target` etc. (`slices/06 §1, §2`). |
| E10 | `app/gateway/options.go:57-177` | Gateway still runs a full `ProducerInitializer` legacy startup hook (`app/producer.go:60-66`) and wires `DataOptions + CoordinationOptions + BuildInfraOptions + chaos + k8s`. Heavier than a typical edge gateway. |
| E11 | `app/both.go:5-10` | `BothOptions` doesn't explicitly include `ExecutionInjectionOwnerModules()` — relies on transitive inclusion via `ProducerHTTPModules` (http_modules.go:45). Inconsistent style with `consumer.go` which includes it explicitly. |
| E12 | `app/iam/options.go:22-27` | `iam-service → resource-service → orchestrator-service` is a 3-hop RPC chain when the gateway needs to resolve a team's projects. Each hop goes through the Decorator pattern. |
| E13 | `infra/k8s/gateway.go:26-35` | Despite fx providing singleton semantics, `infra/k8s` keeps `sync.Once` package-globals (`k8sRestConfigOnce`, `k8sClientOnce`, `k8sDynamicClientOnce`, `controllerOnce`). Gateway type is window-dressing over package state. |
| E14 | `infra/k8s/controller.go:236-237` | `RemoveNamespaceInformers` admits via comment that informers cannot be gracefully stopped — it just flips `activeNamespaces[ns]=false` and relies on every event handler to check `isNamespaceActive`. Any new handler forgetting the check processes stale events. |
| E15 | `infra/db/module.go` | `NewGormDB` runs `AutoMigrate` + `createDetectorViews` inside the constructor (not a Lifecycle OnStart hook) — schema work happens at fx graph BUILD time, before `app.Start()`. A panic mid-migration means the OnStop close hook is never registered. |
| E16 | `infra/chaos/module.go` | `chaos.Initialize(*rest.Config)` → `chaosCli.InitWithConfig`. Package implicitly depends on `k8s.Module` providing `*rest.Config`. Nothing prevents wiring chaos without k8s; fx would fail at resolution. |
| E17 | `cmd/aegisctl/cmd/inject_guided.go` | `aegisctl inject guided` runs **purely local YAML I/O** unless `--apply` is passed. With `--apply`, it hits the same endpoint as `inject submit`; backend dispatches on spec shape. |
| E18 | `cmd/aegisctl/cmd/cluster.go` | `cluster preflight` connects directly to k8s/MySQL/Redis/etcd/ClickHouse — out-of-band, NOT via the AegisLab HTTP API. |
| E19 | `middleware/middleware.go:23` SSEPath regex | Regex is `^/stream(/.*)?$` — **never matches** the actual SSE routes `/traces/:id/stream`, `/groups/:id/stream`, `/notifications/stream` because those don't start with `/stream`. Response headers therefore must be set by each handler individually, or the middleware is a silent no-op for SSE. |
| E20 | `interface/http/ServerConfig` | Only has `Addr` — no TLS, no read/write timeouts, no graceful-shutdown timeout. |
| E21 | `router/router.go:22` | CORS `ExposeHeaders` exposes `X-Request-Id` but not `X-Group-ID` — browser JS reading `res.headers.get('X-Group-Id')` will see null. |
| E22 | `module/trace/handler.go` / `module/group/handler.go` | SSE endpoints duplicate the "replay XRANGE then XREAD tail" skeleton per module. Consider a shared stream replay util. |

## F. What was correct in older docs and worth restating

- `GetInjectionMetadata` and `TranslateFaultSpecs` **do return HTTP 410** at the handler
  (`module/injection/handler.go:286, 335`). Routes survive only as 410 stubs — the guided-CLI
  migration is complete at the wire level.
- `LGU-SE-Internal/chaos-experiment` is NOT imported anywhere. Only the OperationsPAI fork
  is used (`go.mod:63` replace → local `../../chaos-experiment`). Unchanged from v1.
- The three chaos-experiment subpackages used by AegisLab are still `handler`, `client`,
  `pkg/guidedcli` — nothing else.

## G. Things the recovery couldn't fully verify

1. **`consts.NotificationStreamKey` writer chain** — `module/notification/` is read-only; the
   write is somewhere in `module/system`, `module/ratelimiter.GC`, or other cross-slice
   code. Confirm which modules actually XADD to `notifications:global`.
2. **`module/docs/`** — declared but never wired; double-check it doesn't leak via another
   fx graph.
3. **`systemmetric.Repository`** stub — possibly intentional for future use, or should be
   deleted.
4. **`runtimeclient` partial coverage** — is the system-service the only consumer? If so,
   should the 4 unused typed RPCs be removed from the runtime proto?

These are the right targets for the next verification loop — pick one, follow the code,
update this doc.
