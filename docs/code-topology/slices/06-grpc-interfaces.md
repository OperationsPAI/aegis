# 06 — Inter-service gRPC surface

All paths rooted at `/home/ddq/AoyangSpace/aegis/AegisLab/src`.

## 1. Per-service gRPC contract

### iam-service

- Proto package: `aegis/proto/iam/v1`
- Proto file: `proto/iam/v1/iam.proto` (248 lines)
- Generated pb: `proto/iam/v1/iam.pb.go`, `proto/iam/v1/iam_grpc.pb.go`
- gRPC service: `IAMService` (fully qualified: `iam.v1.IAMService`)
- 58 RPCs (proto/iam/v1/iam.proto:11-71):
  - `VerifyToken(VerifyTokenRequest) → VerifyTokenResponse`
  - `CheckPermission(CheckPermissionRequest) → CheckPermissionResponse`
  - `Login/Register/RefreshToken(MutationRequest) → StructResponse`
  - `Logout(LogoutRequest) → Empty`
  - `ChangePassword(UserBodyRequest) → Empty`
  - `GetProfile(UserIDRequest) → StructResponse`
  - API-keys: `CreateAPIKey/ListAPIKeys/GetAPIKey/DeleteAPIKey/DisableAPIKey/EnableAPIKey/RevokeAPIKey/RotateAPIKey` (UserBody/UserQuery/UserScopedIDRequest variants)
  - Membership: `IsUserTeamAdmin/IsUserInTeam/IsTeamPublic/IsUserProjectAdmin/IsUserInProject(... → BoolResponse)`
  - `ExchangeAPIKeyToken(ExchangeAPIKeyTokenRequest) → ExchangeAPIKeyTokenResponse`
  - Users CRUD: `CreateUser/DeleteUser/GetUser/ListUsers/UpdateUser`
  - User bindings: `AssignUserRole/RemoveUserRole/AssignUserPermissions/RemoveUserPermissions/Assign|RemoveUserContainer/Dataset/Project`
  - Roles: `CreateRole/DeleteRole/GetRole/ListRoles/UpdateRole/AssignRolePermissions/RemoveRolePermissions/ListUsersFromRole`
  - Permissions/Resources: `GetPermission/ListPermissions/ListRolesFromPermission/GetResource/ListResources/ListResourcePermissions`
  - Teams: `CreateTeam/DeleteTeam/GetTeam/ListTeams/UpdateTeam/ListTeamProjects/AddTeamMember/RemoveTeamMember/UpdateTeamMemberRole/ListTeamMembers`
- Server adapter: `interface/grpc/iam/service.go`. Delegates to 6 modules' Handler services (see lines 26-52 for struct fields; all RPCs dispatch to them). Mapping table:

  | RPC | Local call |
  | --- | --- |
  | VerifyToken | `auth.Service.VerifyToken`/`VerifyServiceToken` (iam/service.go:59,77) |
  | CheckPermission | `middleware.Service.CheckUserPermission` (iam/service.go:106) |
  | Login/Register/RefreshToken/Logout/ChangePassword/GetProfile | `auth.HandlerService` (`authAPI`) |
  | *APIKey | `authAPI.{Create,List,Get,Delete,Disable,Enable,Revoke,Rotate}APIKey` |
  | ExchangeAPIKeyToken | `auth.Service.ExchangeAPIKeyToken` (iam/service.go:738) |
  | IsUser* / IsTeamPublic | `middleware.Service` (iam/service.go:669-721) |
  | Create/Delete/Get/List/UpdateUser + Assign/Remove* | `user.HandlerService` |
  | Create/Delete/Get/List/UpdateRole + role-perms + perms + resources | `rbac.HandlerService` |
  | Create/Delete/Get/List/UpdateTeam + team-members/projects | `team.HandlerService` |

