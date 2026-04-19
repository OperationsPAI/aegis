# AegisLab Code-Topology — Orphans, Bugs, and Risks

Findings from the 6-slice recovery pass. Each item cites `file.go:line` so it can be re-checked against live code.
Divided by severity / kind so the list is actionable.

## A. Security red flags

| # | File:line | Risk |
|---|---|---|
| A1 | `utils/jwt.go:16` | **Hard-coded JWT secret** `"your-secret-key-change-this-in-production"`. No env-var fallback. Every token generator uses this constant. |
| A2 | `utils/password.go:48-51` | **Password hashing is plain SHA-256(salt‖password)** — not bcrypt/argon2. Constant-time compare is used but GPU brute force is feasible. |
| A3 | `middleware/ratelimit.go:148` | **`UserBasedRateLimit` bucket-key generator is broken**: `"user_" + string(rune(userID))` collapses user IDs into single Unicode replacement chars (any ID outside the basic range shares one bucket). Should be `strconv.Itoa`. |
| A4 | `middleware/permission.go:177` | `anyPermission` swallows DB errors with `logrus.Warnf("Permission check error: %f", err)` (`%f` for an error value) and `continue`s — real permission-check DB failures are masked into silent allow paths. |
| A5 | `middleware/permission.go:39-41, 102-112` | Two service-token bypass paths in `extractPermissionContext` and `withPermissionCheck`. Redundant guards; drift between the two could turn a bypass into a hard auth failure for K8s job callers. |
| A6 | `handlers/v2/tasks.go:188` | **WS endpoint authenticates via `?token=` query param**; no WS middleware. Token appears in URLs / access logs. Unavoidable for WS but worth noting. |
| A7 | `repository/token.go:84` | JWT blacklist uses `SCAN blacklist:token:*`. Cost scales with blacklist size on every login/refresh path. |

## B. Workflow correctness bugs

| # | File:line | Bug |
|---|---|---|
| B1 | `service/producer/project.go:237` | **`ManageProjectLabels` removal branches are inverted.** Calls `ClearProjectLabels` only when `len(labelIDs)==0`; the non-empty branch is unreachable. `ManageContainerLabels` (container.go:248) and `ManageInjectionLabels` (injection.go:578) do it correctly — copy-paste regression. |
| B2 | `service/producer/execution.go:263` | Same inversion as B1 — `ManageExecutionLabels` cannot actually remove labels. |
| B3 | `service/producer/container.go:828-832` | `processGitHubSource` branch for empty `req.GithubBranch` goes to the full-clone path, while the non-empty branch uses `--single-branch`. The original intent (comment) is inverted. |
| B4 | `service/consumer/task.go:331` | `executeTaskWithRetry` creates per-attempt `context.WithCancel` and discards `cancel` with `_ = cancel` — leaks cancel func per retry attempt. |
| B5 | `service/consumer/task.go:441, 445` | `RemoveFromZSet` error log prints `err` from outer scope (from `GetTaskQueue`) which may be nil when the inner `RemoveFromZSet` failed — wrong-shadowed error reporting. |
| B6 | `repository/injection.go:297` | **`ListInjectionsWithIssues` queries the wrong model** — passes `&FaultInjectionNoIssues{}` to `db.Model` while returning `[]FaultInjectionWithIssues`. Only "works" by accident of identical column shape. |
| B7 | `service/consumer/restart_pedestal.go:346-353` | `installPedestal` retries `helmClient.Install` from `LocalPath` after remote failure, but still passes `item.Version` (a remote-only chart version) to the local-path install. Silently inert. |
| B8 | `service/consumer/fault_injection.go:42` | `batchManager` state is in-memory only (no persistence). Restart during an in-flight hybrid batch leaves the batch permanently pending — `BuildDatapack` never submits. |
| B9 | `service/consumer/task.go:42` + `repository/task.go:29` | `LastBatchInfoKey = "last_batch_info"` declared twice, never read or written anywhere. Likely leftover from a dropped feature. |

## C. Stale / invalid constants and whitelists

