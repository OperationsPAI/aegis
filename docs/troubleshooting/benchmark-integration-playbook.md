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

This is the common path all 5 benchmarks followed. Use it as a checklist.

1. **Pick the short code** (2–5 chars). Must not collide with existing
   compiled registry (e.g. `ts`=TrainTicket, so TeaStore uses `tea`).
2. **Fork / locate the upstream chart.** Put a wrapper under
   `benchmark-charts/charts/<code>-aegis/` that embeds the upstream chart
   as a subchart and layers aegis-specific patches on top.
3. **Patch the chart for aegis assumptions:**
   - `app` label propagation (aegis's guided resolver selects pods by
     `app_label_key`). Each pod must carry the label value the regression
     YAML will reference.
   - Entry-service and mid-tier services all need deterministic labels.
4. **Build or reuse images.** Several benchmarks ship single-binary
   images (DSB hotel-reservation is one image with all Go services in
   `/go/bin`). Others need per-service builds (sockshop Coherence/Helidon,
   built via Jib).
5. **Wire up telemetry.** If the stack uses OpenTracing-era Jaeger
   clients (DSB family + TeaStore), drop in the Jaeger→OTLP bridge
   (section 4). If it uses OTel SDK natively, just point its exporter at
   `otel-collector.otel.svc.cluster.local:4317`.
6. **Add a loadgen** (`templates/loadgen.yaml` in the wrapper chart).
   `dsb-wrk2` for DSB family, locust for TeaStore, whatever upstream
   ships for others. Always pick a mid-tier fault target — never the
   entry service.
7. **Package + upload chart.**
   ```
   helm package benchmark-charts/charts/<code>-aegis
   kubectl cp <code>-aegis-<ver>.tgz aegislab-backend-producer-0:/var/lib/rcabench/dataset/charts/
   ```
   Also optionally push OCI (`helm push ... oci://registry-1.docker.io/opspai/...`).
8. **Seed data.yaml** entries under
   `AegisLab/data/initial_data/{prod,staging}/data.yaml` — pedestal
   entry + 7 `injection.system.<code>.*` dynamic_configs.
9. **Manually insert DB + etcd rows on the running cluster** (the
   #91.5 auto-seed gap — see gotcha below). Order: DB first, etcd second,
   else the `config_listener` rejects with `record not found`.
10. **Pre-install the chart once** before the first regression
    (`helm install <code>0 ... -n <code>0`). Release name must equal the
    namespace — aegis's `installPedestal` uses `releaseName = namespace`
    (`src/service/consumer/restart_pedestal.go:178`).
11. **Add regression case** `AegisLab/regression/<code>-guided.yaml`.
12. **Run** `aegisctl regression submit …` and trace through
    RestartPedestal → FaultInjection → BuildDatapack → RunAlgorithm →
    CollectResult.

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

## 5. Recurring gotchas (deduped across all 5 integrations)

1. **Editing `data.yaml` does not populate `dynamic_configs` on a running
   cluster.** `initializeDynamicConfigs` (consumer.go:118) only runs on
   fresh seed. You must manually INSERT the 7 rows + `etcdctl put` the
   7 keys. Tracked as #91.5.
2. **Order matters: DB rows first, etcd put second.** If you put etcd
   before the DB row exists, `config_listener` rejects with
   `record not found` and the runtime registry never picks up the system.
3. **etcd keys live under `/rcabench/config/global/` prefix.** Writing at
   root silently drops to "Removed runtime-only system registration".
4. **Pedestal container `name` must equal the short system code.** Not
   the display name. `UPDATE containers SET name='hs' WHERE name='hotelreservation'`.
   Submit validator checks `container_name == system_type`.
5. **Pre-install release name must equal the namespace.** aegis's
   `installPedestal` uses `releaseName = namespace`. Mismatched release
   names produce different pod-app-label suffixes.
6. **First-submit guided resolution runs before RestartPedestal.** The
   resolver lists pods in the target namespace at submit time, so an
   empty ns fails with misleading `system X does not match any registered
   namespace pattern` (actual cause: zero-app-set). Workaround: manual
   `helm install` once. Tracked as #91.
7. **Chart edits require `helm package` + `kubectl cp` into the backend
   producer pod before next rerun**, or `RestartPedestal` silently rolls
   back to the stored tgz. No automated upload yet.
8. **Traces land in `otel.otel_traces`, not `default.otel_traces`.**
9. **Never pick the entry service as fault target.** Killing the entry
   stops all traffic, emptying the abnormal window. Regression should
   warn or reject but does not today.
10. **Backend image may predate #17 ca-certificates fix.** Remote OCI
    helm install fails with `x509: certificate signed by unknown
    authority`. Fallback: `local_path` tgz in `helm_configs`.
11. **Cluster prerequisites are manual.** e.g. `coherence-operator` for
    sockshop, `otel-kube-stack` for trace ingestion. No per-system
    `prerequisites:` reconciler yet.
12. **Datapack metrics validation often fails** on stacks that don't
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

- **Coherence pods don't inherit `app` label.** Coherence Operator reads
  `spec.labels` from the CR, not `metadata.labels`. Fix: patch each
  `Coherence` CR template in the wrapper to also include `spec.labels`
  with `{app: <name>}`. Bumped wrapper 0.1.0 → 0.1.1.
- **Prerequisite: `coherence-operator`.** `helm repo add coherence …`
  + `helm install operator coherence/coherence-operator` by hand before
  installing sockshop-aegis.
- **No tracing bridge** — Coherence MP emits Prometheus metrics only.
  Needs `RCABENCH_OPTIONAL_EMPTY_PARQUETS` on the bench container.
- Per-service Jib builds: `opspai/ss-{carts,catalog,orders,payment,
  shipping,users}:2.12.3` + `opspai/ss-frontend:propagator`.

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
