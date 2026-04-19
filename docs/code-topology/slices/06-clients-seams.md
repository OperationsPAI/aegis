# 06 — Clients, cross-cutting utils, and cross-repo seams

Source-of-truth: code only. All citations relative to `/home/ddq/AoyangSpace/aegis`.

---

## 1. Client inventory (`AegisLab/src/client/`)

Caller "packages" identified by grepping `client.<Symbol>` across `AegisLab/src` (per-package, not per-file).

| File | One-line purpose | Constructor / singleton | Exported API | Callers (Go packages) |
|---|---|---|---|---|
| `etcd_client.go` | etcd v3 KV + lease + watch wrapper | `GetEtcdClient()` singleton (`etcd_client.go:23`); `CloseEtcdClient()` | `EtcdPut`, `EtcdGet`, `EtcdDelete`, `EtcdWatch`, `EtcdGetWithRevision`, `EtcdWatchFromRevision` | `service/common` (config_registry, config_listener) |
| `harbor_client.go` | Harbor v2 image-tag lookup | `GetHarborClient()` singleton returning `*HarborClient` (`harbor_client.go:32`) | `(*HarborClient).GetLatestTag`, `.CheckImageExists` | `service/producer` (container, build paths via container ops) |
| `helm.go` | Helm v3 install/uninstall + repo mgmt + local-cache lookup | `NewHelmClient(namespace)` (`helm.go:34`) | `(*HelmClient).AddRepo`, `.UpdateRepo`, `.SearchRepo`, `.IsReleaseInstalled`, `.UninstallRelease`, `.InstallRelease`, `.Install` | `service/consumer` (restart_pedestal); also `helm_test.go` |
| `jaeger.go` | OTLP/HTTP trace exporter bootstrap (file is mis-named — sends to OTLP endpoint, not Jaeger native) | `InitTraceProvider()` (`jaeger.go:22`); `ShutdownTraceProvider(ctx)` (`jaeger.go:55`); package var `TraceProvider *sdktrace.TracerProvider` | same | `main.go:106,134,176` |
| `loki.go` | Loki HTTP `query_range` for historical job logs by `task_id` (LogQL `{app="rcabench"} \| task_id=…`) | `NewLokiClient()` (`loki.go:46`) | `(*LokiClient).QueryJobLogs(ctx, taskID, QueryOpts)`; type `QueryOpts` | `service/logreceiver` |
| `redis_client.go` | go-redis v9 wrapper + generic hash/stream/pubsub helpers | `GetRedisClient()` singleton (`redis_client.go:23`) | `CheckCachedField`, `GetHashField[T]`, `SetHashField[T]`, `GetRedisListRange`, `GetRedisZRangeByScoreWithScores`, `RedisXAdd`, `RedisXRead`, `RedisPublish` | nearly all `service/*` packages, `repository`, `handlers/system`, `client/debug` |
| `debug/status_registry.go` | In-memory typed debug-knob registry persisted to Redis stream `rcabench:debug:history` | `NewDebugRegistry()` (`debug/status_registry.go:62`) | `EntryType` + consts `EntryTypeReadOnly|ReadWrite`, `HistoryKey`, `DefaultHistoryLimit`; types `DebugEntry`, `HistoryEntry`, `DebugRegistry`; methods `Get`, `GetAll`, `GetHistory`, `Register`, `Set` | (registered, but no internal callers found in the slice — appears to be wired from `handlers/v2` debug surface only) |

Notes:
- Helm install path runs through `tracing.WithSpan` (`helm.go:262`) — only client that takes a tracing dependency.
- Singletons (`GetRedisClient`, `GetEtcdClient`, `GetHarborClient`) all use `sync.Once` and call `logrus.Fatalf` on connection failure — fail-fast.
- `LokiClient` is the only one not a singleton; constructed per-use.

---

## 2. `tracing/decorator.go`

File: `AegisLab/src/tracing/decorator.go` (57 lines, single tracer name `"rcabench/task"`).

Exported API:

| Symbol | Signature | Purpose |
|---|---|---|
| `WithSpan` | `func(ctx, f func(ctx) error) error` | Function-decorator; auto-derives span name from `runtime.Caller(1)` (`decorator.go:15`) |
| `WithSpanNamed` | `func(ctx, name string, f func(ctx) error) error` | Same, explicit name (`decorator.go:29`) |
| `WithSpanReturnValue[T]` | `func(ctx, f func(ctx) (T, error)) (T, error)` | Generic variant returning a value (`decorator.go:36`) |
| `SetSpanAttribute` | `func(ctx, key, value string)` | Sets attribute on the span found in ctx; no-op when not recording (`decorator.go:49`) |