- Lifecycle: `interface/grpc/iam/lifecycle.go:30-52`. Default addr `:9091`, config keys `iam.grpc.addr`, `iam.grpc.reflection`. Wraps with `httpx.UnaryServerRequestIDInterceptor` (lifecycle.go:31). Health via `grpc_health_v1` (lines 34-37). **No TLS** — plain TCP listener (lifecycle.go:59). Graceful stop via `server.GracefulStop()` (line 80). fx hook at `registerLifecycle` (line 84).
- Module: `interface/grpc/iam/module.go:5-11` — provides `newIAMServer`, `newLifecycle`; invokes `registerLifecycle`.
- Test hints: `service_test.go` builds `iamServer` with stub `middlewareStub`/`teamHandlerStub` and asserts each RPC's struct-encoding + validation paths (38+ sub-tests; `TestIAMServerVerifyTokenUser` 105, `TestIAMServerCheckPermission` 147).

### orchestrator-service

- Proto package: `aegis/proto/orchestrator/v1`
- Proto file: `proto/orchestrator/v1/orchestrator.proto` (192 lines)
- gRPC service: `OrchestratorService`
- 28 RPCs (proto/orchestrator/v1/orchestrator.proto:10-38):
  - `Ping`, `SubmitExecution`, `SubmitFaultInjection`, `SubmitDatapackBuilding`
  - Runtime mutations (all `MutationRequest → StructResponse`): `CreateExecution`, `CreateInjection`, `UpdateExecutionState`, `UpdateInjectionState`, `UpdateInjectionTimestamps`, `GetInjectionMetrics`, `GetExecutionMetrics`, `ListEvaluationExecutionsByDatapack`, `ListEvaluationExecutionsByDataset`
  - `CancelTask`, `GetExecution`, `ListProjectStatistics`, `GetTask`, `PollTaskLogs`, `ListTasks`
  - Traces/groups: `GetTrace`, `ListTraces`, `GetGroupStats`
  - Streams: `GetTraceStreamState`, `ReadTraceStreamMessages`, `GetGroupStreamState`, `ReadGroupStreamMessages`, `ReadNotificationStreamMessages`
  - Dead-letter: `ListDeadLetterTasks`, `RetryTask`
- Server adapter: `interface/grpc/orchestrator/service.go`. Ten-field server struct (lines 236-247). Mapping:

  | RPC | Local call |
  | --- | --- |
  | SubmitExecution | `execution.Service.SubmitAlgorithmExecution` (service.go:295) |
  | SubmitFaultInjection | `injection.Service.SubmitFaultInjection` (service.go:339) |
  | SubmitDatapackBuilding | `injection.Service.SubmitDatapackBuilding` (service.go:383) |
  | CreateExecution | `execution.Service.CreateExecutionRecord` (service.go:409) |
  | CreateInjection | `injection.Service.CreateInjectionRecord` (service.go:422) |
  | UpdateExecutionState | `execution.Service.UpdateExecutionState` (service.go:434) |
  | UpdateInjectionState | `injection.Service.UpdateInjectionState` (service.go:445) |
  | UpdateInjectionTimestamps | `injection.Service.UpdateInjectionTimestamps` (service.go:456) |
  | GetInjectionMetrics/GetExecutionMetrics | `metric.Service.Get{Injection,Execution}Metrics` (service.go:493,508) |
  | CancelTask/RetryTask/ListDeadLetterTasks | `taskController` — in-file `taskQueueController` wrapping `redisinfra.Gateway` + `consumer.CancelTask` (service.go:83-210, 464-472, 697-705) |
  | GetExecution | `execution.Service.GetExecution` (service.go:478) |
  | ListProjectStatistics | `projectStatisticsReader.ListProjectStatistics` → `project.Repository.ListProjectStatistics` (project_statistics.go:20) |
  | ListEvaluationExecutionsBy{Datapack,Dataset} | `execution.Service.ListEvaluationExecutionsBy...` (service.go:534,546) |
  | GetTask/PollTaskLogs/ListTasks | `task.Service.{GetDetail,PollLogs,List}` (service.go:557,572,587) |
  | GetTrace/ListTraces | `trace.Service.{GetTrace,ListTraces}` (service.go:598,613) |
  | GetGroupStats/GetGroupStreamState | `group.Service.{GetGroupStats,GetGroupTraceCount}` (service.go:624,657) |
  | GetTraceStreamState | `trace.Service.GetTraceStreamAlgorithms` (service.go:635) |
  | ReadTraceStreamMessages | `trace.Service.ReadTraceStreamMessages` (service.go:646) |
  | ReadGroupStreamMessages | `group.Service.ReadGroupStreamMessages` (service.go:668) |
  | ReadNotificationStreamMessages | `notification.Service.ReadStreamMessages` (service.go:682) |

