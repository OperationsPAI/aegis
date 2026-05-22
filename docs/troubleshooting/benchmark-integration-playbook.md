# Benchmark Integration Playbook

Consolidated runbook for onboarding a new microservice benchmark onto
aegis-local. Derived from 5 per-system integration records produced on
2026-04-21 (sockshop, hotelreservation, socialnetwork, mediamicroservices,
teastore). Supersedes the individual `*-integration-record-2026-04-21.md`
files.

Use alongside the `register-aegis-system` skill — this doc captures the
recurring pitfalls, the reusable infra, and per-system deltas.

---

## 0. The binding you must establish: microservice ↔ fault injection points

Onboarding a benchmark is **two registrations**, not one. Both are
keyed by the same short system code (`ts`, `hs`, `tea`, …) and the
inject pipeline silently no-ops if either is missing.

| What you register | What it answers | Where it lives | Symptom when absent |
|---|---|---|---|
| **System identity** — namespace pattern, app-label-key, display name, pedestal chart pointer | "Does aegis know this microservice exists, and how do I find its pods?" | etcd `/rcabench/config/global/injection.system.<code>.*` (7 keys) + DB `containers` / `container_versions` / `helm_configs` | `system_type` rejected at submit; `Chaos system config manager loaded 0 systems`; preflight `namespace X has no pods matching app=Y` |
| **Fault injection points** — the concrete injectable surface (HTTP endpoints, network pairs, DNS endpoints, DB operations, JVM class+method targets, runtime mutator targets) the system *exposes* | "What can I actually inject into?" | DB `chaos_points` table (queried via `platform/k8s/resourcelookup.GetSystemCache(<code>)`) | Guided inject preview returns empty lookups; `lookupHTTPEndpoint` / `lookupNetworkPair` etc. fall back to whatever the user typed; campaign generators have no menu to pick from |

The two are bound by **short system code**. Drift between them — e.g.
points imported under `media` but the system registered as `mm` — makes
the points invisible. Pick the code once, use it everywhere.

**Authoring fault injection points.** Points are described by a
**PointManifest** YAML (see ADR-0008/0009/0010, schema at
`aegislab/src/cli/cmd/manifest_schema.json`, sample at
`aegislab/src/cli/cmd/testdata/manifest-valid.yaml`).

- **Production delivery is chart-bound** (ADR-0009). Every benchmark
  chart ships a `post-install` / `post-upgrade` Job that POSTs its
  PointManifest to `aegis-chaos /v1beta/systems/<code>/points/import`
  and a `post-delete` Job that retires the service version. Job
  failure = chart install failure, so a broken manifest cannot
  silently ship a stale catalog. Templated against `Chart.Version`,
  so chart version ↔ point manifest version stays bound.
- **Manual import** for dev/debug (no chart, or chart Job failed):
  `aegisctl manifest import <file>` (top-level, not under `chaos`) validates against the
  bundled schema then POSTs to the same endpoint.
- **Offline validation** on every chart PR that touches a manifest:
  `aegisctl manifest validate <file>` runs the JSON Schema
  check without contacting the cluster.

> All three commands require `--chaos-server <url>` (or env
> `AEGIS_CHAOS_SERVER`) because they hit aegis-chaos, not aegis-api.
> Inside a helm post-install hook this is
> `http://{{ include "chaos.fullname" . }}.{{ .Release.Namespace }}.svc:{{ .Values.httpPort }}`.
> From a laptop, port-forward the in-cluster Service:
> `kubectl -n exp port-forward svc/rcabench-chaos 8086:8086`.

**Validated end-to-end 2026-05-22** on byte-cluster: imported a
`pod_failure` point on `hs/geo` (point id `988f70a26864863b`), submitted
`aegisctl chaos inject submit --point-id ... --namespace hs0`,
chaos-mesh applied the PodChaos at `t+0s`, `geo` pod entered
`RunContainerError`, recovered at `t+30s`, injection terminal=`succeeded`,
`AllRecovered=True` in the diagnostics.

**Webhook → BuildDatapack caveat (byte-cluster deployment drift,
not a code bug).** The chaos→aegis-backend webhook returned
401 `token has invalid claims: token is expired` in this run, so
`BuildDatapack` never fired. Diagnosis: the deployed chaos image
`byte-20260520-step5b-r5full` predates commit
`79335448 feat(auth): wire SA tokens end-to-end on chaos webhook hop`
(verified by `strings /app/aegis-chaos | grep CHAOS_SA_TOKEN` →
no match; only `CHAOS_WEBHOOK_BEARER` is present). Even though the
Helm chart now wires `CHAOS_SA_TOKEN` from Secret `rcabench-chaos-sa`
(a year-valid `rcabench-sa`-issued SA JWT with scope
`chaos.webhook.write`), the running binary doesn't read that env;
it falls back to the deprecated `CHAOS_WEBHOOK_BEARER` static admin
SSO JWT, which expires routinely. **Fix: rebuild + push the chaos
image from current main and `kubectl set image` on the chaos
Deployment.** Once the new binary is in, SA path takes over and the
webhook stops aging out.

**Verification quick path after importing a new PointManifest.**

The point catalog is one thing; "the chaos-mesh CR actually fires and lands
on the right pods" is another. Verify the second separately from any
datapack/algorithm run:

1. Confirm the point is in the catalog:

   ```bash
   aegisctl chaos points list --system <code> --service <svc> --chaos-server <url>
   ```