Pattern of use: **function decorator** (callee wraps its own body), not gin/HTTP middleware. Callers typically:

```go
return tracing.WithSpan(ctx, func(ctx context.Context) error { … })
```

Used by: `service/consumer/{fault_injection,task,restart_pedestal,build_container,build_datapack,collect_result,distribute_tasks,algo_execution}.go`, `client/k8s/job.go`, `client/helm.go`.

---

## 3. utils package map (`AegisLab/src/utils/*.go`)

| File | Main exports | Notable non-trivial logic |
|---|---|---|
| `color.go` | `IsValidHexColor` | regex `^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$` |
| `docker.go` | `ParseFullImageRefernce` (sic) | Uses `github.com/distribution/reference` to split full image refs into registry/namespace/repo/tag (default tag `"latest"`) |
| `error.go` | `ErrorProcessor`, `NewErrorProcessor`, `(*ErrorProcessor).GetErrorByLevel` | Walks `errors.Unwrap` chain into a slice; supports negative indexing |
| `fault_translate.go` | `FieldDescription`, `BuildSystemIndexMap`, `BuildReverseTypeMap`, `ExtractFieldDescriptions`, `FaultSpecInput`, `TranslateFaultSpec` | Reflects on `chaos-experiment/handler.SpecMap` structs to extract `range`/`dynamic`/`description` struct tags into a name-indexed `chaos.Node` tree. Also the duration parser in `parseDurationToMinutes` (`fault_translate.go:204`) |
| `file.go` | `ExculdeRule` (sic), `AddToZip`, `CheckFileExists`, `CopyDir`, `CopyFile`, `CopyFileFromFileHeader`, `CalculateFileSHA256`, `GetAllSubDirectories`, `IsAllowedPath`, `LoadYAMLFile`, `MatchFile`, `ExtractZip`, `ExtractTarGz`, `ReadTomlFile` | Path-traversal guard in `IsAllowedPath` (`file.go:170`) restricts to `jfs.dataset_path`. Both extractors detect a single top-level directory and strip it (`file.go:207`, `:291`). Uses **`stretchr/testify/assert/yaml`** for YAML — surprising vendor path, see §8. |
| `github.go` | `IsValidGitHubLink`, `IsValidGitHubRepository`, `IsValidGitHubBranch`, `IsValidGitHubCommit`, `IsValidGitHubToken` | Full GitHub naming-rules validator: owner ≤39 chars, no leading/trailing `-`, no `--`; branches reject `..`, `@`; tokens checked by prefix table `ghp_/gho_/ghu_/ghs_/ghr_` and `ghp_` must be exactly 40 chars |
| `jwt.go` | `JWTSecret` (hard-coded), `TokenExpiration`, `RefreshTokenExpiration`, `ServiceTokenExpiration`; types `Claims`, `RefreshClaims`, `ServiceClaims`; `GenerateToken`, `GenerateRefreshToken`, `GenerateServiceToken`, `ValidateToken`, `ValidateTokenWithCustomClaims`, `ValidateServiceToken`, `GetUserIDFromToken`, `GetUsernameFromToken`, `ParseTokenWithoutValidation`, `ExtractTokenFromHeader` | HMAC-HS256 with `JWTSecret = "your-secret-key-change-this-in-production"` (`jwt.go:16`) — see §8. ServiceClaims used for K8s job-to-API auth, distinguished by issuer `"rcabench-service"` |
| `map.go` | `DeepMergeClone`, `GetPointerIntFromMap`, `MakeSet`, `MapToStruct[T]`, `MergeSimpleMaps[K,V]`, `StructToMap` | Recursive deep merge; `MapToStruct` round-trips through `json.Marshal/Unmarshal` to populate a generic struct from a `map[string]any` |
| `password.go` | `SaltLength`, `MinPasswordLength`, `MaxPasswordLength`, `PasswordStrength` enum + `String()`, `HashPassword`, `VerifyPassword`, `ValidatePasswordStrength`, `GenerateRandomPassword`, `IsPasswordValid` | **SHA-256(salt‖password)** — not bcrypt/argon2; constant-time compare in `constantTimeCompare` (`password.go:89`). Score-based strength heuristic |
| `pointer.go` | `BoolPtr`, `IntPtr`, `StringPtr`, `TimePtr`, `GetBoolValue`, `GetIntValue`, `GetStringValue`, `GetTimeValue`, `GetTimePtr` | Trivial |
| `runtime.go` | `GetCallerInfo(skip)` | Wraps `runtime.Caller` returning (file, line, funcName) |
| `slice.go` | `FilterEmptyStrings`, `SubtractArrays[T]`, `ToUniqueSlice[T]`, `Union[T]` | Generic set ops |
| `string.go` | `HelmKeyPart`, `ConvertSimpleTypeToString`, `ConvertStringToSimpleType`, `GenerateColorFromKey`, `GenerateULID`, `IsValidEnvVar`, `IsValidHelmValueKey`, `IsValidUUID`, `ParseHelmKey`, `ToSnakeCase`, `ToSingular` | `ConvertStringToSimpleType` preserves leading-zero strings as-is (`string.go:63`); `ParseHelmKey` parses `accounting.initContainers[0].image` into `[]HelmKeyPart`; `ToSingular` has irregular plurals table |
| `type.go` | `ConvertToType[T]`, `GetTypeName`, `Must` | Generic JSON-roundtrip type assertion |
| `validator.go` | `InitValidator`, `ValidateMinPtr`, `ValidateNonZeroIntSlice` | Registers two custom Gin/`go-playground/validator` rules on package init via explicit call from main |
| `version.go` | `ParseSemanticVersion` | `Sscanf("%d.%d.%d", …)` |

