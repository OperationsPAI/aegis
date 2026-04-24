# Benchmark Integration Playbook

Consolidated runbook for onboarding a new microservice benchmark onto
aegis-local. Derived from 5 per-system integration records produced on
2026-04-21 (sockshop, hotelreservation, socialnetwork, mediamicroservices,
teastore). Supersedes the individual `*-integration-record-2026-04-21.md`
files.

Use alongside the `register-aegis-system` skill — this doc captures the
recurring pitfalls, the reusable infra, and per-system deltas.

---

## 1. Overview — the 5-layer mental model

Every benchmark integration touches the same five layers. If the pipeline
fails, first identify which layer is misconfigured; the failure symptoms
are near-unique per layer.

| # | Layer | What it holds | Owner | Failure symptom if skipped |
|---|-------|---------------|-------|----------------------------|
| 1 | Compiled registry (`chaos-experiment/internal/systemconfig`) | Short system code (`ts`, `ob`, `hs`, `sn`, `mm`, `ss`, `tea`), `NsPattern` prefix | Go constant | n/a for runtime-registered systems |
| 2 | etcd dynamic_configs (`/rcabench/config/global/injection.system.<code>.*`) | 7 per-system injection defaults | `etcdctl put` | `Removed runtime-only system registration: X` after backend restart |
| 3 | DB fixtures (`containers`, `container_versions`, `helm_configs`, `dynamic_configs`) | Pedestal row, chart pointer, defaults | MySQL INSERTs | Submit rejects `system_type`, or `record not found` in listener |
| 4 | Helm chart (wrapper in `benchmark-charts/charts/<name>-aegis/`) | Upstream chart + aegis-specific overrides; pushed OCI or local tgz | `helm package` + push or `kubectl cp` | Pods never start; `available apps: …` empty |
| 5 | Telemetry pipeline (OTLP → ClickHouse `otel.otel_traces`) | Spans for the abnormal window | OTel SDK or jaeger-bridge | `abnormal_traces.parquet has no data rows` |

Layer 1 is optional: systems registered via etcd (`sn`, `mm`, `ss`, `hs`,
`tea`) do not need a Go const. Only layers 2–5 are per-integration work.

---

## 2. Step-by-step integration template

Once chart + data.yaml are prepared, runtime wiring on a live cluster is
3 `aegisctl` commands. Lower-level mechanics are under "What each step
does" for debugging.

**Prep (outside aegisctl):** pick short code (no collision with compiled
registry — `ts`=TrainTicket, TeaStore uses `tea`); author wrapper chart
under `benchmark-charts/charts/<code>-aegis/` with `app_label_key`
propagation, telemetry (Jaeger bridge or OTel SDK →
`otel-collector.otel.svc.cluster.local:4317`), and a loadgen; add
pedestal + 7 `injection.system.<code>.*` entries to
`AegisLab/data/initial_data/{prod,staging}/data.yaml`;
`helm package` → `<code>-aegis-<ver>.tgz`; add regression case
`AegisLab/regression/<code>-guided.yaml`.

**Runtime wiring (3 commands):**

```bash
# 1. Seed etcd + dynamic_configs atomically from data.yaml.
aegisctl system register --from-seed AegisLab/data/initial_data/prod/data.yaml --name <code>

# 2. Publish the chart (skip if published remotely; step 3 will fetch).
aegisctl pedestal chart push --name <code> --tgz ./<code>-aegis-<ver>.tgz

# 3. Preflight + submit. --auto-install runs chart install if ns is empty.
aegisctl regression run <code>-guided --auto-install
```

### What each step does

Useful when something goes sideways and you reach for `etcdctl`, `mysql`,
or `kubectl cp` directly.

1. **`system register --from-seed`** writes 7
   `/rcabench/config/global/injection.system.<code>.*` etcd keys + 7
   `dynamic_configs` rows atomically (DB before etcd, so
   `config_listener` never sees a dangling key) and upserts `containers`
   / `container_versions` / `helm_configs` so `system_type=<code>`
   passes submit validation. Backend `POST /api/v2/systems` round-trips
   `is_builtin`.