2. Direct-inject to verify chaos-mesh actually applies the CR:

   ```bash
   aegisctl chaos inject submit \
     --point-id <id> --namespace <ns> \
     --params '{...}' --idempotency-key "$(uuidgen)" \
     --chaos-server <url>
   ```

   This is the **verification-only path** (see `aegislab/docs/aegis-chaos-design.md`
   §5.1 path comparison). It lights up chaos-mesh, you can watch the target
   pods react, the injection lifecycle terminates, and you confirm the point
   is wired correctly. It does NOT run the datapack/algorithm chain, and it
   bypasses the resourcelookup cache entirely — so it's the unaffected
   escape hatch while OperationsPAI/aegis#459 (cache freshness for guided)
   is open.

3. For full experiments (datapack + algorithm + collect):

   ```bash
   aegisctl inject guided --apply ...     # interactive/scripted run
   aegisctl regression run <case-name>    # repo-tracked smoke case
   ```

**Inspecting what's bound right now.**

```bash
aegisctl chaos points list --system <code> --chaos-server <url>     # what's active (server-side)
aegisctl chaos points export <out-dir> --system <code> --chaos-server <url>   # dump back to YAML
aegisctl manifest list-points --system <code> --chaos-server <url>   # alternative, paginated
```

If `points list` is empty but the system is otherwise registered, the
PointManifest was never imported (or the post-install Job failed) —
the system exists but has no injectable surface yet.


---

## 1. Overview — the 5-layer mental model

Every benchmark integration touches the same five layers. If the pipeline
fails, first identify which layer is misconfigured; the failure symptoms
are near-unique per layer.

| # | Layer | What it holds | Owner | Failure symptom if skipped |
|---|-------|---------------|-------|----------------------------|
| 1 | Compiled registry (`chaos-experiment/internal/systemconfig`) | Short system code (`ts`, `ob`, `hs`, `sn`, `mm`, `ss`, `tea`), `NsPattern` prefix | Go constant | n/a for runtime-registered systems |
| 2 | etcd dynamic_configs (`/rcabench/config/global/injection.system.<code>.*`) | 7 per-system **identity** keys (count, ns_pattern, extract_pattern, display_name, app_label_key, is_builtin, status) — see section 0 | `etcdctl put` | `Removed runtime-only system registration: X` after backend restart |
| 3 | DB fixtures (`containers`, `container_versions`, `helm_configs`, `dynamic_configs`, **`chaos_points`**) | Pedestal row, chart pointer, defaults — **plus the system's PointManifest entries** (section 0) | MySQL INSERTs or `aegisctl chaos manifest import` | Submit rejects `system_type`, or `record not found` in listener; empty `chaos_points` ⇒ guided inject menus are empty |
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
`aegislab/data/initial_data/{prod,staging}/data.yaml`;
`helm package` → `<code>-aegis-<ver>.tgz`; add regression case
`aegislab/regression/<code>-guided.yaml`.

**Runtime wiring (3 commands):**

```bash
# 1. Seed etcd + dynamic_configs atomically from data.yaml (system identity).
aegisctl system register --from-seed aegislab/data/initial_data/prod/data.yaml --name <code>

# 2. Publish the chart (skip if published remotely; step 3 will fetch).
aegisctl pedestal chart push --name <code> --tgz ./<code>-aegis-<ver>.tgz

# 3. Preflight + submit. --auto-install runs chart install if ns is empty.
#    The chart's post-install Job imports the PointManifest (ADR-0009).
aegisctl regression run <code>-guided --auto-install
```

Verify the binding before injecting: `aegisctl chaos points list
--system <code>` must return a non-empty set. An empty list means the
chart's post-install Job didn't run (or failed) — guided inject will
have no menu to offer. Re-run the chart with
`aegisctl pedestal chart install <code>` and check Job logs, or import
the manifest manually with `aegisctl manifest import <file>` (top-level, not under `chaos`) to
unblock local work.

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
- **`cd aegislab/` requirement** (fix 12) — `aegisctl regression run`
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
5b. **Bitnami-subchart toggles need ad-hoc helm overrides.** Charts
    that depend on `bitnami/*` subcharts (e.g. `trainticket` →
    `rabbitmq`) abort unless
    `global.security.allowInsecureImages=true`. As of 2026-05-22
    `aegisctl pedestal chart install` supports
    `--set k=v` / `--set-string k=v` (repeatable; applied AFTER
    --apply-overrides / value file, so they win); use them for
    upstream-chart toggles that aren't seed-managed. Validated by
    bringing up `ts0` on byte-cluster:
    `aegisctl pedestal chart install ts --apply-overrides --set global.security.allowInsecureImages=true`.
6. **Cluster prerequisites are manual** — `coherence-operator` for
   sockshop, `otel-kube-stack` for trace ingestion. No per-system
   `prerequisites:` reconciler yet.
7. **Datapack metrics validation often fails** on stacks that don't
   emit OTel sum/histogram metrics. BuildDatapack is fail-fast by
   design — there is no production bypass. If a required parquet
   comes back empty, run `aegisctl datapack diagnose --injection
   <id>` to see which file and why; almost always the inject hit a
   service with no traffic. Fix the targeting, or pre-screen with
   `aegisctl reason filter-clean`. The CLI flag `python
   cli/prepare_inputs.py run --allow-empty` exists for local /
   offline investigation only and is deliberately NOT wired into the
   in-cluster Job entrypoint.

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
  This means trace-derived parquets will be empty, and BuildDatapack
  will fail-fast (intentionally). Production runs against sockshop
  require fixing the trace path; for one-off local investigation,
  invoke `python cli/prepare_inputs.py run --allow-empty`.
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