---

## 4. chaos-experiment integration surface

### a. Imports of chaos-experiment in AegisLab/src — by file

| File | Import path(s) |
|---|---|
| `main.go:41` | `client` (alias `chaosCli`) |
| `client/k8s/controller.go:11` | `client` (alias `chaosCli`) |
| `cmd/aegisctl/cmd/inject_guided.go:12` | `pkg/guidedcli` |
| `config/chaos_system.go:9` | `handler` (alias `chaos`) |
| `consts/consts.go:8` | `handler` |
| `database/entity.go:10`, `database/entity_helper.go:8`, `database/view.go:6` | `handler` |
| `dto/injection.go:14,15`, `dto/injection_test.go:8`, `dto/evaluation.go:9` | `handler` + `pkg/guidedcli` |
| `handlers/v2/injections.go:25` | `handler` |
| `service/analyzer/evaluation.go:12` | `handler` |
| `service/common/metadata_store.go:11` | `handler` |
| `service/consumer/fault_injection.go:19,20` | `handler` + `pkg/guidedcli` |
| `service/consumer/jvm_runtime_mutator.go:14` | `handler` |
| `service/consumer/restart_pedestal.go:18` | `handler` |
| `service/initialization/producer.go:19`, `service/initialization/systems.go:10` | `handler` |
| `service/producer/chaos_system.go:12` | `handler` |
| `service/producer/injection.go:25,26` | `handler` + `pkg/guidedcli` |
| `service/producer/spec_convert.go:14`, `service/producer/spec_convert_test.go:8` | `handler` |
| `service/producer/upload.go:18` | `handler` |
| `utils/fault_translate.go:10`, `utils/fault_translate_test.go:7` | `handler` |

The replace directive in `go.mod:63` points the `OperationsPAI/chaos-experiment` import at `../../chaos-experiment` (local checkout). The `LGU-SE-Internal/chaos-experiment` path is **not** referenced anywhere in `AegisLab/src` — only the OperationsPAI fork is used. `chaos-mesh/api` is also remapped to a fork (`go.mod:65`).

### b. Concrete symbols AegisLab actually calls

From `chaos-experiment/handler` (alias `chaos`):
- Types: `ChaosType`, `SystemType`, `Node`, `InjectionConf`, `SystemConfig`
- Maps/registries: `ChaosTypeMap`, `SpecMap`
- Functions: `GetChaosTypeName(ct)` (`utils/fault_translate.go:50`), `RegisterSystem(SystemConfig)` (`service/producer/chaos_system.go:71,128`, `service/initialization/systems.go:52`), `NodeToStruct[T](ctx, *Node)` (`service/consumer/fault_injection.go:174`)

