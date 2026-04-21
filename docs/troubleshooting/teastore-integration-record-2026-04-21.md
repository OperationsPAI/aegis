# TeaStore Integration Record — 2026-04-21

7th benchmark pedestal integrated. First non-DSB-family Java stack. End-to-end regression passed on first try.

## Layers

| Layer | Status | Notes |
|---|---|---|
| 1 Compiled registry | n/a | runtime-registered via etcd |
| 2 etcd dynamic_configs | ✓ | 7 keys under `/rcabench/config/global/injection.system.tea.*` |
| 3 DB fixtures | ✓ | `containers.id=14 (tea)`, `container_versions.id=15`, helm_configs row |
| 4 Helm chart | ✓ | 7 StatefulSets + 1 bridge + 1 loadgen Running in `tea0` |
| 5 Telemetry pipeline | ✓ | Jaeger-client-java → otel-collector bridge → OTLP → ClickHouse |

Regression trace `6c420ab6-5528-4e91-8e6f-29e9b7e1f0aa`:
RestartPedestal ✓ → FaultInjection ✓ → BuildDatapack ✓ → RunAlgorithm ✓ → CollectResult ✓ (`datapack.no_anomaly`).

Short code: `tea` (not `ts` — already taken by TrainTicket in platform).

## Tracing: jaeger-client-java (surprise: same family as DSB)

TeaStore uses `io.jaegertracing.internal.JaegerTracer` (OpenTracing-era Jaeger Java client) — same on-the-wire UDP compact-thrift as DSB. Despite being a completely different codebase, the exact same Jaeger→OTLP bridge pattern works.

**Difference from DSB**: TeaStore's chart ships no `JAEGER_AGENT_HOST` env, so jaeger-client-java falls back to `localhost:6831` by default — spans go nowhere. Had to inject env into each of the 6 Java-service statefulsets (auth, image, persistence, recommender, webui, registry). Used a Python script to patch templates — 5 had an existing `env:` block to append to, 1 (registry) needed a new `env:` block inserted before `resources:`.

```yaml
env:
  - name: JAEGER_AGENT_HOST
    value: "jaeger"
  - name: JAEGER_AGENT_PORT
    value: "6831"
```

The bridge itself is a wrapper-level template (not a subchart override like DSB) because TeaStore chart has no `jaeger` subchart slot — it's just a Deployment + Service named `jaeger` that takes `JAEGER_AGENT_HOST=jaeger` DNS resolution. Otherwise identical otel-collector-contrib receiver/exporter config.

## Loadgen: locust (not wrk2)

Upstream TeaStore ships a `locustfile.py` driver. Used public `locustio/locust:2.27.0` image + mounted the locustfile via ConfigMap. No custom image needed.

Webui is the entry service at `teastore-webui:80` (kubernetes Service, not ingress). Locust hits `/`, `/login`, `/category`, `/product`, `/cartAction`, etc.

## App label convention

Unlike DSB family (all pods labeled `app: <service-name>`), TeaStore labels pods with:
- `app.kubernetes.io/part-of: teastore` (common)
- `app.kubernetes.io/name: <microservice>` (per-pod, e.g. `auth`, `webui`, `registry`)

Set `injection.system.tea.app_label_key = app.kubernetes.io/name` so guided-inject targets individual microservices. Regression YAML uses `app: auth` (mid-tier) with the `app_label_key` lookup handling the `.` naming.

## Fault target

TeaStore entry = `webui` (handles all user HTTP). Avoid targeting it (same rule as DSB). Regression picks `auth`. Other safe mid-tier targets: `image`, `persistence`, `recommender`, `registry` (registry is critical for service discovery but still recoverable; prefer the others for lighter blast radius).

## Concrete artifacts

- `benchmark-charts/charts/teastore-aegis/` — wrapper chart
- `benchmark-charts/charts/teastore-aegis/charts/teastore/templates/*-statefulset.yaml` — patched with JAEGER env
- `benchmark-charts/charts/teastore-aegis/templates/jaeger-bridge.yaml` — wrapper-level otel-collector-contrib Deployment + ConfigMap + Service
- `benchmark-charts/charts/teastore-aegis/templates/loadgen.yaml` — locust Deployment + ConfigMap
- `AegisLab/regression/teastore-guided.yaml` — smoke case
- `AegisLab/data/initial_data/{prod,staging}/data.yaml` — seed entries
- DB: `containers.id=14`, `container_versions.id=15`, 7 `dynamic_configs`, 1 `helm_configs`
- etcd: 7 keys `/rcabench/config/global/injection.system.tea.*`
- `/var/lib/rcabench/dataset/charts/teastore-aegis-0.1.0.tgz` in backend producer pod

## Recurring gotchas (confirmed again)

- Pre-install chart once before first regression (guided-inject validates `app=...` against live pods at submit time).
- Chart edits → `helm package` + `kubectl cp` into backend pod before next rerun.
- Traces land in `otel.otel_traces`, not `default.otel_traces`.

## Reusable infra summary (after 3 DSB + 1 TeaStore)

The "Jaeger→OTLP bridge" pattern works for **any** stack whose services use OpenTracing-era Jaeger clients (jaeger-client-{java,go,cpp,python}) — just deploy an otel-collector-contrib as Service `jaeger` with a jaeger receiver and wire the clients at `jaeger:6831`. No per-service instrumentation needed.

**Detector `opspai/detector:graph-warn-20260421`** handles any stack where the trace graph misses nodes (DSB misses DB/cache clients; TeaStore's graph is RPC-only too). No more stack-specific graph asserts.