- Lifecycle: `interface/grpc/orchestrator/lifecycle.go:30-52`. Default `:9092`; keys `orchestrator.grpc.{addr,reflection}`. Same Unary interceptor + health + graceful stop pattern.
- Module: `interface/grpc/orchestrator/module.go:9-18`. Provides `project.NewRepository`, `newProjectStatisticsReader`, `newTaskQueueController`, `newOrchestratorServer`, `newLifecycle`.
- Test hints: `service_test.go` — 14 tests covering submit + runtime mutations + dead-letter queue + retry + task/trace/group/notification streaming using in-memory stubs.

### resource-service

- Proto package: `aegis/proto/resource/v1`
- Proto file: `proto/resource/v1/resource.proto` (105 lines)
- gRPC service: `ResourceService`
- 25 RPCs (proto/resource/v1/resource.proto:11-35):
  - `Ping`
  - Projects RO: `ListProjects`, `GetProject`
  - Containers RO: `ListContainers`, `GetContainer`
  - Datasets RO: `ListDatasets`, `GetDataset`
  - Labels CRUD: `CreateLabel/GetLabel/ListLabels/UpdateLabel/DeleteLabel/BatchDeleteLabels`
  - ChaosSystems CRUD + metadata: `ListChaosSystems/GetChaosSystem/CreateChaosSystem/UpdateChaosSystem/DeleteChaosSystem/UpsertChaosSystemMetadata/ListChaosSystemMetadata`
  - Evaluations: `ListDatapackEvaluationResults/ListDatasetEvaluationResults/ListEvaluations/GetEvaluation/DeleteEvaluation`
- Server adapter: `interface/grpc/resource/service.go`. Six-field struct (lines 73-81). Mapping:

  | RPC | Local call |
  | --- | --- |
  | ListProjects/GetProject | `project.Service.{ListProjects,GetProjectDetail}` (service.go:119,131) |
  | ListContainers/GetContainer | `container.Service.{ListContainers,GetContainer}` (service.go:147,159) |
  | ListDatasets/GetDataset | `dataset.Service.{ListDatasets,GetDataset}` (service.go:175,187) |
  | Label* | `label.HandlerService.{Create,GetDetail,List,Update,Delete,BatchDelete}` (service.go:203-275) |
  | ChaosSystem* | `chaossystem.HandlerService.{ListSystems,GetSystem,CreateSystem,UpdateSystem,DeleteSystem,UpsertMetadata,ListMetadata}` (service.go:287-377) |
  | Evaluation* | `evaluation.Service.{ListDatapackEvaluationResults,ListDatasetEvaluationResults,ListEvaluations,GetEvaluation,DeleteEvaluation}` (service.go:393-453) |

- Lifecycle: `interface/grpc/resource/lifecycle.go:30-52`. Default `:9093`; keys `resource.grpc.{addr,reflection}`.
- Module: `interface/grpc/resource/module.go:5-11`.
- Test hints: `service_test.go` — 9 tests exercising list/get/validation for projects/datasets/containers/labels/chaos-systems/evaluations with stubs.

### runtime-service

- Proto package: `aegis/proto/runtime/v1`
- Proto file: `proto/runtime/v1/runtime.proto` (84 lines)
- gRPC service: `RuntimeService`
- 6 RPCs (proto/runtime/v1/runtime.proto:10-15):
  - `Ping(PingRequest) → PingResponse`
  - `GetRuntimeStatus(RuntimeStatusRequest) → RuntimeStatusResponse`
  - `GetQueueStatus(QueueStatusRequest) → QueueStatusResponse`
  - `GetLimiterStatus(LimiterStatusRequest) → LimiterStatusResponse`
  - `GetNamespaceLocks(PingRequest) → StructResponse`
  - `GetQueuedTasks(PingRequest) → StructResponse`