From `chaos-experiment/client` (alias `chaosCli`):
- `InitWithConfig(k8sConfig)` (`main.go:67`)
- `GetCRDMapping()` (`client/k8s/controller.go:102,103`) — drives which GVRs the K8s controller watches

From `chaos-experiment/pkg/guidedcli`:
- Types `GuidedConfig`, `GuidedSession`
- `LoadConfig`, `SaveConfig`, `MergeConfig`, `Resolve`, `ApplyNextSelection` (used in `cmd/aegisctl/cmd/inject_guided.go`)
- `BuildInjection(ctx, cfg)` → `(InjectionConf, SystemType, error)` — the inject pipeline's runtime entry point (`service/producer/injection.go:1246`, `service/consumer/fault_injection.go:156`)

### c. Top-level packages inside `chaos-experiment/` (1 level deep)

`chaos`, `client`, `cmd`, `controllers`, `docs`, `handler`, `internal`, `pkg`, `tools`, `utils` (from `ls`).

**Used by AegisLab**: `client`, `handler`, `pkg/guidedcli`. Not referenced by AegisLab: `chaos`, `cmd`, `controllers`, `internal`, `tools`, `utils`, `docs`.

---

## 5. Frontend → backend `/api/v2/*` endpoints actually referenced

The frontend has `BASE_PATH = '/api/v2'` (`AegisLab-frontend/src/api/client.ts:4`). `apiClient` is a hand-rolled axios instance at that base; the SDK from `@rcabench/client` is wired in `src/api/sdk.ts` and routes through the same axios instance. Plus a few hard-coded `/api/v2/...` literals for SSE/WS streams.

Deduped endpoint paths the frontend code references (suffix appended to `/api/v2`):

```
/auth/refresh                    (literal in client.ts + tests)
/auth/login, /auth/logout        (via SDK AuthenticationApi in api/auth.ts)
/auth/profile, /auth/change-password, /auth/register (SDK)
/permissions, /permissions/{id}
/groups/{id}/stats
/groups/{id}/stream                          (SSE, literal)
/executions, /executions/{id}/labels, /executions/labels, /executions/batch-delete
/datasets, /datasets/{id}, /datasets/{id}/labels
/containers, /containers/{id}, /containers/{id}/labels, /containers/build
/teams, /teams/{id}, /teams/{id}/members, /teams/{id}/members/{userId}, /teams/{id}/projects
/metrics/injections, /metrics/executions, /metrics/algorithms
/tasks, /tasks/{id}, /tasks/batch-delete
/tasks/{id}/logs/ws                          (WebSocket, literal)
/injections, /injections/{id}, /injections/search, /injections/metadata
/injections/{id}/clone, /injections/{id}/labels (PATCH inferred from injections.ts), /injections/{id}/files
/injections/{id}/files/query, /injections/upload, /injections/batch-delete
/roles, /roles/{id}, /roles/{id}/users
/labels, /labels/{id}, /labels/batch-delete
/system/metrics, /system/metrics/history
/evaluations/{id}, /evaluations/datasets, /evaluations/datapacks
/projects/{id}/labels                        (other /projects/* go through SDK)
/resources, /resources/{id}, /resources/{id}/permissions
/users, /users/{id}/detail, /users, /users/{id}, /users/{userId}/roles/{roleId}
/traces, /traces/{id}
/traces/{id}/stream                          (SSE, literal)
```

(SDK-mediated endpoints not enumerated above are present in `@rcabench/client` and used via `…Api(sdkConfig, '', sdkAxios)` in `src/api/{auth,projects,…}.ts`. The hand-rolled `apiClient.<method>('/<path>')` calls above are the explicit additions.)

---

## 6. rcabench-platform ↔ AegisLab data contract

AegisLab side (writes the datapack files):
- `service/producer/upload.go:24-28` — explicit allowlist of expected file names: `abnormal_traces.parquet`, `abnormal_metrics.parquet`, `normal_traces.parquet`, `normal_metrics.parquet`
- `service/producer/upload.go:226` — writes `injection.json` into the datapack dir

rcabench-platform side (reads them) — evidence points:

| File path in datapack/dataset | rcabench-platform read site |
|---|---|
| `injection.json` | `cli/dataset_transform/make_rcabench.py:214`, `cli/prepare_inputs.py:368,438,446`, `cli/dataset_analysis/detector_cli.py:98,111`, `notebooks/sdg.py:135` |
| `env.json` | `cli/dataset_transform/make_rcabench.py:213`, `cli/prepare_inputs.py:364` |
| `normal_traces.parquet` / `abnormal_traces.parquet` | `cli/dataset_analysis/scan_datasets.py:43,44`, `cli/dataset_analysis/check_traces.py:86`, `cli/detector.py:773-786,821,822`, `cli/dataset_analysis/detector_cli.py:172,205-208` |
| `normal_metrics.parquet` / `abnormal_metrics.parquet` (+ `_sum`, `_histogram` variants) | `cli/dataset_analysis/scan_datasets.py:46,47,124-129` |
| `normal_logs.parquet` / `abnormal_logs.parquet` | `cli/dataset_analysis/scan_datasets.py:49,50` |
| `traces.parquet`, `simple_metrics.parquet`, `metric.parquet`, `log.parquet`, `metrics.parquet`, `logs.parquet` | `cli/dataset_analysis/scan_datasets.py:40,41,183`, `cli/dataset_transform/make_eadro.py:272,317,338`, `cli/dataset_transform/make_aiops21.py:238-269` |
| `conclusion.parquet` | `cli/dataset_transform/make_rcabench.py:260,281,294`, `notebooks/sdg.py:164` |
| Dataset-level meta files: `attributes.parquet`, `rows_count.parquet`, `metric_names.parquet`, `normal_traces_duration.parquet`, `fault_types.perf.parquet`, `compare.parquet`, `<algo>.ranks.parquet`, `<algo>.ranks.summary.parquet` | `cli/dataset_analysis/scan_datasets.py:30,114,168,171; scan_outputs.py:30,42,56,63,75; check_traces.py:71`; `cli/dataset_transform/make_rcabench.py:203` |

Generic parquet roundtrip: `src/rcabench_platform/v2/utils/serde.py:74` asserts `.parquet` extension only.

**Contract (stable):** AegisLab writes `injection.json` + the four `*_traces.parquet`/`*_metrics.parquet` files into a per-datapack directory. rcabench-platform CLIs walk that directory and read those exact filenames. `env.json` is also expected (written by upstream prepare_inputs path, read by `make_rcabench.py`).

---

## 7. `go.mod` — notable direct deps

All from the `require (…)` block at top of `AegisLab/src/go.mod`:

| Concern | Module | Version | Line |
|---|---|---|---|
| HTTP router | `github.com/gin-gonic/gin` | v1.10.0 | go.mod:18 |
| Gin SSE / CORS | `github.com/gin-contrib/{sse,cors}` | sse v1.0.0, cors v1.7.3 | :17, :16 |
| ORM | `gorm.io/gorm` | v1.25.12 | :52 |
| ORM drivers | `gorm.io/driver/{mysql,sqlite}` | mysql v1.5.7, sqlite v1.5.0 | :50, :51 |
| ORM tracing plugin | `gorm.io/plugin/opentelemetry` | v0.1.13 | :53 |
| Config | `github.com/spf13/viper` | v1.19.0 | :35 |
| CLI | `github.com/spf13/cobra` | v1.10.1 | :34 |
| Redis | `github.com/redis/go-redis/v9` | v9.7.3 | :30 |
| etcd | `go.etcd.io/etcd/client/v3` | v3.6.7 | :40 |
| chaos-experiment | `github.com/OperationsPAI/chaos-experiment` | v0.1.0 (replaced → `../../chaos-experiment`) | :9, :63 |
| chaos-mesh API | `github.com/chaos-mesh/chaos-mesh/api` | (replaced → `OperationsPAI/chaos-mesh/api ...20260124102507-517f3df45e54`) | :65 |
| OpenTelemetry SDK + OTLP HTTP | `go.opentelemetry.io/otel`, `…/sdk`, `…/trace`, `…/exporters/otlp/otlptrace/otlptracehttp`, `…/proto/otlp` | otel v1.37.0, otlptracehttp v1.35.0, proto/otlp v1.5.0 | :41-45 |
| DuckDB (Arrow build tag) | `github.com/duckdb/duckdb-go/v2` | v2.5.0 | :15 |
| Apache Arrow | `github.com/apache/arrow-go/v18` | v18.4.1 | :12 |
| controller-runtime | `sigs.k8s.io/controller-runtime` | v0.21.0 | :59 |
| K8s API/client-go | `k8s.io/{api,apimachinery,client-go,cli-runtime}` | api v0.33.1, apimachinery v0.33.1, client-go v0.33.1, cli-runtime v0.32.2 | :55-58 |
| Helm | `helm.sh/helm/v3` | v3.17.3 | :54 |
| Harbor SDK | `github.com/goharbor/go-client` | v0.213.1 | :21 |
| Jaeger | (no dedicated jaeger module — `client/jaeger.go` actually uses OTLP HTTP exporter; file name is misleading) | — | — |
| Loki | (no SDK — `client/loki.go` uses `net/http` directly) | — | — |
| BuildKit | `github.com/moby/buildkit` | v0.18.2 | :27 |
| Docker CLI | `github.com/docker/cli` | v27.4.1+incompatible | :14 |
| JWT | `github.com/golang-jwt/jwt/v5` | v5.2.3 | :22 |
| Validator | `github.com/go-playground/validator/v10` | v10.24.0 | :20 |
| Logging | `github.com/sirupsen/logrus` | v1.9.3 | :33 |
| WebSocket | `github.com/gorilla/websocket` | v1.5.4-0.20250319132907-e064f32e3674 | :24 |
| In-mem Redis (test) | `github.com/alicebob/miniredis/v2` | v2.37.0 | :10 |
| Swagger | `github.com/swaggo/{files,gin-swagger}` | files v1.0.1, gin-swagger v1.6.0 | :37, :38 |
| Cron | `github.com/robfig/cron/v3` | v3.0.1 | :31 |
| ULID | `github.com/oklog/ulid` | v1.3.1 | :28 |
| Prometheus client | `github.com/prometheus/client_golang` | v1.22.0 | :29 |