2. **`pedestal chart push --tgz`** copies the tgz to
   `/var/lib/rcabench/dataset/charts/` inside
   `aegislab-backend-producer-0`. Alternative: publish remotely and skip
   push — `chart install` resolves source via
   `GET /api/v2/systems/by-name/:name/chart` and accepts
   `--tgz <https://|oci://|file://>` URLs or `--repo/--chart` Helm repo
   form.
3. **`regression run --auto-install`** preflights pedestal pod existence;
   if empty, invokes `pedestal chart install <code>` before submitting.
   Release name = namespace
   (`src/service/consumer/restart_pedestal.go:178`).

---

## 3. Per-system diff table

| Short code | Stack | Entry svc | Safe fault target | `app_label_key` | Loadgen | Chart source | Tracing bridge? |
|------------|-------|-----------|-------------------|-----------------|---------|--------------|-----------------|
| `hs` | Go (DSB hotelReservation) | `frontend` | `geo`, `profile`, `rate`, `reservation`, `search`, `user` | `app` | `dsb-wrk2` | upstream DSB `helm-chart/hotelreservation` (patched WorkingDir) | yes |
| `mm` | C++ (DSB mediaMicroservices) | `nginx-web-server` | `user-service`, `movie-review-service`, `compose-review-service`, `page-service` | `app` | `dsb-wrk2` | upstream DSB `helm-chart/media-microservices` | yes |
| `sn` | C++ (DSB socialNetwork) | `nginx-thrift` | `user-service`, `compose-post-service`, `user-timeline-service` | `app` | `dsb-wrk2` | upstream DSB `helm-chart/socialnetwork` | yes |
| `ss` | Java (Coherence/Helidon sockshop) | `front-end` | `carts`, `catalog`, `orders`, `payment`, `shipping`, `users` | `app` (patched Coherence CR) | upstream loader | LGU-SE-Internal `coherence-helidon-sockshop-sample` fork | no (Prometheus only) |
| `tea` | Java (Descartes TeaStore) | `webui` | `auth`, `image`, `persistence`, `recommender`, `registry` | `app.kubernetes.io/name` | locust | upstream TeaStore helm | yes |

Additional per-system notes in section 6.

---

## 4. Reusable infra: Jaeger→OTLP bridge + dsb-wrk2 + detector graph-warn

Three pieces of infra landed during the DSB work and are now stack-agnostic.
Reuse them whenever a new benchmark in the same family lands — do not
rebuild them per-system.

### 4.1 Jaeger→OTLP bridge

**When to use.** Any stack whose services use OpenTracing-era Jaeger
clients: `jaeger-client-{java,go,cpp,python}`. Confirmed working for DSB
(Go + C++) and TeaStore (Java). The on-the-wire protocol (UDP compact-thrift
on `jaeger:6831`) is identical across all four language SDKs.

**What it is.** Replace the Jaeger deployment with
`otel/opentelemetry-collector-contrib:0.100.0` running a `jaeger`
receiver on the same ports and an OTLP exporter pointed at
`otel-collector.otel.svc.cluster.local:4317` → ClickHouse. Clients keep
their `jaeger:6831` config unchanged.

**How to drop it in.**
- If upstream chart has a `jaeger` subchart (DSB family), override
  `charts/<upstream>/charts/jaeger/templates/{configmap.yaml,deployment.yaml}`
  in the wrapper.
- If not (TeaStore), add a wrapper-level
  `templates/jaeger-bridge.yaml` that provides a Deployment + Service
  named `jaeger` with the same collector config. Works as long as clients
  resolve `JAEGER_AGENT_HOST=jaeger` via DNS.

**Catch for TeaStore-style stacks:** if the chart doesn't set
`JAEGER_AGENT_HOST`, the client defaults to `localhost:6831` and spans go
nowhere. Inject env into each Java-service pod spec (6 statefulsets for
TeaStore).

**Where traces land.** `otel.otel_traces`, NOT `default.otel_traces`. The
database prefix trips people up — wasted debug time in sn and hs.

### 4.2 `dsb-wrk2` loadgen image

**When to use.** Any DSB benchmark. DSB ships `wrk2` Lua scripts but
expects the operator to drive load from outside the cluster — no in-chart
loadgen. Without continuous load during the fault window,
`abnormal_traces.parquet` comes up empty.

