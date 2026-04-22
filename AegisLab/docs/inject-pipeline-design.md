# Fault Injection Pipeline - Design (2026-04-20)

This document describes the converged AegisLab fault-injection submit path.

The canonical path is now:

```text
GuidedConfig
  -> guidedcli.BuildInjection(...)
  -> handler.InjectionConf
  -> handler.BatchCreate(...)
```

`GuidedConfig` is the only accepted external submit format for `POST /api/v2/projects/{id}/injections/inject`.
`handler.InjectionConf` remains an internal execution IR used only inside the consumer/runtime path; it is not a new HTTP or SDK contract.

## Wire format

`SubmitInjectionReq` in `src/module/injection/api_types.go` accepts:

- `pedestal`, `benchmark`, `interval`, `pre_duration`, `algorithms`
- `specs [][]GuidedSpec` - outer slice is batches, inner slice is a batch of guided configs executed in parallel

Each `specs[i][j]` entry must be a `guidedcli.GuidedConfig`-shaped object with top-level `chaos_type`.
Legacy `FriendlyFaultSpec`, raw `chaos.Node`, and mixed guided/legacy payloads are rejected with a 4xx response during request binding/validation.

## End-to-end flow

```text
HTTP POST /api/v2/projects/{id}/injections/inject
    |
    v
module/injection.Handler.SubmitProjectFaultInjection
    |
    v
module/injection.Service.SubmitFaultInjection
    |
    v
parseBatchGuidedSpecs(...)
    |
    |  validates batch shape / duplicate-service warnings
    v
RestartPedestal task
    |
    v
FaultInjection subtask
    |
    v
parseInjectionPayload(...)
    |
    v
guidedcli.BuildInjection(...)
    |
    v
handler.InjectionConf
    |
    v
handler.BatchCreate(...)
    |
    v
Chaos Mesh CRDs
```

## Current file map

- `src/module/injection/api_types.go` - guided-only submit request types and legacy-shape rejection
- `src/module/injection/handler.go` - HTTP submit handlers
- `src/module/injection/submit.go` - batch validation and deduplication for guided configs
- `src/module/injection/service.go` - guided-only task submission path
- `src/service/consumer/fault_injection.go` - guided-only runtime execution path
- `src/cmd/aegisctl/cmd/inject_guided.go` - canonical CLI submit path for guided configs
- `src/consts/consts.go` and `src/dto/task.go` - task payload keys and task envelope fields

## Behavior notes

1. `chaos_type` is the required dispatch key for every submitted fault spec.
2. Task payloads between producer and consumer only carry guided config batches (`guided_configs`).
3. The consumer is responsible for building live `handler.InjectionConf` values from guided configs right before execution.
4. `handler.InjectionConf` is an internal IR for runtime execution and is intentionally not exposed as a new external API shape.
5. Adding a new fault type in `chaos-experiment` should only require guided resolver/builder support there; Aegis no longer maintains FriendlySpec translators or raw Node passthrough logic on the main submit path.

## Migration status

- [x] GuidedConfig is the only accepted HTTP submit format
- [x] Producer task payloads carry guided config batches only
- [x] Consumer runtime executes only `guidedcli.BuildInjection(...) -> handler.BatchCreate(...)`
- [x] FriendlySpec / raw Node compatibility helpers are removed from the main submit path

## Related

- `OperationsPAI/aegis#23`, `#28`, `#36`-`#40`