Replace directives of note (`go.mod:63-65, :317-323`): `OperationsPAI/chaos-experiment` → local `../../chaos-experiment`; `chaos-mesh/api` → `OperationsPAI/chaos-mesh/api` fork; OTel log packages pinned to old versions to avoid the breaking SDK split.

---

## 8. Surprises / dead code

1. **`client/jaeger.go` does not talk to Jaeger** — it builds an OTLP/HTTP exporter (`otlptracehttp.New`) and reads `jaeger.endpoint` from config (`jaeger.go:25-28`). The file name and config key are legacy.
2. **Hard-coded JWT secret in source** — `JWTSecret = "your-secret-key-change-this-in-production"` (`utils/jwt.go:16`), used by every token generator. There is no env-var fallback in this file.
3. **Password hashing is plain SHA-256(salt‖password)**, not bcrypt/argon2 (`utils/password.go:48-51`). The comment says "constant time compare" but the salt scheme is still vulnerable to fast GPU brute force.
4. **YAML import via `stretchr/testify/assert/yaml`** (`utils/file.go:20`) — production code using a testify internal subpath, while `sigs.k8s.io/yaml` is also a direct dep used in `client/helm.go`. Inconsistent and unusual.
5. **Typo in exported name**: `ParseFullImageRefernce` (`utils/docker.go:10`) and `ExculdeRule` (`utils/file.go:23`) — both exported, both misspelled.
6. **`Hybrid chaos.ChaosType = -1`** sentinel (`consts/consts.go:38`) — an out-of-band ChaosType used to signal batch/hybrid injections; not a value defined by the chaos-experiment library.
7. **`client/debug/status_registry.go` registers exactly one entry** (`debug_mode`, registered in `registerEntries()` at line 219) and grep finds no other call sites in the slice — suggests an extension point that is mostly empty.
8. **`tracing.WithSpan` only inside one client (`helm.go:262`)** — every other client (etcd/redis/loki/harbor/jaeger) skips tracing entirely. Loki even logs each query with `logrus.Infof` instead.
9. **Two parallel inject pipelines coexist**: legacy Friendly→`chaos.Node` (`utils/fault_translate.go`, `service/producer/spec_convert.go`) and the new `pkg/guidedcli` flow (`dto/injection.go:395-436`, `service/consumer/fault_injection.go:34,156`). Code paths in `service/consumer/fault_injection.go:326-341` branch on which one is present in the payload.
10. **`replace` for `chaos-mesh/api`** points at a fork pinned to a 2026-01 commit (`go.mod:65`) — depends on an unreleased API. This is an upstream-fork lock, not a hot-fix you can drop without coordination.