- Server adapter: `interface/grpc/runtime/service.go`. Built via `runtimeServerParams` fx.In at line 26 with DB + Redis/K8s/BuildKit/Helm gateways and three named rate limiters (`restart_limiter`, `build_limiter`, `algo_limiter`). All logic goes through `consumer.RuntimeSnapshotService` (constructed at service.go:47-56). `GetNamespaceLocks`/`GetQueuedTasks` bypass snapshot service and query `redis.Gateway` directly (service.go:130-144, 146-191, 193-214).
- Lifecycle: `interface/grpc/runtime/lifecycle.go:30-52`. Default `:9094`; **config keys use `runtime_worker.grpc.{addr,reflection}`** (note the underscore mismatch vs other services).
- Module: `interface/grpc/runtime/module.go:5-11`.
- Test hints: `service_test.go:13 TestRuntimeServerStatusEndpoints` — sole happy-path test asserting the 3 typed status RPCs return populated snapshots from a stub consumer.

### system-service

- Proto package: `aegis/proto/system/v1`
- Proto file: `proto/system/v1/system.proto` (51 lines)
- gRPC service: `SystemService`
- 12 RPCs (proto/system/v1/system.proto:10-21):
  - `Ping`, `GetHealth`, `GetMetrics`, `GetSystemInfo`
  - Configs: `ListConfigs`, `GetConfig`
  - Audit: `ListAuditLogs`, `GetAuditLog`
  - Runtime introspection: `ListNamespaceLocks`, `ListQueuedTasks`
  - Metrics: `GetSystemMetrics`, `GetSystemMetricsHistory`
- Server adapter: `interface/grpc/system/service.go`. Two readers in struct (lines 39-43):

  | RPC | Local call |
  | --- | --- |
  | GetHealth/GetMetrics/GetSystemInfo/ListConfigs/GetConfig/ListAuditLogs/GetAuditLog/ListNamespaceLocks/ListQueuedTasks | `system.Service` (systemReader) (service.go:62-151) |
  | GetSystemMetrics/GetSystemMetricsHistory | `systemmetric.Service` (service.go:155-167) |

- Lifecycle: `interface/grpc/system/lifecycle.go:30-52`. Default `:9095`; keys `system.grpc.{addr,reflection}`.
- Module: `interface/grpc/system/module.go:5-11`.
- Test hints: `service_test.go` — 4 tests (`TestSystemServerGetHealth` 80, `TestSystemServerListConfigs` 107, `TestSystemServerGetAuditLogNotFound` 136, `TestSystemServerGetSystemMetricsHistory` 151).

## 2. Per-service gRPC client

Shared pattern (all five): `NewClient(lc fx.Lifecycle)` — reads a target address, returns empty `&Client{}` (disabled, `Enabled()`=false) if unset; otherwise opens `grpc.NewClient` with `insecure.NewCredentials()` + `httpx.UnaryClientRequestIDInterceptor`, stashes a conn + typed `rpc`, and registers `conn.Close()` on fx OnStop. `mapRPCError` converts gRPC status codes back to `consts.Err*` sentinels. Payloads move through `structpb.Struct` via `toStructPB`/`decodeStruct` helpers.

### iamclient — `internalclient/iamclient/client.go`
- Config keys: `clients.iam.target` (preferred) → `iam.grpc.target` fallback (client.go:37-40).
- 60 methods (one per IAMService RPC plus `Enabled`, `VerifyServiceToken`). Satisfies `middleware.TokenVerifier` (assertion client.go:973).
- Module: `internalclient/iamclient/module.go:5-7` — single `fx.Provide(NewClient)`.

