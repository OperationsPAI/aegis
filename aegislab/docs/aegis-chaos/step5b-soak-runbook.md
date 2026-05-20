# §11 step 5b — per-system soak runbook

Operational companion to [`step5b-residual-debt.md`](step5b-residual-debt.md).
Audience: operator rolling the chaos-service cutover one logical system at a
time before step 5c (CRD watcher teardown — irreversible).

Per system: flip the executor-authoritative flag, watch the pipeline emit on
the new path for N cycles, advance. If anything trips a fail-criterion in §4
below, roll back per §5 and file the gap before retrying.

The flag is etcd-backed via configcenter; viper bridges it into the running
backend pods, so a flip is picked up live — no redeploy.

## 1. Prerequisites

- **aegisctl** built off main ≥ `7ea3ec79` so the `injection executor`
  subcommand exists (`aegislab/src/cli/cmd/injection_executor.go`):
  ```bash
  just build-aegisctl                      # → /tmp/aegisctl
  /tmp/aegisctl injection executor --help  # must list get/set/list/unset
  ```
- **Backend release** at or past `7ea3ec79` (current main):
  - W1: SA-minted bearer in `core/orchestrator/chaos_sa_token.go` →
    `resolveChaosOutboundBearer()` in `dispatcher.go` prefers it over
    `CHAOS_OUTBOUND_BEARER`.
  - W2: `fault.injection.started` emit on the chaos-service path lives at
    `core/orchestrator/fault_injection.go:313` (`withEvent(consts.EventFaultInjectionStarted, name)`).
  - W3: `--via-chaos` on `regression run` errors out
    (`cli/cmd/regression.go:304-305`).
- **Cluster deploy** following [`runbooks/aegis-chaos-step5b-deploy.md`](../runbooks/aegis-chaos-step5b-deploy.md)
  for byte-cluster (shared `rcabench-chaos-auth` Secret, `chaos.enabled: true`,
  webhook receiver wired).
- **chaos-client SA seeded** via initial-data reseed.
  - Today the SA row is only present after `aegislab/manifests/byte-cluster/initial-data/*`
    runs against a fresh cluster. On an existing cluster the mint logs
    `initial backend→chaos SA token mint failed` and the dispatcher falls
    through to `CHAOS_OUTBOUND_BEARER`.
  - Workaround until post-seed gap is closed: keep the
    `CHAOS_OUTBOUND_BEARER` env wired on existing clusters; for fresh
    clusters the SA path is the supported one. Confirm with:
    ```bash
    kubectl -n exp logs deploy/rcabench-aegis-api | grep '"component":"chaos_sa_token"' | tail -5
    # Expect one INFO "backend→chaos SA token minted" per refresh cycle.
    ```

## 2. Per-system flip procedure

Pick one system at a time, in the order from §6.

Replace `<sys>` with the logical name (`ts`, `otel-demo`, `hs`, `sn`, `mm`,
`teastore`, `sockshop`).

### 2.1 Baseline (before flip)

```bash
/tmp/aegisctl injection executor get --system=<sys>
# Bare output: chaos-mesh-direct       (default)
# or:          chaos-service           (already flipped — investigate)
```

JSON form for capture into a runbook log:

```bash
/tmp/aegisctl injection executor get --system=<sys> -o json
# {"path":"chaos-mesh-direct","set":false,"system":"<sys>"}
```

### 2.2 Flip

```bash
/tmp/aegisctl injection executor set \
  --system=<sys> \
  --path=chaos-service \
  --reason="step 5b soak start: <sys>"
# stderr: ok: system=<sys> <unset, defaults to chaos-mesh-direct> -> chaos-service
```

Dry-run first if landing during a live campaign:

```bash
/tmp/aegisctl injection executor set --system=<sys> --path=chaos-service \
  --reason="dry-run" --dry-run
```

### 2.3 Verify

```bash
/tmp/aegisctl injection executor get --system=<sys>
# Must print: chaos-service
```

Cross-check the running backend picked it up (viper poll cadence is on the
order of a couple seconds; wait 5s):

```bash
kubectl -n exp logs deploy/rcabench-aegis-api --since=30s \
  | grep -E 'executor.*chaos-service|injection_dispatch_total'
```

### 2.4 Wait for one full inject→datapack cycle before measuring

