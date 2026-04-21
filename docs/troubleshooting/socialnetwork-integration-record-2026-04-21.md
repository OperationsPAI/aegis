# SocialNetwork (DSB) Integration Record — 2026-04-21

5th benchmark pedestal integrated using the `register-aegis-system` skill. Layers 1–4 done same-day; layer 5 (trace collection) unblocked later the same day by adding a Jaeger→OTLP bridge and a wrk2 load generator. The pipeline now reaches `RunAlgorithm` — the remaining failure is unrelated to trace ingestion and tracked as a downstream detector issue.

## Layers

| Layer | Status | Notes |
|---|---|---|
| 1 Compiled registry | n/a | runtime-registered via etcd (not compiled-in) |
| 2 etcd dynamic_configs | ✓ | 7 keys under `/rcabench/config/global/injection.system.sn.*` on `aegislab-backend-etcd-0` |
| 3 DB fixtures | ✓ | `containers.id=12 (sn)`, `container_versions.id=13`, `helm_configs` row pointing at `/var/lib/rcabench/dataset/charts/socialnetwork-aegis-0.1.0.tgz` |
| 4 Helm chart | ✓ | 27 pods Running in `sn0`; `nginx-thrift` serves `/wrk2-api/user/register` (200) |
| 5 Telemetry pipeline | ✓ (fixed) | Jaeger→OTLP bridge + wrk2 loadgen; spans landing in `otel.otel_traces` during fault window |

Pipeline end state (trace `2ea2897b-e2f8-4094-8cec-997d762c4b80`):
RestartPedestal ✓ → FaultInjection ✓ → **BuildDatapack ✓** → RunAlgorithm ✗ (detector assertion)

## Original blocker → fix: DSB Jaeger vs. OTLP

**Problem.** DSB services (hotelreservation Go, socialnetwork C++) push spans to an in-chart `jaegertracing/all-in-one` service via UDP compact-thrift on `jaeger:6831`, not to aegis's OTLP collector. Validation failed on `abnormal_traces.parquet has no data rows`.

**Fix.** Replace the `jaeger` deployment in each DSB subchart with `otel/opentelemetry-collector-contrib:0.100.0` running a receiver on the same Jaeger ports and exporting OTLP → `otel-collector.otel.svc.cluster.local:4317` → ClickHouse. Services keep their `jaeger:6831` config unchanged. Implemented in both `benchmark-charts/charts/hotelreservation-aegis/` and `.../socialnetwork-aegis/`. Spans confirmed in `otel.otel_traces` (note: `otel.otel_traces`, not `default.otel_traces` — wasted some debug time on the wrong DB).

## Next blocker → fix: no load during fault window

**Problem.** With the bridge up, traces still didn't land in `abnormal_traces.parquet`. Root cause: DSB ships `wrk2` scripts (`socialNetwork/wrk2/scripts/.../compose-post.lua`, etc.) but expects the operator to drive load manually from outside the cluster — no loadgen Deployment. During a fault window there's nothing pushing traffic through nginx-thrift, so no traces exist for the abnormal window.

**Fix.** Built `docker.io/opspai/dsb-wrk2:20260421` — wrk2 binary + DSB lua scripts for both socialNetwork and hotelReservation, with an entrypoint that loops wrk forever at configurable threads/conns/rate. Added a `loadgen` Deployment to each wrapper chart (`benchmark-charts/charts/*/templates/loadgen.yaml`). Build context lives in `benchmark-charts/images/dsb-wrk2/` with README pointing to DSB for `src/`, `deps/`, `Makefile`.

Additionally: avoid picking the entry service (e.g. `nginx-thrift`) as the fault target — killing the entry stops traffic flow, emptying the abnormal window. Mid-tier services (e.g. `user-service`) work as targets because loadgen keeps hitting nginx-thrift and many other services continue to emit spans during the fault.

## Current blocker (not today's scope): detector service-graph assumption

**Trace `2ea2897b-...`**: BuildDatapack completes, then `RunAlgorithm` (detector evaluation step) errors with:

```
AssertionError: Service 'user-timeline-mongodb' not found in graph
  at /app/src/rcabench_platform/v3/internal/metrics/metrics_calculator.py:26
```

DSB's jaeger-client-cpp only instruments RPC between services — it doesn't auto-instrument MongoDB/Redis/Memcached clients the way OTel SDK does for otel-demo/ob. So the trace graph for sn is missing `*-mongodb`, `*-redis`, `*-memcached` nodes that the rcabench-platform detector expects. This is a platform-side assumption, not a trace-pipeline problem.

Two follow-up options:
1. **rcabench-platform side (preferred)**: relax the detector to build its graph from spans actually present rather than asserting against an expected service list. Single-file change near `metrics_calculator.py:26`.
2. **DSB side**: OTel-instrument the DSB source (retrofit OTel C++/Go SDKs into services, or run an OTel auto-instrumentation sidecar). Large effort, essentially re-instrumenting DSB.

## Follow-ups

- [ ] `rcabench-platform`: build service graph from observed spans (detector should not assert on an expected service list).
- [ ] `aegis`: automate backend chart upload (`helm package` + `kubectl cp` into `/var/lib/rcabench/dataset/charts/` is still manual; every chart edit requires re-upload before RestartPedestal picks it up).
- [ ] `aegis`: automate DB + etcd seeding from `data/initial_data/*/data.yaml` (hand-written INSERTs and `etcdctl put` loops are still the norm, consistent with prior records).
- [ ] `aegis`: regression submit should reject entry-service as fault target, or warn — today it silently leads to empty abnormal windows.

## Image / chart artifacts

- `docker.io/opspai/dsb-wrk2:20260421` — loadgen container (pushed + kind-loaded on all 3 nodes)
- `benchmark-charts/charts/socialnetwork-aegis/` — new wrapper chart with loadgen + OTLP-bridged jaeger subchart
- `benchmark-charts/charts/hotelreservation-aegis/` — wrapper updated with loadgen + OTLP-bridged jaeger subchart
- `benchmark-charts/images/dsb-wrk2/` — Dockerfile + entrypoint + lua scripts for the loadgen image