### orchestratorclient — `internalclient/orchestratorclient/client.go`
- Config keys: `clients.orchestrator.target` → `orchestrator.grpc.target` (client.go:37-40).
- 28 methods mirroring the 28 RPCs + helpers (ReadTraceStreamMessages etc. re-decode into `redis.XStream`).
- Module: `internalclient/orchestratorclient/module.go`.

### resourceclient — `internalclient/resourceclient/client.go`
- Config keys: `clients.resource.target` → `resource.grpc.target` (client.go:~37).
- 25 methods matching RPCs.
- Module: `internalclient/resourceclient/module.go`.

### runtimeclient — `internalclient/runtimeclient/client.go`
- Config keys: `clients.runtime.target` → `runtime_worker.grpc.target` (client.go:30-33) — **also uses the `runtime_worker` key**.
- Only exports 2 methods: `GetNamespaceLocks` (client.go:66), `GetQueuedTasks` (client.go:77). The typed status RPCs (`GetRuntimeStatus`, `GetQueueStatus`, `GetLimiterStatus`) and `Ping` are NOT wrapped here.
- Module: `internalclient/runtimeclient/module.go`.

### systemclient — `internalclient/systemclient/client.go`
- Config keys: `clients.system.target` → `system.grpc.target`.
- 11 methods (all system RPCs except `Ping`).
- Module: `internalclient/systemclient/module.go`.

## 3. interface/http server

- `interface/http/server.go:13-22` — `ServerConfig{Addr string}`. `NewServer(config, *gin.Engine) *http.Server` returns a raw `http.Server{Addr, Handler: engine}`. **No TLS.** No timeouts set.
- Lifecycle `registerServerLifecycle` (server.go:24-40): OnStart spawns a goroutine running `ListenAndServe`, logs `http.ErrServerClosed` suppressed. OnStop calls `server.Shutdown(ctx)` — graceful.
- Module `interface/http/module.go:10-17` provides `middleware.NewService`, `router.New`, `NewServer`, and invokes `registerServerLifecycle`. Note: `router.New` replaces the old `router.New()+engine.Run()` path from main.go.
- `ServerConfig` provider: supplied by app layer — `app/producer.go:33 fx.Supply(httpapi.ServerConfig{Addr: normalizeAddr(port)})`. Test overrides: `app/startup_smoke_test.go:257,306` and `app/service_entrypoints_test.go:283` via `fx.Replace`. No other consumer; only `NewServer` depends on it.

## 4. Service adjacency (from client imports)

From `app/**/*options.go` and `app/gateway/options.go`:

- `gateway` → **all five** clients (iam + orchestrator + resource + system → decorates HandlerService interfaces; runtime not imported here). `app/gateway/options.go:53-56`.
- `iam` service → `resourceclient` (app/iam/options.go:6,26) — iam needs resource lookups for user-resource bindings (container/dataset/project assignments).
- `resource` service → `orchestratorclient` (app/resource/options.go:6,27) — resource reads evaluation executions from orchestrator.
- `orchestrator` service → (no internal-client deps in app/orchestrator/options.go).
- `runtime` worker → `orchestratorclient` (app/runtime_stack.go:11,22) + `grpcruntime.Module` (line 34) — callback path: runtime consumer → orchestrator.CreateInjection/UpdateInjectionState/CreateExecution/UpdateExecutionState etc. (see `service/consumer/owner_adapter.go:8,30-46`).
- `system` service → `runtimeclient` (app/system/options.go:7,28) — system reads namespace locks + queued tasks from the runtime worker (matching the 2 runtime client methods exactly).