A cycle here means: `FaultInjection` row goes
`DatapackInitial(0) → DatapackInjectSuccess(2) → DatapackBuildSuccess(4) →
DatapackDetectorSuccess(6)` (state ints from `platform/consts/consts.go:218-226`;
the residual-debt doc's "state=6" is `DatapackDetectorSuccess`).

End-to-end this takes ~3–6 minutes depending on `pre_duration` and the
benchmark's loadgen warmup. Do not start measuring §4 criteria until at least
one full cycle has terminated on the new path.

## 3. What to watch

All identifiers below are grep-verifiable against the source tree at this
SHA. The verifications below are based on the code at
`7ea3ec79c065aadd27459c976d72c31f4657ff5f`.

### 3.1 Prometheus

`aegis_injection_dispatch_total{path,system}` — counter, declared at
`core/orchestrator/dispatcher.go:47-50`, incremented at line 175. Labels:

- `path` ∈ {`chaos-mesh-direct`, `chaos-service`}
- `system` = the logical system name (`ts`, `otel-demo`, …)

```promql
sum by (path) (rate(aegis_injection_dispatch_total{system="<sys>"}[5m]))
```

After flip, the `chaos-mesh-direct` series for that system must stop
incrementing on **new** dispatches; `chaos-service` series must be the sole
mover. Any non-zero `chaos-mesh-direct` rate for `<sys>` after the flip is a
signal the etcd flag isn't reaching the pod (viper bridge sick, or operator
hit a stale aegisctl).

### 3.2 Trace events (Redis stream)

- `fault.injection.started` — `EventFaultInjectionStarted` constant at
  `platform/consts/consts.go:519`. On the chaos-service path it is emitted
  by `executeFaultInjection` at `core/orchestrator/fault_injection.go:313`.
  On the legacy chaos-mesh-direct path the CRD watcher
  (`core/orchestrator/k8s_handler.go:181`) emits the same event — see §3.4
  for distinguishing the two.
- `fault.injection.completed` — `EventFaultInjectionCompleted` at
  `platform/consts/consts.go:520`. On the chaos-service path this comes from
  the webhook handler in `crud/hooks/chaos/handler.go` (gate path); on the
  legacy path from `HandleCRDSucceeded`.

Tail with:

```bash
/tmp/aegisctl trace stream --system=<sys> --tail=20
```

After the flip, every successful cycle must show both `started` and
`completed` for `<sys>`. Missing `started` after a `dispatch_total{path=chaos-service}`
increment is a W2 emit regression — file before continuing.

### 3.3 FaultInjection state lifecycle

Numeric states from `platform/consts/consts.go:215-226`:

| int | name                       |
|-----|----------------------------|
| 0   | `DatapackInitial`          |
| 1   | `DatapackInjectFailed`     |
| 2   | `DatapackInjectSuccess`    |
| 3   | `DatapackBuildFailed`      |
| 4   | `DatapackBuildSuccess`     |
| 5   | `DatapackDetectorFailed`   |
| 6   | `DatapackDetectorSuccess`  |

Happy path is `0 → 2 → 4 → 6` (the "1→6" framing the design uses for
"hand-waving" includes the odd-numbered failure absorbing states; on the
soak path you want even-only). Any cycle that terminates at 1/3/5 is a
fail-pattern — count toward §4 budget.

Query:

```sql
SELECT id, name, status, chaos_injection_id, created_at, updated_at
FROM fault_injections
WHERE benchmark = '<sys>'
ORDER BY id DESC LIMIT 20;
```

### 3.4 Logs to grep

```bash
kubectl -n exp logs deploy/rcabench-aegis-api --since=30m \
  | jq -c 'select(.component == "chaos_sa_token")'
```

- One `INFO` per refresh with `"backend→chaos SA token minted"` and
  `"expires_at"` field (`chaos_sa_token.go:81`).
- Zero `ERROR` for `initial backend→chaos SA token mint failed`
  (`chaos_sa_token.go:46`) or `refresh failed` (line 69).
- Zero ERROR `DEPRECATED: backend→chaos auth using static CHAOS_OUTBOUND_BEARER`
  (`dispatcher.go:91-92`). One-shot per process, so if you see it the pod
  is in env-fallback mode for the whole life of the process — restart only
  helps after the SA seed is in place.

Distinguishing dispatch path in `fault.injection.started`:

The event payload carries an `executor_path` field
(`chaos-service` or `chaos-mesh-direct`) set by the emit site, so the audit
is a single jq query against the trace stream:

```bash
aegisctl trace stream --event fault.injection.started --since=30m \
  | jq -r '.payload.executor_path' | sort | uniq -c
```

Legacy emits without the field decode as `null` (`omitempty`); treat those
as `chaos-mesh-direct` for historical windows.

## 4. Soak pass criteria

A system **passes** when, on the chaos-service path, **all** of the following
hold over the soak window:

- **Cycle count.** Five consecutive successful inject→datapack cycles
  terminating at state 6 (`DatapackDetectorSuccess`), or 24 hours of
  continuous production traffic without an anomalous failure, whichever
  comes first.
- **Zero FK retry fires.** `crud/hooks/chaos/handler.go::getOrCreateShadowFaultInjection`
  has a `TaskID = nil` retry at line 354 for the case where the parent task
  row was deleted out from under an in-flight webhook. Count grep hits in
  the soak window:
  ```bash
  kubectl -n exp logs deploy/rcabench-aegis-api --since=24h \
    | grep -c "shadow FI retry without TaskID"
  ```
  Must be 0.
- **Zero deprecation-fallback log entries.** The env-bearer one-shot ERROR
  at `dispatcher.go:91-92` must not fire during the soak. (One-shot per
  process: check freshly across the whole pod lifetime, not just window.)
