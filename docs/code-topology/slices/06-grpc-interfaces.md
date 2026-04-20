# 06 · Remaining gRPC interfaces (Phase 6)

Source: `/home/ddq/AoyangSpace/aegis/AegisLab/src`

This slice replaces the old inventory of IAM/resource/orchestrator/system gRPC services.
After the phase-2 collapse there is only one internal proto package left: `proto/runtime/v1`.

## 1. What remains

Remaining internal gRPC artifacts:

- `proto/runtime/v1/runtime.proto`
- `interface/grpc/runtime/*`
- `interface/grpc/runtimeintake/*`
- `internalclient/runtimeclient/*`

Removed internal gRPC families:

- `interface/grpc/iam`
- `interface/grpc/orchestrator`
- `interface/grpc/resource`
- `interface/grpc/system`
- `internalclient/{iam,orchestrator,resource,system}client`
- `proto/{iam,orchestrator,resource,system}`

## 2. `RuntimeService` (served by runtime-worker)

Proto: `proto/runtime/v1/runtime.proto`

Purpose: query runtime-worker state.

RPCs:

- `Ping(PingRequest) -> PingResponse`
- `GetRuntimeStatus(RuntimeStatusRequest) -> RuntimeStatusResponse`
- `GetQueueStatus(QueueStatusRequest) -> QueueStatusResponse`
- `GetLimiterStatus(LimiterStatusRequest) -> LimiterStatusResponse`
- `GetNamespaceLocks(PingRequest) -> StructResponse`
- `GetQueuedTasks(PingRequest) -> StructResponse`

Server implementation:

- module: `interface/grpc/runtime/module.go`
- service: `interface/grpc/runtime/service.go`
- lifecycle: `interface/grpc/runtime/lifecycle.go`

Wiring summary:

- `newRuntimeServer(...)` builds a `consumer.RuntimeSnapshotService` from DB, Redis, K8s,
  BuildKit, Helm, and the three named rate limiters.
- `GetNamespaceLocks` and `GetQueuedTasks` read directly from Redis-backed helpers.
- lifecycle config key: `runtime_worker.grpc.addr`
- default listen address: `:9094`

## 3. `RuntimeIntakeService` (served by api-gateway)

Proto: `proto/runtime/v1/runtime.proto`

Purpose: accept worker-produced execution/injection state writes back into the API-owned
module graph.

RPCs:

- `CreateExecution(StructResponse) -> StructResponse`
- `GetExecution(StructResponse) -> StructResponse`
- `UpdateExecutionState(StructResponse) -> StructResponse`
- `CreateInjection(StructResponse) -> StructResponse`
- `UpdateInjectionState(StructResponse) -> StructResponse`
- `UpdateInjectionTimestamps(StructResponse) -> StructResponse`

Server implementation:

- module: `interface/grpc/runtimeintake/module.go`
- service: `interface/grpc/runtimeintake/service.go`
- lifecycle: `interface/grpc/runtimeintake/lifecycle.go`

Wiring summary:

- `newIntakeServer(...)` depends on local `*execution.Service` and `*injection.Service`.
- The server is deliberately thin: decode JSON-over-`Struct`, call the local owner service,
  encode the response back to `StructResponse`.
- lifecycle config keys: `api_gateway.intake.grpc.addr`, legacy `runtime_intake.grpc.addr`
- default listen address: `:9096`

## 4. The shared client: `internalclient/runtimeclient.Client`

`runtimeclient.Client` now owns both directions.

### Query direction

Configuration:

- primary: `clients.runtime.target`
- legacy: `runtime_worker.grpc.target`

State helpers:

- `Enabled()` reports whether the query channel is configured.
- `GetNamespaceLocks(...)`
- `GetQueuedTasks(...)`

### Intake direction

Configuration:

- primary: `clients.runtime_intake.target`
- legacy: `runtime_intake.grpc.target`

State helpers:

- `IntakeEnabled()` reports whether the intake channel is configured.
- `CreateExecution(...)`
- `GetExecution(...)`
- `UpdateExecutionState(...)`
- `CreateInjection(...)`
- `UpdateInjectionState(...)`
- `UpdateInjectionTimestamps(...)`

The client opens up to two `grpc.ClientConn`s in one object and closes both on `fx` stop.

## 5. How the dedicated deployment uses the seam

### `runtime-worker-service`

`runtimeapp.Options(...)` wires:

- `app.ExecutionInjectionOwnerModules()`
- `app.RuntimeWorkerStackOptions()`
- `consumer.RemoteOwnerOptions()`
- `app.RequireConfiguredTargets(api-gateway-intake)`

`consumer.RemoteOwnerOptions()` is the remaining `fx.Decorate(...)` use in the codebase.
It swaps the local `ExecutionOwner` / `InjectionOwner` with runtime-intake-backed wrappers in
`service/consumer/owner_adapter.go`.

### `api-gateway`

`gateway.Options(...)` wires:

- `app.ProducerHTTPOptions(port)`
- `grpcruntimeintake.Module`

The API process serves the intake endpoint, but it does not currently wire a runtime query
client into its app graph. The query side is still available in proto/client form for future
ops tooling or later API exposure.

### Collocated modes

`producer`, `consumer`, and `both` do not force the remote owner path.

- `consumer` and `both` use `consumer.NewExecutionOwner` / `consumer.NewInjectionOwner`
  against local `execution.Service` and `injection.Service`.
- `runtimeclient.Client` can still be present in the graph, but without an intake target those
  owners stay local.

## 6. Protocol notes

- Both services use plain unary gRPC with the request-id interceptor only.
- No TLS is configured in the server/client lifecycle helpers.
- `RuntimeIntakeService` intentionally tunnels DTO payloads through
  `google.protobuf.Struct` so execution/injection DTO changes do not require proto churn.
- There is no server-streaming API left in the internal gRPC layer.

## 7. Validation coverage

Relevant tests:

- `app/service_entrypoints_test.go` - validates and boots both dedicated service graphs
- `app/startup_smoke_test.go` - validates runtime-worker gRPC startup in collocated modes
- `app/remote_require_test.go` - validates required target configuration helpers
- `interface/grpc/runtime/service_test.go` - covers the runtime status server
