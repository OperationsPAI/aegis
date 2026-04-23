---
name: onboard-benchmark-system
description: >-
  How to onboard a new microservice benchmark (Online Boutique, TrainTicket,
  Hotel Reservation, Social Network, otel-demo, etc.) onto the aegis kind
  cluster so it can be chaos-injected and observed via the OTLP -> ClickHouse
  pipeline. Use this whenever the user asks to "add a new system", "run a
  demo", "onboard <benchmark>", "inject chaos on X service", or wants to
  gather fault-injection trace data on any workload that isn't already wired
  up. Also use when diagnosing why a newly deployed demo isn't emitting traces
  or why chaos isn't landing.
---

# Onboard a benchmark system

Goal: get a microservice workload (any of the ones listed in
`chaos-experiment`'s `internal/systemconfig`, or a fresh one) to the point
where you can run an injection and see the effect in ClickHouse traces.

This is a methodology briefing, not a runbook. Concrete commands and
verbatim manifests live under `references/`. Read the reference that
matches your step, not the whole directory.

## Phases

Each phase has an explicit exit criterion. **Do not advance until it's
met** — forging ahead past a broken phase wastes hours of debugging later.

### 1. Preflight
Verify cluster, chaos-mesh, and observability pipeline are healthy
*before* touching the new workload.

- Exit criterion: `kubectl get pods -A` is clean across `chaos-mesh`
  and `otel` namespaces; a smoke `NetworkChaos` on any existing pod
  reaches `AllInjected: True`.
- See `references/prerequisites.md`.

### 2. Deploy the workload
Pick a namespace dedicated to the system (e.g. `ts`, `demo`, `otel-demo`).
Prefer the upstream release manifest. Check public-registry images —
anything referencing an internal mirror needs re-tagging.

- Exit criterion: `kubectl wait --for=condition=available deploy --all`
  returns clean, and a host-side probe (curl or a service-internal
  health check) proves the workload is actually serving.
- See `references/deploy-patterns.md` for a worked example on
  Online Boutique.

### 3. Instrument for observability
**This is where most time is lost.** Every demo emits OTEL differently.
Some honor standard `OTEL_EXPORTER_OTLP_ENDPOINT`, some need a custom
env var and panic without it, some ship their own collector.

- Exit criterion: a phase-windowed query (§6) shows non-zero spans for
  at least one caller-side client span from the services you plan to
  target or measure through.
- See `references/instrumentation-patterns.md`.

### 4. Pick the injection path
Three in descending order of integration, ascending order of reliability:

1. `aegisctl inject guided …` / `aegisctl regression run …` → backend → CRD
2. `chaos-experiment` CLI → CRD directly
3. Hand-written NetworkChaos / PodChaos YAML + `kubectl apply`

Try 1 first. If the backend rejects your namespace (unknown benchmark),
that's an aegis-control-plane problem, not a workload problem — go run
the `register-aegis-system` skill (specifically `aegisctl system
register --from-seed` + `aegisctl pedestal chart push/install`), then
come back here. Only fall back to path 3 if you explicitly want a
one-shot smoke test and don't care about datapack collection.

- Exit criterion: `aegisctl regression run <case> --auto-install`
  passes preflight, or you have a minimal NetworkChaos manifest ready.

### 5. Define time windows before injecting
Measurement is only meaningful against three timestamped windows:
`baseline`, `inject`, `recovery`. Capture `date -u +%Y-%m-%dT%H:%M:%S`
at each boundary in shell variables; you'll use them in the ClickHouse
query. This matters because spans keep flowing continuously and the
windows are how you separate signal from noise.

### 6. Run + measure
Apply chaos, wait enough samples, delete chaos, wait recovery, then
query ClickHouse once with a phase-windowed `multiIf`. Query the
**caller-side** spans, not the target's server spans — a `direction: to`
delay barely moves the target's own processing time.

- See `references/phase-windowed-measurement.md` for the query template.

### 7. Document
Write the result table (phase × avg/p50/p95) plus the reproduction recipe
into `docs/deployment/NN-<system>-smoke.md`. If you hit a new trap, add
one `known-gaps.md` entry with Where / Symptom / Root cause / Workaround
/ Suggested fix. Other onboarders will hit the same trap.

## Heuristics for skipping phases

- Already-onboarded system (listed in `chaos-experiment/internal/*/`):
  phases 1, 5, 6, 7 only — skip deploy/instrument unless something
  changed upstream.
- Same workload, new chaos type: phase 4, 5, 6 only.
- Net-new system: all seven. Budget ~2–4 hours.

## When things go sideways

If your chaos is `Applied` but latency doesn't move, or traces never
arrive, don't keep poking at manifests — the failure is almost always
one of the four patterns in `references/troubleshooting.md`. Diagnose
via the chaos-daemon log and the collector log before editing YAML.

## What "onboarded" means

A system is onboarded when someone unfamiliar with it can:
- Apply one chaos spec and see the effect in a traces query within 5
  minutes of reading the docs you wrote in phase 7.
- Reproduce without touching this skill's references — everything they
  need is in `docs/deployment/`.
