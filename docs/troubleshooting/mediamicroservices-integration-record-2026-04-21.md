# MediaMicroservices (DSB) Integration Record — 2026-04-21

6th benchmark pedestal integrated, 3rd from the DeathStarBench family (after hs and sn). End-to-end regression passed on first try — the DSB infrastructure landed earlier the same day (Jaeger→OTLP bridge, `dsb-wrk2` loadgen image, detector graph-warn) was reused wholesale.

## Layers

| Layer | Status | Notes |
|---|---|---|
| 1 Compiled registry | n/a | runtime-registered via etcd |
| 2 etcd dynamic_configs | ✓ | 7 keys under `/rcabench/config/global/injection.system.mm.*` |
| 3 DB fixtures | ✓ | `containers.id=13 (mm)`, `container_versions.id=14`, `helm_configs` row |
| 4 Helm chart | ✓ | 34 pods Running in `mm0`; `nginx-web-server` serves `/wrk2-api/review/compose` |
| 5 Telemetry pipeline | ✓ | Jaeger subchart swapped to otel-collector-contrib; spans land in `otel.otel_traces` |

Regression trace `c588aa6c-4a34-42e8-8ccd-0f33a1569143`:
RestartPedestal ✓ → FaultInjection ✓ → BuildDatapack ✓ → RunAlgorithm ✓ → CollectResult ✓ (`datapack.no_anomaly`).

## Reused from earlier DSB work

- **Jaeger→OTLP bridge** — copied sn's `charts/media-microservices/charts/jaeger/templates/{configmap.yaml,deployment.yaml}` template pattern verbatim (just renamed the `socialnetwork/service` label to `mediamicroservices/service`).
- **dsb-wrk2 loadgen image** — rebuilt as `opspai/dsb-wrk2:20260421-media` after adding `scripts/media-microservices/compose-review.lua` to `benchmark-charts/images/dsb-wrk2/scripts/`. The Dockerfile + entrypoint were unchanged.
- **Detector graph-warn** — `opspai/detector:graph-warn-20260421` (shipped earlier today) handles the `*-mongodb / *-memcached / *-redis` nodes that DSB C++ jaeger-client doesn't instrument. No code changes needed for media.

## Fault target choice

Upstream chart has `nginx-web-server` as the entry service — do not use it as fault target (same trap as sn/hs: kills the entry, empties the abnormal window). Regression targets `user-service` (one of several mid-tier options; any of `movie-review-service`, `compose-review-service`, `page-service` would work too).

## wrk2 script limitation

`compose-review.lua` hardcodes `username_1..1000` + `password_1..1000` + `movie_titles[1..1000]`. DSB expects you to pre-run `register_users.sh` + `register_movies.sh` — we skipped that: 4xx/5xx responses still emit spans, which is all the regression needs. If you care about response correctness, add a pre-register Job to the chart.

## Concrete artifacts

- `benchmark-charts/charts/mediamicroservices-aegis/` — wrapper chart (Chart.yaml + values.yaml + templates/loadgen.yaml + jaeger-bridge override)
- `benchmark-charts/charts/mediamicroservices-aegis/charts/media-microservices/` — upstream DSB chart + modified `charts/jaeger/templates/{configmap.yaml,deployment.yaml}`
- `benchmark-charts/images/dsb-wrk2/scripts/media-microservices/compose-review.lua` — added to the wrk2 image source
- `docker.io/opspai/dsb-wrk2:20260421-media` — pushed
- `AegisLab/regression/mediamicroservices-guided.yaml` — regression case
- `AegisLab/data/initial_data/{prod,staging}/data.yaml` — seed entries
- DB: `containers.id=13`, `container_versions.id=14`, 7 `dynamic_configs` rows, 1 `helm_configs` row
- etcd: 7 keys `/rcabench/config/global/injection.system.mm.*`
- Backend chart store: `/var/lib/rcabench/dataset/charts/mediamicroservices-aegis-0.1.0.tgz` (in producer pod)

## Recurring DSB-family gotchas (unchanged)

- Pre-install the chart once manually before first regression — guided inject validates `app=...` against currently running pods, but `RestartPedestal` only runs *during* the trace. After the first install, `RestartPedestal` handles subsequent reruns via `helm upgrade --install` from the backend tgz.
- Traces land in the `otel` database (`otel.otel_traces`), not `default.otel_traces`.
- Avoid entry service (`nginx-web-server`) as fault target.
- Chart edits → `helm package` + `kubectl cp` into backend pod before next run, or RestartPedestal silently rolls back to the stored tgz.

## Still-open follow-ups

(Same list as sn/hs; not media-specific):
- Automate backend chart upload (still manual `helm package` + `kubectl cp`).
- Automate DB + etcd seeding from `data/initial_data/*/data.yaml`.
- Regression should reject/warn when fault target equals entry service.
- Regression should auto-install chart on first run, not require manual pre-install.
