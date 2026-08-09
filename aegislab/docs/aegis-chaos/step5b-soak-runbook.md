# chaos-service post-cutover operations runbook

Audience: operator watching the chaos-service dispatch path in production
after §11 step 5c landed. The per-system executor flag, the chaos-mesh-direct
fallback, and the in-process CRD watcher are all gone — chaos-service is the
only dispatch path and there is nothing to flip.

This runbook covers: what to watch, what failure modes look like, and how to
narrow the diagnosis to a single subsystem when something breaks.

## 1. Prerequisites

- **Backend release** at or past the §11 step 5c merge:
  - Dispatcher always POSTs to `aegis-chaos /v1beta/injections`; no per-system
    flag exists in configcenter or in code.
  - `fault.injection.started` is emitted from
    `core/orchestrator/fault_injection.go::executeFaultInjection`.
  - `fault.injection.completed` is emitted from the webhook receiver
    (`crud/hooks/chaos/handler.go::fireOnce`) — that is the sole BuildDatapack
    trigger.
- **Cluster deploy** following [`runbooks/aegis-chaos-step5b-deploy.md`](../runbooks/aegis-chaos-step5b-deploy.md)
  (shared `rcabench-chaos-auth` Secret, `chaos.enabled: true`, webhook receiver
  wired).
- **chaos-client SA seeded** via initial-data reseed. On a fresh cluster the
  dispatcher mints + refreshes the SA token via
  `core/orchestrator/chaos_sa_token.go`; on an existing cluster the env
  fallback (`CHAOS_OUTBOUND_BEARER`) is used and the dispatcher logs the
  deprecation ERROR once per process — track separately.

Sanity check:
```bash
kubectl -n exp logs deploy/rcabench-aegis-api | grep '"component":"chaos_sa_token"' | tail -5
# Expect one INFO "backend→chaos SA token minted" per refresh cycle.
```

## 2. What to watch

### 2.1 Prometheus

`aegis_injection_dispatch_total{system}` — counter declared at
`core/orchestrator/dispatcher.go::injectionDispatchTotal`. The only label is
`system` (the logical name: `ts`, `otel-demo`, …). A non-incrementing series
during active campaigns means the dispatcher is failing before the increment
— check the next two signals.

```promql
sum by (system) (rate(aegis_injection_dispatch_total[5m]))
```

### 2.2 Trace events

- `fault.injection.started` — emitted from `executeFaultInjection` once the
  chaos-service ACK arrives. Missing event after a dispatch_total increment
  means the dispatcher errored after the metric incremented but before the
  event emit — read the dispatcher log entry for the same task_id.
- `fault.injection.completed` — emitted from the webhook receiver's gate
  path. Missing means the webhook never fired (chaos-service didn't post back)
  or the gate row already existed (idempotent replay).

Tail:
```bash
/tmp/aegisctl trace stream --system=<sys> --tail=20
```

### 2.3 FaultInjection state lifecycle

Happy path on the chaos-service path:
`DatapackInitial(0) → DatapackInjectSuccess(2) → DatapackBuildSuccess(4) →
DatapackDetectorSuccess(6)` (numeric values from
`platform/consts/consts.go`). State 1/3/5 are the failure absorbing states.

The webhook receiver writes the `fault_injections` row directly — there is
no orchestrator-side CreateInjection any more. The row carries:
- `chaos_injection_id` (chaos-service ULID, indexed)
- `engine_config` (JSON of the originating GuidedConfig slice, round-tripped
  via `caller_metadata.engine_config`)
- `groundtruths` (from `caller_metadata.groundtruths`,
  see `dispatcher.go::renderGroundtruths`)
- `task_id` FK back to `tasks` when `caller_metadata.has_backend_task` is true

Query:
```sql
SELECT id, name, status, chaos_injection_id, engine_config, created_at, updated_at
FROM fault_injections
WHERE benchmark = '<sys>'
ORDER BY id DESC LIMIT 20;
```

A row with `engine_config = '{}'` is a regression — the dispatcher's marshal
either failed silently or the webhook fell back to the empty-object default.

### 2.4 Logs to grep

```bash
kubectl -n exp logs deploy/rcabench-aegis-api --since=30m \
  | jq -c 'select(.component == "chaos_sa_token")'
```

- One `INFO` per refresh with `"backend→chaos SA token minted"`.
- Zero `ERROR` for `initial backend→chaos SA token mint failed` or
  `refresh failed`.
- Zero ERROR `DEPRECATED: backend→chaos auth using static
  CHAOS_OUTBOUND_BEARER`. One-shot per process — its presence means the pod
  is in env-fallback mode for its whole lifetime; restart only helps after
  the SA seed is in place.

## 3. Health criteria

A cluster is **healthy** when, over the observation window:

- **Zero FK retry fires.**
  `crud/hooks/chaos/handler.go::getOrCreateShadowFaultInjection` retries
  shadow-FI insert with `TaskID=nil` if the parent task row vanished mid-flight.
  ```bash
  kubectl -n exp logs deploy/rcabench-aegis-api --since=24h \
    | grep -c "shadow FI retry without TaskID"
  ```
  Must be 0 during steady-state.
- **Zero deprecation-fallback log entries** in the SA-token component
  (see §2.4).
- **`fault.injection.started` count equals `dispatch_total` increments** per
  system over the same window (±1 boundary cycle).
- **No state-1/3/5 terminations** outside of intentional regression cases.
  State 5 is detector-side (algorithm couldn't find a root cause) and is a
  separate concern.

## 4. Common failure modes

- **chaos-service unreachable / 5xx** → dispatcher logs
  `dispatcher: POST /v1beta/injections: ...` and the FI task lands in error.
  Check `chaos.service_url` and the chaos-service deployment health.
- **Bearer rejected** → dispatcher gets 401 from chaos-service. Check the
  SA token refresh log (§2.4) and `CHAOS_OUTBOUND_BEARER` if on env-fallback.
- **Webhook receiver never fires** → row stays at `DatapackInjectSuccess`
  forever. Check chaos-service → backend network path and the chaos service's
  own webhook delivery log.
- **`engine_config = '{}'` in fault_injections** → dispatcher marshal failed
  silently OR a chaos-service instance from before §11 step 5c is round-
  tripping the caller_metadata without preserving the new `engine_config`
  field. Check chaos-service version + the dispatcher log for the marshal
  error.

## 5. Escalation

- **Cancel propagation** — backend cancel issues DELETE to chaos-service via
  `CancelChaosServiceInjection` in `dispatcher.go`. A 404 from chaos-service
  is swallowed as idempotent cleanup. Anything else lands in error and the
  CR may leak — file an issue.
- **Groundtruth derivation** — the dispatcher's `renderGroundtruths` computes
  a minimal `{service, container}` impact from the originating GuidedConfig
  and the webhook handler writes it onto the shadow FI row. If algorithm
  containers start asserting empty groundtruth on chaos-service rows, check
  that path before blaming the executor.