| # | File:line | Problem |
|---|---|---|
| C1 | `consts/validation.go:85` | `ValidConfigScopes` rejects `ConfigScope.Global` even though Global is declared and populated in `ConfigScopeMap`. Any POST carrying `scope=global` fails validation. |
| C2 | `consts/validation.go:90-96` | `ValidDatapackStates` omits `DatapackDetectorFailed` / `DatapackDetectorSuccess` — states that the consumer pipeline actually writes. Validation rejects values the system produces. |
| C3 | `consts/validation.go:203-210` | `ValidTaskTypes` omits `TaskTypeCronJob`. Cron-scheduled tasks fail validation. |
| C4 | `service/consumer/distribute_tasks.go:48` (default branch) | `TaskTypeCronJob` has no `case` in `dispatchTask` — falls through to `unknown task type`. Cron is handled only in the scheduler reschedule path. |
| C5 | `database/view.go:65` | **Hard-coded `algorithm_id = 1`** in the detector-results JOIN used by `fault_injection_{no_issues,with_issues}` views. Magic number, no comment. If the detector container_version id differs, the analysis views show no data. |
| C6 | `middleware/audit.go:135` vs `handlers/system/monitor.go:121` | Audit excludes path `/system/monitor/namespace-locks`, but actual route is `/system/monitor/namespaces/locks`. Audit still captures namespace-lock GETs because the skip string never matches. |
| C7 | `repository/injection.go:255, 297` | `database.Sort("dataset_id desc")` applied to `fault_injection_no_issues/with_issues` views which expose `datapack_id`, not `dataset_id`. Either a rename leftover or a bug. |
| C8 | `repository/granularity.go:32` | Reuses `containerVersionOmitFields = "active_version_key,HelmConfig,EnvVars"` on `GranularityResult` rows, which have none of those fields. Copy-paste. |
| C9 | `repository/project.go:189` | References `projectOmitFields` which is not defined inside the audited slice (may live in a sibling file or be a dangling reference). |
| C10 | `service/producer/dynamic_config.go:52` vs `service/common/config_listener.go:21` | Two different `etcdPrefixForScope` / `scopePrefix` maps for the same information. Two sources of truth; drift risk. |

## D. Dead code

| # | Location | What |
|---|---|---|
| D1 | `service/consumer/jvm_runtime_mutator.go` | **Entire file is one big `/* … */` block comment** (~100 lines). Builds clean but contributes nothing. Refers to a removed `Consumer`/`task.Status` model. |
| D2 | `router/v2.go:582-583` | `analyzer := v2.Group("/analyzer", JWTAuth()); _ = analyzer` — declared then discarded. Wastes one middleware allocation per startup. |
| D3 | `database/scope.go` | `KeywordSearch`, `CursorPaginate`, `Paginate` have zero call sites in the audited slice. Only `Sort` is used (and misused, see C7). |
| D4 | `service/common/injection.go:13` | `ProduceFaultInjectionTasks` has no caller in the producer slice. Intended to be used by consumer (CRD callback); wrapper exists but the grep found no consumer call either. Potentially dead. |
| D5 | `service/producer/user.go:206` | `SearchUsers` is a stub returning `(nil, nil)`. |
| D6 | `service/consumer/rate_limiter.go:31` vs `service/producer/rate_limiter.go:28` | Duplicate `isTerminalState` / `isTaskTerminal` with identical semantics (`state ∈ {Completed, Error, Cancelled}`). |
| D7 | `handlers/v2/docs.go:38` | `SwaggerModelsDoc` handler has empty body — pure swagger anchor. |
| D8 | `handlers/debug.go` | `GetAllVars`, `GetVar`, `SetVar` have no `@Router` annotation — mounted somewhere outside the handler slice (debug-only route group). Unclear if reachable in production. |
| D9 | `handlers/system/monitor.go:31` | `GetMetrics` is `@Deprecated`, returns hardcoded data, sets `Deprecation: true` header. Successor is `v2.GetSystemMetrics`. |
| D10 | `consts/consts.go:359` (`NotificationStreamKey="notifications:global"`) | Stream name declared; no writer found in the audited slice. Only consumer side. |
| D11 | `service/consumer/task.go:36-44` vs `repository/task.go:23-31` | Redis queue key constants duplicated; only the `repository/` ones are read. Consumer copies are dead. |
| D12 | `consts/consts.go:243` (`TaskCancelled=-2`) | No code transitions a task to `TaskCancelled`. `CancelTask` deletes the task from queues and aborts context but doesn't write the state. |
| D13 | `repository/execution.go` / `database/entity.go` | Implicit GORM `project_containers` and `project_datasets` M2M tables migrate but no repo function references either — cross-slice code may handle them, or they are orphans. |
| D14 | Several entities without repo queries | `Resource.Parent` traversal, `UserPermission` direct-grant listing, `ConfigLabel` join table, `Evaluation` listing by project — all declared & migrated but have no repository accessor. |
| D15 | `repository/injection.go:97` | `ListExistingEngineConfigs` does string comparison on `engine_config` (a `longtext` column) without an index. Hot path for inject dedup; will degrade with scale. |

## E. Surprises / naming drift

