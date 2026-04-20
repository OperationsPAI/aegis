# Fault Injection Pipeline - Design (2026-04-20)

This document tracks the current guided fault-injection submit path in the post-phase-6 codebase.

It supersedes the retired `chaoscli.Spec` end-to-end design. The `chaoscli` package is gone; `pkg/guidedcli` from `github.com/OperationsPAI/chaos-experiment` is the canonical guided config model.

## Wire format

`POST /api/v2/projects/{id}/injections/inject` accepts `SubmitInjectionReq` from `src/module/injection/api_types.go`.

Important fields:

- `pedestal`, `benchmark`, `interval`, `pre_duration`, `algorithms`
- `Specs [][]json.RawMessage` - outer slice is batches, inner slice is homogeneous spec payloads

Each raw-message element is interpreted as one of three shapes:

| Shape | Identified by | Status |
| --- | --- | --- |
| guided config | top-level `chaos_type` field | preferred |
| Node DSL | legacy chaos-experiment node tree | legacy |
| `FriendlyFaultSpec` | top-level `type` field | legacy |

`SubmitInjectionReq.ResolveSpecs(...)` performs this dispatch in `src/module/injection/api_types.go`.

## End-to-end flow

```text
HTTP POST /api/v2/projects/{id}/injections/inject
    |
    v
module/injection.Handler.SubmitProjectFaultInjection
    |
    v
SubmitInjectionReq.ResolveSpecs
    |                         |
    | guided                  | legacy (Node DSL or FriendlyFaultSpec)
    v                         v
ResolvedGuidedConfigs        ResolvedSpecs
    |                         |
    +-----------+-------------+
                v
        RestartPedestal task
                |
                v
        FaultInjection subtask
                |
      parseInjectionPayload(...)
    |                         |
    | guided_configs          | node payload
    v                         v
guidedcli.BuildInjection     chaos.NodeToStruct[handler.InjectionConf]
    |                         |
    +-----------+-------------+
                v
        handler.BatchCreate
                |
                v
        Chaos Mesh CRDs
```

## Current file map

- `src/module/injection/api_types.go` - `SubmitInjectionReq`, `FriendlyFaultSpec`, `ResolveSpecs`, guided/legacy dispatch
- `src/module/injection/spec_convert.go` - legacy friendly-spec conversion helpers
- `src/module/injection/handler.go` - HTTP submit handlers and the current `GET /api/v2/injections/systems` system-mapping endpoint
- `src/module/injection/routes.go` - SDK and portal route registration
- `src/service/consumer/fault_injection.go` - payload parsing and runtime injection execution
- `src/cmd/aegisctl/cmd/inject_guided.go` - `aegisctl inject guided`
- `src/cmd/aegisctl/cmd/inject.go` - CLI submit path that wraps guided configs into `SubmitInjectionReq`
- `src/consts/task.go` and `src/dto/task.go` - task payload keys and task envelope fields

## Behavior notes

1. `chaos_type` is the guided-dispatch key. Do not reuse that field name for unrelated payload shapes.
2. Each inner batch must stay homogeneous. Guided and legacy payloads cannot be mixed inside the same request batch.
3. Legacy Node DSL and `FriendlyFaultSpec` still compile and still run for compatibility, but new work should use the guided path.
4. The guided path does not round-trip through the legacy node tree. `guidedcli.BuildInjection(...)` resolves directly to live injection config at consumer time.
5. The current route surface does not expose the older `/translate` helper. Treat it as retired.

## Migration status

- [x] guided config path exported from `chaos-experiment/pkg/guidedcli`
- [x] `aegisctl inject guided` submits guided configs through the same backend API
- [x] backend submit path supports guided + legacy compatibility dispatch
- [x] consumer path builds live injection configs from guided input
- [ ] frontend migration can continue reducing dependence on legacy payload helpers
- [ ] legacy `FriendlyFaultSpec` helpers can be deleted once callers are gone

## Related docs

- [`../../docs/troubleshooting/e2e-cluster-bootstrap.md`](../../docs/troubleshooting/e2e-cluster-bootstrap.md)
- [`../../docs/troubleshooting/e2e-repair-record-2026-04-20.md`](../../docs/troubleshooting/e2e-repair-record-2026-04-20.md)
- `OperationsPAI/aegis#23`, `#28`, `#36`-`#40`