**What it is.** `docker.io/opspai/dsb-wrk2:<tag>` — wrk2 binary + DSB Lua
scripts with an entrypoint that loops wrk forever at configurable
threads/conns/rate. Build context: `benchmark-charts/images/dsb-wrk2/`.

**How to drop it in.** Add `templates/loadgen.yaml` (Deployment) to the
wrapper chart, pointing at the entry service. To add scripts for a new
DSB app, drop a `scripts/<app>/` dir, rebuild, push the image with a new
tag (`20260421-media`, `20260421-<next>`, etc.), and reference it from the
wrapper loadgen template.

**`kind load` caveat.** `ctr: content digest ...: not found` on
containerd 2.1 for some multi-arch manifests. Workaround: rely on kind's
internet to pull on demand (`imagePullPolicy: IfNotPresent`).

### 4.3 Detector `opspai/detector:graph-warn-20260421`

**Why.** DSB's jaeger-client-cpp (and most OpenTracing-era Jaeger clients)
only instrument RPC between services — no auto-instrumentation of
MongoDB/Redis/Memcached clients. The trace graph is RPC-only. Prior
detector asserted against an expected service list and threw
`AssertionError: Service 'user-timeline-mongodb' not found in graph`.

**Fix shipped.** `graph-warn-20260421` builds the service graph from spans
actually present and warns instead of asserting on missing nodes. No
per-stack graph assertions needed. Works across DSB + TeaStore + any
future RPC-only-instrumented stack.

---

## 5. Recurring gotchas

### Now handled automatically

- **data.yaml ↔ live cluster drift / DB-before-etcd ordering / etcd
  prefix / `containers.name`=`<code>`** — all covered by
  `system register --from-seed` (atomic, correct prefix, correct order).
  Supersedes #91.5.