| # | File | Notes |
|---|---|---|
| E1 | `client/jaeger.go` | Does **not** talk to Jaeger — builds an OTLP/HTTP exporter (`otlptracehttp.New`). The file name and the `jaeger.endpoint` config key are legacy. |
| E2 | `utils/docker.go:10` | Exported `ParseFullImageRefernce` (spelling error, visible in API). |
| E3 | `utils/file.go:23` | Exported `ExculdeRule` (spelling error, visible in API). |
| E4 | `service/producer/execution.go:291`, surfaced in `handlers/v2/executions.go:251` | Exported `ProduceAlgorithmExeuctionTasks` (spelling error). |
| E5 | `utils/file.go:20` | Production code uses `stretchr/testify/assert/yaml` for YAML — testify internal subpath in a non-test file. `sigs.k8s.io/yaml` is also a direct dep (`client/helm.go`). Inconsistent. |
| E6 | `service/producer/system.go:269` | **Package `init()` spawns a 1-minute metric-collection goroutine on import.** Side-effecting init — hard to find, silent failures (`runtime.Gosched()` on error). |
| E7 | `handlers/v2/pedestalhelm/` | A separate subpackage carved out of `handlers/v2` to work around a build break. The doc comment at `verify.go:1-7` says explicitly "intentionally separated… which currently has an unrelated compile break in injections.go" — implies `injections.go` is broken in at least one build config. |
| E8 | `consts.Hybrid chaos.ChaosType = -1` | Sentinel declared in `consts/consts.go:38`, not a value defined by the chaos-experiment library. Signals hybrid/batch injections. |
| E9 | `service/consumer/rate_limiter.go:195-204` | Bucket caps 2/3/5 are defaults — overridable via dynamic-config key `MaxTokens*` under `rate_limiting` category. |
| E10 | `go.mod:65` | `chaos-mesh/api` replace points to a fork pinned to a 2026-01-24 commit. Upstream-fork lock. |
| E11 | `cmd/aegisctl/cmd/inject_guided.go` | `inject guided` runs purely local YAML I/O unless `--apply` is passed. Without `--apply` there is zero backend traffic. With `--apply`, posts to the same endpoint as `inject submit`; backend dispatches on spec shape. |
| E12 | `cmd/aegisctl/cmd/cluster.go` | `aegisctl cluster preflight` connects directly to k8s / MySQL / Redis / etcd / ClickHouse. **Does not go through the AegisLab HTTP API.** |
| E13 | `cmd/aegisctl/cmd/container.go:300-322` | `fetchContainerVersionByID` is O(N×M): scans all containers + versions over HTTP. Usable for small registries; scales poorly. |
| E14 | `cmd/aegisctl/cmd/task.go:257-275` | `task logs --follow=false` waits exactly 5 s then exits — race with slow-starting WS stream. |
| E15 | `main.go:82` | `--conf` advertised as a file path but `viper.AddConfigPath` expects a directory. Silently falls through on file-path values. |
| E16 | `router/v2.go:494 & v2.go:248` | `SearchInjections` handler registered at two routes: `POST /api/v2/injections/search` (system-admin gate) and `POST /api/v2/projects/:pid/injections/search` (project-member gate). Auth check logic must also live inside the handler, or the system-admin route is the only admin gate. |
| E17 | `handlers/v2/pedestal_helm.go` (whole file), `handlers/v2/tasks.go:208`, `handlers/system/health.go:108,122` | Handler layer leaks: bypasses the service layer and reaches directly into `repository` / `database`. The whole pedestal helm CRUD lacks a service abstraction. |
| E18 | `service/consumer/k8s_handler.go` task-type switch at 529-594 and 596-667 | The "next task" submission for BuildDatapack→RunAlgorithm and RunAlgorithm→CollectResult is the only place that knows the workflow order. Changing the pipeline requires editing this file. |

## F. Things that were correct in the old docs but worth restating

- `GetInjectionMetadata` (`handlers/v2/injections.go:130`) and `TranslateFaultSpecs` (`handlers/v2/injections.go:677`) **do return HTTP 410** despite the routes still being registered in `router/v2.go` (the memory note was right on behaviour). Guided-CLI migration is the replacement — `producer.FriendlySpecToNode` + `guidedcli.BuildInjection`.
- `LGU-SE-Internal/chaos-experiment` is mentioned in old CLAUDE.md but **not imported** anywhere in `AegisLab/src`. Only the OperationsPAI fork is used (`go.mod:63` replaces it with local `../../chaos-experiment`).

## G. Areas where recovery couldn't verify cleanly

1. `consts.NotificationStreamKey` has no writer in the audited slice. Either writer is cross-repo (unlikely) or the notifications stream is currently dormant.
2. `common.ProduceFaultInjectionTasks` caller — reachable via `restart_pedestal.go:179` in consumer, but the producer-side wrapper in `common/injection.go:28` has no direct call. Verify whether the wrapper is the canonical path or dead.
3. `projectOmitFields` (`repository/project.go:189`) — not defined in the audited files. Either elsewhere in the package or a dangling identifier; a `go build` will clarify.
4. `DebugRegistry` in `client/debug/` registers exactly one knob (`debug_mode`) and `debug.go` handlers have no `@Router`. Unclear if/where the debug router is actually mounted.

These 4 items are the right targets for the *next* verification loop — pick one, follow the code, update this doc.