- **`fault.injection.started` present in every cycle.** Count must equal
  the number of `chaos-service` dispatches for the system in the same
  window:
  ```promql
  sum(increase(aegis_injection_dispatch_total{path="chaos-service",system="<sys>"}[24h]))
  ```
  vs trace-event count from §3.2 — equal within ±1 (boundary cycle).
- **No state-1/3/5 terminations** in the cycle sample. The detector may
  legitimately return state 5 if the algorithm container ran but couldn't
  find a root cause; that's a separate detector issue, not a soak failure
  — but it still **does not count toward the 5-cycle budget**. Re-run until
  you have 5 clean state-6 terminations.

Record the result in the per-system row of the soak ledger (a markdown table
the operator maintains externally — not committed). Move to the next system.

## 5. Rollback SOP

Single command, no redeploy:

```bash
/tmp/aegisctl injection executor set \
  --system=<sys> \
  --path=chaos-mesh-direct \
  --reason="step 5b rollback: <reason>"
```

Or remove the override entirely (system reverts to default
`chaos-mesh-direct`):

```bash
/tmp/aegisctl injection executor unset --system=<sys> --yes \
  --reason="step 5b rollback: <reason>"
```

Verify:

```bash
/tmp/aegisctl injection executor get --system=<sys>
# Must print: chaos-mesh-direct
```

Confirm the next inject takes the legacy in-process branch:

```promql
increase(aegis_injection_dispatch_total{path="chaos-mesh-direct",system="<sys>"}[5m]) > 0
```

— and the `chaos-service` series for the same system goes flat.

No backend restart is needed; viper picks up the etcd change live. If the
metric does not move within one campaign-submit cycle, re-check the
configcenter audit log via `aegisctl etcd events` — the write may have
been blocked.

## 6. Rollout order

```
ts → otel-demo → hs → sn → mm → teastore → sockshop
```

Rationale (extrapolated from §11; the design doc lists the order without
calling out per-step reasoning):

- **ts** first — highest production traffic, most points in the catalog,
  best regression coverage. Largest blast radius but also fastest failure
  detection.
- **otel-demo** second — smallest topology, already exercised the chaos-service
  path end-to-end in the r11i validation (`step5b-residual-debt.md` references
  FI row 60). Confidence-builder after `ts`.
- **hs / sn / mm** — DSB stack (Go / C++), share the same Jaeger→OTLP bridge
  and dsb-wrk2 loadgen. Adjacent failure modes — group them so a CNI / OTel
  regression surfaces once, not three times.
- **teastore** — Descartes Java stack, independent loadgen (locust),
  env-inject. Different enough from DSB to catch JVM-specific path issues.
- **sockshop** — last because it had the etcd-auto-seed / chart-publish /
  prereq-manage gaps surface during integration (`project_sockshop_integration`),
  so its onboarding code path is the youngest and most likely to surface a
  late-stage issue. Catch it after every other system is green.

Recommendation only — re-order if a specific system has known instability
or a pinned operator constraint. Document the reason in the soak ledger.

## 7. Known gaps & escalation

- **Cancel path not yet wired (task #36 in progress).** Backend cancel does
  not propagate to chaos-service. Implication for soak: if a campaign is
  cancelled mid-flight on the chaos-service path, expect a leaked CR until
  5b cancel lands. **Do NOT cancel mid-campaign during soak** — let it run
  to terminal state, or rely on `pre_duration` to age it out.
- **`chaos_sa_token` SA seed only takes effect on fresh cluster** — see §1.
  Existing-cluster workaround: keep `CHAOS_OUTBOUND_BEARER` Secret in place;
  it will trigger the deprecation ERROR once but otherwise works. Track the
  post-seed-mint gap separately (no issue link yet — file if you hit it).
- **`--via-chaos` on `aegisctl regression run` errors out** (W2 cleanup,
  `cli/cmd/regression.go:304`). Operators must use the standard backend
  submit (e.g. `aegisctl regression run` without the flag, or
  `aegisctl inject guided` for a standalone client smoke). Older
  `AEGIS_INJECT_VIA_CHAOS=1` shells will hit the error — clear the env.
- **CRD watcher coexistence still required** (residual-debt item 2). The
  in-process watcher remains the sole `fault.injection.completed` emitter
  for legacy systems until 5c. Do not pre-disable it.
- **Groundtruth computed server-side** (residual-debt item 1). The
  dispatcher's `renderGroundtruths` populates `caller_metadata.groundtruths`;
  the webhook handler writes it onto the shadow FI row. If algorithm
  containers start asserting empty groundtruth on chaos-service rows,
  check this path before blaming the executor flip.

## 8. After all systems soak

Once every system in §6 has cleared §4 against the chaos-service path
without a rollback in the soak window, proceed to step 5c teardown — tracked
separately as task #39. Do NOT begin 5c teardown work from this runbook;
that step is irreversible and has its own gates (see §11 of the design doc
and the trigger list in `step5b-residual-debt.md`).