- **First-submit empty-namespace 500** — `regression run
  --auto-install` preflights pods and calls `chart install` if needed
  (#91 mitigation).
- **`cd AegisLab/` requirement** (fix 12) — `aegisctl regression run`
  resolves repo-relative paths itself.
- **Chart upload ritual** — `pedestal chart push` replaces the manual
  `kubectl cp … aegislab-backend-producer-0:/var/lib/rcabench/dataset/charts/`.
  Or publish remotely and skip push entirely.

### Still applies

1. **Never pick the entry service as fault target.** Killing the entry
   empties the abnormal window. Regression does not warn today.
2. **Pre-install timing vs backend restart.** If the chart is pushed via
   `pedestal chart push` after the backend has cached the stored tgz,
   `RestartPedestal` may still roll back to the old bytes until the
   producer pod rereads. Prefer URL-based install for churn.
3. **Jaeger→OTLP bridge still required** for DSB-family and TeaStore
   (any OpenTracing-era `jaeger-client-*`). See section 4.1. Traces land
   in `otel.otel_traces`, not `default.otel_traces`.
4. **Release name = namespace.** `installPedestal` uses
   `releaseName = namespace`; honored by `chart install` but still a
   trap if you run raw `helm install` during debugging.
5. **Backend image may predate #17 ca-certificates fix** — remote OCI
   helm install fails with `x509: certificate signed by unknown
   authority`. Fallback: local tgz via `chart push`.
6. **Cluster prerequisites are manual** — `coherence-operator` for
   sockshop, `otel-kube-stack` for trace ingestion. No per-system
   `prerequisites:` reconciler yet.
7. **Datapack metrics validation often fails** on stacks that don't
   emit OTel sum/histogram metrics. Set
   `RCABENCH_OPTIONAL_EMPTY_PARQUETS` on the bench container's
   `container_versions.env_vars`.

---

## 6. Per-system unique notes

Short sub-sections for things that are unique to one system only. Shared
items are covered above.

### `hs` — HotelReservation (DSB Go)

- **WorkingDir trap.** Upstream chart's `command: ./frontend` assumes
  `WorkingDir=/go/bin`, but the published image has
  `WorkingDir=/workspace`. Fix in wrapper `values.yaml`: drop the `./`
  prefix (8 services) so shell resolves via PATH.
- **mountPath trap.** Kubernetes interprets relative
  `mountPath: config.json` as `/config.json`, but the binary reads
  `./config.json` from `/workspace`. Fix: set each subchart's
  `configMaps[0].mountPath: /workspace/config.json`.
- Single multi-service image `deathstarbench/hotel-reservation:latest` —
  no per-service builds needed.

### `mm` — MediaMicroservices (DSB C++)

- `compose-review.lua` hardcodes `username_1..1000` + movie titles.
  Upstream expects pre-run `register_users.sh` + `register_movies.sh`;
  skipping is fine for regression since 4xx/5xx still emit spans.
- First DSB benchmark to land green on first try after bridge + dsb-wrk2
  + graph-warn infra existed. Validates those are stack-agnostic.

### `sn` — SocialNetwork (DSB C++)

- First benchmark to hit the missing-load-during-fault-window problem;
  drove the `dsb-wrk2` image creation.
- First benchmark to hit the detector graph-assumption problem; drove
  `detector:graph-warn-20260421`.

### `ss` — SockShop (Coherence/Helidon Java)

- **Chart + images are now self-hosted in the fork** (same pattern as
  hs/sn/media/teastore): chart at
  `https://lgu-se-internal.github.io/coherence-helidon-sockshop-sample/`
  (repo `sockshop-lgu`, chart `sockshop`, ≥ `1.1.1`); images at
  `docker.io/opspai/ss-{carts,catalog,orders,payment,shipping,users,frontend,loadgen}`
  tagged `YYYYMMDD-<sha>` + `latest`. The previous
  `opspai/sockshop-aegis` wrapper is retired.
- **Coherence pods don't inherit `app` label.** Coherence Operator reads
  `spec.labels` from the CR, not `metadata.labels`. Fix baked into the
  chart since `1.1.1`: each `Coherence` CR template emits `spec.labels`
  with `{app: <name>}`. Without this, aegisctl preflight fails with
  `namespace sockshop0 has no pods matching app=carts`.
- **Prerequisite: `coherence-operator`.** Install with the Shanghai
  mirror overrides:
  `helm repo add coherence https://oracle.github.io/coherence-operator/charts && helm repo update && helm upgrade -i coherence-operator coherence/coherence-operator -n coherence-test --create-namespace --wait --version 3.5.11 --set image.registry=pair-cn-shanghai.cr.volces.com/opspai --set image.name=coherence-operator --set image.tag=3.5.11 --set defaultCoherenceImage.registry=pair-cn-shanghai.cr.volces.com/opspai --set defaultCoherenceImage.name=coherence-ce --set defaultCoherenceImage.tag=14.1.2-0-3`.
  The same values are seeded in `prerequisites.values`, so
  `aegisctl system reconcile-prereqs --name sockshop` applies them
  automatically.
- **No tracing bridge** — Coherence MP emits Prometheus metrics only.
  Needs `RCABENCH_OPTIONAL_EMPTY_PARQUETS` on the bench container.
- **Frontend is Node.js**, vendored into the fork at `frontend/` from
  `YifanYang6/front-end@64dff7d` so the fork builds a single `ss-frontend`
  image in its own CI. Provenance + re-sync in `frontend/PROVENANCE.md`.

### `tea` — TeaStore (Descartes Java)

- **Short code is `tea`, NOT `ts`** (already taken by TrainTicket).
- **`app_label_key = app.kubernetes.io/name`** (not `app`). Upstream
  labels with `app.kubernetes.io/{part-of,name}`.
- **Env injection into 6 statefulsets.** Upstream ships no
  `JAEGER_AGENT_HOST`; jaeger-client-java defaults to `localhost:6831`.
  Inject `JAEGER_AGENT_HOST=jaeger` + `JAEGER_AGENT_PORT=6831` into
  auth, image, persistence, recommender, webui, registry pod specs.
  5 had existing `env:` blocks, registry needed a new one inserted
  before `resources:`.
- Bridge is a wrapper-level template (not a subchart override) because
  TeaStore has no `jaeger` subchart slot.
- Loadgen is `locustio/locust:2.27.0` + ConfigMap-mounted locustfile —
  no custom image needed.