Adjacency list (A → B means A calls B's gRPC):

```
gateway   → iam, orchestrator, resource, system
iam       → resource
resource  → orchestrator
runtime   → orchestrator
system    → runtime
```

## 5. RPC surface map (total 129 RPCs)

| service | total | breakdown |
| --- | --- | --- |
| iam | 61 | 1 token-verify + 1 permission-check + 4 auth (login/register/refresh/logout) + 3 profile/password + 8 api-key + 5 membership-bool + 1 api-key-exchange + 5 user-crud + 10 user-binding (role/perms/container/dataset/project × 2) + 8 role (crud+role-perms+users-from-role) + 3 permission + 3 resource + 10 team |
| orchestrator | 28 | 1 ping + 3 submit (exec/inj/datapack) + 8 runtime-mutation + 2 metrics + 2 task-lifecycle (cancel/retry) + 1 dead-letter + 1 project-stats + 2 evaluation-lookup + 3 task-read + 2 trace + 2 group + 3 stream-read + 1 group-stream-state + 1 trace-stream-state + 1 get-execution |
| resource | 25 | 1 ping + 2 project + 2 container + 2 dataset + 6 label + 7 chaos-system + 5 evaluation |
| runtime | 6 | 1 ping + 3 typed-status (runtime/queue/limiter) + 2 struct-wrapped (namespace-locks/queued-tasks) |
| system | 12 | 1 ping + 3 health/metrics/info + 2 config + 2 audit + 2 runtime-introspection + 2 system-metrics |

(iam total = 61 counting ExchangeAPIKeyToken; proto has exactly 61 `rpc` lines.)

## 6. Runtime controller adapter

**No.** `interface/grpc/runtime/service.go` does NOT expose K8s controller lifecycle events over gRPC. The file has zero occurrences of `Callback`, `HandleCRD`, `HandleJob`, or `controller` (grep in §pre-check). The service exposes only **observability snapshots**: runtime/queue/limiter statuses, namespace locks, queued tasks.

Callback forwarding flows the opposite direction: the runtime worker receives K8s events locally (via `client/k8s/controller.go` per CLAUDE.md) and then *pushes* state to orchestrator over gRPC using `orchestratorclient.Client` (wired in `app/runtime_stack.go:11,22` and consumed via `service/consumer/owner_adapter.go:30,45`). Specifically:

- `executionOwnerAdapter.CreateExecution/UpdateExecutionState` → `orchestratorclient.CreateExecution` / `UpdateExecutionState` (orchestrator.proto:14,16).
- `injectionOwnerAdapter.CreateInjection/UpdateInjectionState/UpdateInjectionTimestamps` → corresponding `Runtime*` RPCs on orchestrator (orchestrator.proto:15,17,18).

So: the CRD-callback → next-task-submission chain stays inside the runtime-worker consumer; only state *writes* are forwarded to orchestrator via gRPC. There is no bidirectional/streaming RPC for lifecycle delegation.

## 7. Differences vs. HTTP `HandlerService`

The project has 19 `HandlerService` interfaces (`module/*/handler_service.go`) owned by the handler layer. Observations:

- Most gRPC RPCs map to a single `HandlerService.*` call (or a direct `*.Service.*` call where the gRPC adapter skips the Handler interface). Examples skipping Handler interface: orchestrator's `execution.Service`, `injection.Service`, `metric.Service`, `task.Service`, `trace.Service`, `group.Service`, `notification.Service` (used directly, not via `HandlerService`). This is because those modules' gRPC-exposed methods are a **read-side subset** that exists on the concrete `Service`.
- Orchestrator-only: `CancelTask`, `RetryTask`, `ListDeadLetterTasks` live only on a private `taskQueueController` / `taskController` interface defined in `interface/grpc/orchestrator/service.go:77-103`; they wrap Redis + `consumer.CancelTask` and do NOT exist on `task.HandlerService`. These are pure gRPC-surface additions (no HTTP twin unless the HTTP handler re-implements them).
- Orchestrator `ListProjectStatistics` talks to `project.Repository` directly (project_statistics.go:16-22), bypassing `project.HandlerService`.
- Runtime `GetRuntimeStatus/GetQueueStatus/GetLimiterStatus` — typed responses, not part of any HandlerService. `GetNamespaceLocks/GetQueuedTasks` are listed in both `runtime.proto` and `system.proto` (the latter as `ListNamespaceLocks/ListQueuedTasks` via `system.Service`). So the HTTP side reaches these via `systemclient` which in turn (in the runtime deployment mode) calls `system.Service.ListNamespaceLocks` — which likely delegates back to `runtimeclient.GetNamespaceLocks` inside gateway mode. Mild fan-out.
- No streaming RPC at all. All are unary. HTTP side uses SSE for trace streams, but gRPC emulates via polling: `ReadTraceStreamMessages/ReadGroupStreamMessages/ReadNotificationStreamMessages` are unary reads with a blocking-timeout parameter. For SDK generation this matters: the gRPC layer cannot drive HTTP SSE directly.
- `iam.ExchangeAPIKeyToken` exists in gRPC (iam.proto:32) but its typed response differs from the generic `StructResponse` used by `Login/Register` — slightly inconsistent wire shape.
- `auth.HandlerService` vs. `auth.Service`: gRPC uses *both* in `iamServer` (fields `auth *auth.Service` and `authAPI auth.HandlerService`), because `VerifyToken`/`ExchangeAPIKeyToken`/`VerifyServiceToken` are only on the concrete `Service`, not on `HandlerService`. HTTP layer never needs `VerifyToken` through HandlerService since middleware has direct access — fine, but means HandlerService and gRPC surface diverge.
- `middleware.Service` (not a HandlerService) is exposed over gRPC for 6 RPCs (`CheckPermission`, `IsUser*`, `IsTeamPublic`) — HTTP equivalents are middleware-internal, so these are gRPC-only surface.

Net: **gRPC is not a strict subset of HTTP HandlerService** — it adds `taskQueueController` ops, middleware checks, and `auth.Service` token ops; it omits no HTTP endpoint of note but re-shapes streaming endpoints into polling.

## 8. Surprises

- Runtime service uses config prefix `runtime_worker.grpc.*` (lifecycle.go:39,43) while its internal client also accepts `runtime_worker.grpc.target` as fallback (runtimeclient/client.go:32). All other services use the plain `<name>.grpc.*` prefix. Inconsistent naming.
- `runtimeclient` only wraps 2 of the 6 runtime RPCs (`GetNamespaceLocks`, `GetQueuedTasks`). The typed `Get{Runtime,Queue,Limiter}Status` RPCs are registered on the server but have no internal Go client — they are reachable only via external tools / reflection. Dead-ish server-side surface unless consumed by another process or an HTTP handler we didn't visit.
- Every server uses `grpc.NewServer` with only `UnaryServerRequestIDInterceptor` (httpx) — no auth interceptor, no tracing/metrics interceptor, no TLS (`insecure.NewCredentials()` on every client). In-cluster trust assumption.
- `structpb.Struct` round-trip (`decodeBody`/`encodeStruct`) is repeated verbatim in all five service packages (iam/service.go:933-967, orchestrator/service.go:745-793, resource/service.go:458-501, runtime/service.go:216-231, system/service.go:171-214). Duplicated wiring — ripe for dedup into `httpx` or a shared `grpcutil` pkg.
- `mapRPCError` and `map*Error` are also duplicated five times (iam/service.go:914-931, orchestrator:826-843, resource:503-520, system:216-233; client side iamclient:1013-1036 etc.) — consistent sentinel mapping table repeated.
- Orchestrator's `CancelTask`/`RetryTask`/`ListDeadLetterTasks` live in the gRPC server file itself (service.go:83-234), not in `module/task`. The task module doesn't know about Redis dead-letter ops. Means a second process reaching orchestrator for task cancellation is fine, but anyone wiring `task.Service` directly in-process has no way to cancel.
- `interface/http/module.go` pulls in `middleware.NewService` and `router.New`; but in `gateway` mode `middleware.Service` is *decorated* to route through `iamclient` (app/gateway/options.go:63-67) — the HTTP module still binds a `middleware.NewService` provider which means an accidental loop is possible if ordering changes. The current fx.Decorate resolves it, but it's fragile.
- `httpapi.ServerConfig` has only `Addr` — no ReadTimeout/WriteTimeout/IdleTimeout/MaxHeaderBytes/TLSConfig. Gin engine is the only safeguard against slowloris. `NewServer` at server.go:17 builds `&http.Server{Addr, Handler}` with Go defaults (no timeouts). Production-unsafe as written.
