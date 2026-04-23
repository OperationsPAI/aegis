---
name: register-aegis-system
description: >-
  Methodology for making the aegis control plane aware of a newly added
  microservice benchmark - what layers of the platform (systemconfig
  registry, etcd dynamic config, DB seed, helm chart config, OTel pipeline)
  must be told about the new system, and what fails silently if any layer is
  skipped. Use whenever the user wants to "add a new benchmark", "register a
  new system", "support X workload in aegis", "做一个新的 benchmark",
  "加一个新的微服务", or is debugging why a freshly-deployed workload isn't
  selectable in the guided inject flow, returns 500 on submit, or goes to `0
  enabled` after backend restart. Complements `onboard-benchmark-system`
  (which covers deploying the workload and wiring OTLP); this skill covers the
  aegis-side registration that most new-benchmark failures trace back to.
---

# Registering a new system with the aegis control plane

The workload (pods, charts, OTel collectors) is only half of adding a new
benchmark. The other half is telling the aegis **control plane** it exists.
The control plane state lives in five separate places, each one silently
failing closed if it's missing. The symptoms look unrelated but almost
always come back to one of these five layers being under-populated.

`aegisctl` now covers layers 2 and 4 as one-liners; layers 1, 3, 5 still
need per-system work. Use this skill to map symptoms to layers and pick
the right aegisctl command (or the raw-command fallback when aegisctl
is unavailable). References hold concrete schemas + fallback recipes:

- `references/registry.md` — Layer 1 compiled systemconfig registry
- `references/etcd.md`     — Layer 2 etcd + dynamic_configs (now `aegisctl system`)
- `references/db.md`       — Layer 3 containers / helm_configs schemas
- `references/chart.md`    — Layer 4 wrapper chart (now `aegisctl pedestal chart`)
- `references/otel.md`     — Layer 5 collector + jaeger→OTLP bridge

## The five layers and what fails if you skip each

Think of adding a new system as: the compiled registry knows it exists,
the runtime config enables it, the DB describes it, the helm layer
builds it, the observability pipeline tags it.

### Layer 1 — Compiled registry (`chaos-experiment/internal/systemconfig`)

Holds the *shape* of every supported system as Go constants (`NsPattern`,
`DisplayName`, `AppLabelKey`, short `SystemType` code). `pkg/guidedcli`
reads it to list systems, validate `--system`, and resolve namespaces.

**Skip it and:** the guided flow can't list the system; `--system foo0`
is rejected before anything else runs. Per-system metadata (endpoints,
JVM methods, DB ops) also lives under `internal/<system>/`; missing it
blocks "intelligent" chaos types (HTTP-route, JVM-class) but not coarse
pod-failure or selector-based chaos.

Details: `references/registry.md` (compiled vs etcd-only trade-off).

### Layer 2 — Runtime config in etcd (`injection.system.<name>.*`)

Since PR #90, etcd is the single source of truth for which systems are
enabled at runtime. Seven keys per system; on boot,
`InitializeSystems` unregisters everything not in etcd's enabled set
and re-registers whatever is there. The compiled registry from Layer 1
is a template overridden by etcd.

**Skip it (or ship only some keys) and:** `IsEnabled()` returns false,
`InitializeSystems` removes the runtime registration, logs show
`loaded N systems (0 enabled)` and `Removed runtime-only system
registration: <name>`. Submit fails with
`system "<ns>" does not match any registered namespace pattern`. This
is the single most common first-time failure.

**What to run:**

```bash
aegisctl system register --from-seed AegisLab/data/initial_data \
                         --env prod --name <code>
aegisctl system list          # confirm enabled + is_builtin
```

One call writes all seven etcd keys + seven `dynamic_configs` rows
atomically via POST /api/v2/systems, replacing the old "INSERT then
etcdctl put" dance. `aegisctl system unregister --name <code>` is the
symmetric cleanup. Manual `etcdctl`/SQL fallback and all of the
ordering/scope-prefix traps it guarded against live in
`references/etcd.md`.

### Layer 3 — Database fixtures (`containers`, `container_versions`, `helm_configs`)

Tells the aegis job pipeline which pedestal installs the workload and
which image builds the datapack. Three fixture row classes: a
`containers type=2` (pedestal) row, a `containers type=1` (benchmark)
row, a `container_versions` row per container, and a `helm_configs` row
for the pedestal.

**Skip it and:** submit may pass guided validation but the pipeline
fails stage by stage with different errors — `RestartPedestal` can't
find the chart, `BuildDatapack`'s K8s job can't find an image, empty
`command` fields yield `runc exec: "": executable file not found`.

`aegisctl system register` does **not** touch this layer; it's still
SQL-seeded (or `aegisctl pedestal helm set` for the single helm_configs
row). The pedestal-name constraint below is *caller* responsibility.

**Critical constraint — pedestal `containers.name` = short system code
from Layer 1.** The submit validator checks `pedestal.name ==
system_type`, where `system_type` is the short Go constant defined in
`chaos-experiment/internal/systemconfig`. Current short codes:
`ts`, `otel-demo`, `ob`, `sockshop`, `hs`, `sn`, `media`, `teastore`.
Full-form names (`hotelreservation`, `mediamicroservices`, `teastore`
vs `tea`, etc.) are NOT accepted — seed fails with
`invalid pedestal name: X` and the whole `initializeProducer`
transaction rolls back, leaving the DB half-populated. Frequently
wrong in older data.yaml snapshots: `hotelreservation→hs`,
`mm→media`, `tea→teastore`.

**Note on `ob`**: its 7 `injection.system.ob.*` etcd keys are NOT in
`data/initial_data/prod/data.yaml` (only `ts`, `otel-demo`, `sockshop`,
`hs`, `sn`, `media`, `teastore`). Onboarding ob requires adding those
keys to the seed or POSTing via `aegisctl system register --force`
with a hand-crafted seed. Running `aegisctl regression run
ob-guided` against a stock install will return an empty
`aegisctl system list` for `ob`.

Details (schemas, `type=1` vs `type=2` name collision, seed SQL):
`references/db.md`.

### Layer 4 — Helm values + ephemeral caches

The pedestal helm install runs inside the producer pod. The chart tgz
(if `local_path` is set) lives at `/tmp/<chart>.tgz`; values.yaml lives
at `/var/lib/rcabench/dataset/helm-values/`.

**Skip it and:** every backend `rollout restart` wipes `/tmp`, so a
pipeline assuming the local tgz exists fails with
`failed to locate chart /tmp/<chart>.tgz: path ... not found` on the
next inject. Upstream values that bake in dev-invalid DNS won't
survive a `helm upgrade`; `kubectl set env` patches are not durable.
Single-image Go stacks crash-loop with `exec: "./frontend": no such
file or directory` when `WorkingDir` ≠ binaries dir.

**What to run:**

```bash
# re-seed the tgz into the producer pod after a restart wiped /tmp
aegisctl pedestal chart push --name <code> --tgz ./<chart>.tgz

# pre-install a chart once (works around first-submit namespace race)
aegisctl pedestal chart install <code> --tgz ./<chart>.tgz --wait
aegisctl pedestal chart install <code> --repo <url> --chart <name> --version v
# with no explicit source, falls back to GET /api/v2/systems/by-name/:name/chart
aegisctl pedestal chart install <code>
```

`install` auto-derives the namespace from `ns_pattern` and uses it as
the helm release name (matches what aegis produces internally — see
`references/chart.md` for why that matters). `regression run
--auto-install` does the same thing inline.

Durable values-file edits, WorkingDir/PATH traps, and operator
prerequisites aegisctl can't fix are still in `references/chart.md`.

### Layer 5 — Observability pipeline

The OTel collector must run `k8sattributes` with pod + namespace RBAC
**and** enrich every signal with both `k8s.namespace.name` (drives pod
association) and `service.namespace` (what BuildDatapack filters on:
`WHERE ResourceAttributes['service.namespace'] = '<ns>'`).

**Skip it and:** the trace completes, the job "succeeds", but every
parquet is empty. `datapack.no_anomaly` or `Parquet file has no data
rows` downstream. A traces-only collector config is the most common
misconfiguration — traces, metrics, logs must *all* export to
ClickHouse. Writing to the `default` database instead of `otel` also
silently yields empty parquets.

Four of six current benchmarks use the **Jaeger→OTLP bridge** pattern
(DSB Go/C++ and Java stacks all ship Jaeger clients, not OTLP).
aegisctl does not manage collector configs — this layer is still YAML.

Details: `references/otel.md`.

## How to think about a new-system request

1. Is the Go code registered? (Layer 1 — one-time compile-in.)
2. Run `aegisctl system register --from-seed ... --name <code>`; verify
   via `aegisctl system list` that status=1 and is_builtin is right.
   (Layer 2.)
3. Are `containers` + `container_versions` + `helm_configs` rows
   present, pedestal name = short code, commands non-empty? (Layer 3 —
   still SQL.)
4. Chart reachable — `aegisctl pedestal chart push` if using local tgz,
   or ensure `helm_configs.repo_url` / etcd override is set. Values
   file avoids dev-env DNS. (Layer 4.)
5. Collector tags namespace on all three signals and exports to
   ClickHouse `otel` DB. (Layer 5.)

Failure logs almost always point at the layer; the fix is rarely "more
code", it's "fill in what the seed forgot" — and for layers 2/4 that's
an aegisctl command, not a manual procedure.

## Decision tree — which layer broke?

**"System doesn't show up in guided flow"**
→ Layer 1 (unregistered) or Layer 2 (all-disabled). Run `aegisctl
system list`; if the system is absent or `enabled=false`, run
`aegisctl system register ...`. If it's not in the seed at all, it's
Layer 1. See `references/registry.md`, `references/etcd.md`.

**"Submit returns 500 with `system does not match`"**
→ Layer 2. `aegisctl system list` first; a missing entry or `status=0`
is the cause. `aegisctl system register --name <code>` fixes it. See
`references/etcd.md`.

**"data.yaml seed is correct, backend still says `loaded N systems
(0 enabled)` on a fresh cluster"**
→ Layer 2 etcd-publishing gap — `initializeDynamicConfigs` writes DB
rows but not etcd. `aegisctl system register --from-seed ... --name
<code>` publishes both in one shot. (Historic workaround was a manual
etcdctl pass; see `references/etcd.md` fallback.)

**"Submit returns `mismatched system type X for pedestal Y`"**
→ Layer 3 pedestal-name constraint. `containers.name` for the pedestal
must equal the short `SystemType` code. aegisctl does not enforce this
— it's a pure DB-seed concern. See `references/db.md`.

**"Submit succeeds, pipeline fails at RestartPedestal"**
→ Layer 3 (`helm_configs` missing/wrong — use `aegisctl pedestal helm
get/set`) or Layer 4 (chart gone — `aegisctl pedestal chart push` or
`install`). See `references/db.md` and `references/chart.md`.

**"Submit returns `available apps: <only a few services>`"**
→ Layer 5 operator-managed workloads don't propagate CR metadata
labels. Patch the CR's pod-facing labels field; don't change
`app_label_key`. See `references/otel.md`.

**"Pipeline fully completes but `datapack.no_anomaly` / empty parquets"**
→ Layer 5 (collector attrs missing) or chaos window shorter than the
algorithm's required sample window. See `references/otel.md`.

**"It worked yesterday, now fails after backend restart"**
→ Layer 4 (`/tmp` wiped — re-run `aegisctl pedestal chart push`) or
Layer 2 (new keys in code not yet in etcd — `aegisctl system register
--force`). See `references/chart.md`, `references/etcd.md`.

**"Fresh DB says `(0 enabled)` but dynamic_configs is non-empty; users
table is empty yet seed log says `already seeded, skipping`"**
→ Layer 2 seed-idempotency race. Producer + consumer AutoMigrate
concurrently on a fresh DB; consumer tx "succeeds" but rows land in
tables the migrator then drops. The `newConfigDataWithDB` guard then
sees non-empty dynamic_configs and skips user/container seeding on the
next boot. Recover with a forced re-seed:
`kubectl scale deploy -n exp rcabench-api-gateway --replicas=0 &&
kubectl wait --for=delete pod -n exp -l app=rcabench-api-gateway &&
kubectl exec -n exp rcabench-mysql-0 -- env MYSQL_PWD=yourpassword
mysql -uroot -e 'DROP DATABASE rcabench; CREATE DATABASE rcabench;' &&
kubectl scale deploy -n exp rcabench-api-gateway --replicas=1`.
Scaling down first matters — a still-running pod will race the drop
and re-publish stale rows.

**"`aegisctl system list` still shows old names (e.g. `mm`/`tea`) after
a seed rename"**
→ Layer 2 stale etcd. `publishSeededConfigsToEtcd` is additive: it
writes new `injection.system.<new>.*` keys but never deletes old
`<old>.*`. Clean with
`kubectl exec -n exp rcabench-etcd-0 -- etcdctl --endpoints=http://localhost:2379
del --prefix /rcabench/config/global/injection.system.<old>.`
then restart `rcabench-api-gateway`.

**"First-install chart install fails with `repository not valid, no
index.yaml` against `oci://registry-1.docker.io/opspai`"**
→ Layer 4 / aegisctl-side OCI handling. OCI repos expose no
index.yaml; the install must pass
`oci://<repo>/<chart>` as the positional chart arg (no `--repo`).
Already handled in `aegisctl pedestal chart install` and in the
backend's `restart_pedestal.go` after the kind-cold-start fixes —
re-build if on an older binary.

**"Chart install fails with `missing registry client`"**
→ Layer 4, Helm actionConfig needs `registry.NewClient(...)` attached.
Fixed in `infra/helm/gateway.go` by the kind-cold-start PR.

**"BuildDatapack job env vars only include NAMESPACE; seeded env vars
(e.g. RCABENCH_OPTIONAL_EMPTY_PARQUETS) aren't propagated"**
→ Layer 3 / consumer glue. `HandleCRDSucceeded` used to hardcode the
benchmark env list to just NAMESPACE. Fixed by reading
`container_version_env_vars` and merging. Also note: env var rows must
be linked to the BENCHMARK (`clickhouse`) container_version, not the
pedestal (`ob-bench` etc.) — the regression case's
`benchmark.name=clickhouse` is what the job reads.

**"RestartPedestal hangs forever / helm install in `pending-install`"**
→ A rendered resource can never become Ready. `installAction.Wait=true`
+ one crashlooping Deployment (GCP-bound collector without metadata
server) or a `LoadBalancer` Service (no LB provider in kind) blocks the
whole install. Post-install hooks do NOT fire under deadlock — patch
the chart, don't use hook Jobs. See `references/chart.md`.

**"Pipeline runs green but `abnormal_logs.parquet` empty"**
→ System emits only traces over OTLP, AND cluster `otel-collector` has
no `filelog`/`prometheus` receivers. Deploy the kube-stack; config at
`AegisLab/manifests/otel-collector/otel-kube-stack.kind.yaml`.

**"abnormal_logs OK but abnormal_traces empty"**
→ Apps point OTLP at the legacy `otel-collector` Service, but the
kube-stack creates new collector pods with different labels. Patch the
Service selector — see `references/otel.md`.

**"abnormal_trace_id_ts empty but otel_traces has rows"**
→ Traces land with `k8s.namespace.name` but `service.namespace` blank.
BuildDatapack filters on `service.namespace` only. Transform in
`references/otel.md`.

**"abnormal_metrics_histogram empty — ob/sockshop"**
→ Those stacks don't emit histograms (OpenCensus / Prometheus-only).
Set `RCABENCH_OPTIONAL_EMPTY_PARQUETS` on the benchmark container. See
`references/otel.md`.

**"ServiceName blank in otel_logs for an app without OTel SDK"**
→ k8sattributes `service.name` extraction needs
`app.kubernetes.io/name`. Add label-mapping rules covering `app`,
`name`, etc. See `references/otel.md`.

**"Submit 500s with `namespaces "<ns>" not found`"**
→ First-use chicken-and-egg: submit's groundtruth resolution runs
before RestartPedestal creates the namespace. Fix: `aegisctl pedestal
chart install <code>` once to pre-create the namespace with the
release-name convention. (Issue #91 — now closed by the install
subcommand.)

## What aegisctl does and doesn't cover

Covered as one-liners:
- Layer 2: `aegisctl system register / list / unregister`.
- Layer 4 distribution: `aegisctl pedestal chart push / install`,
  `aegisctl regression run --auto-install`.

Still manual:
- Layer 1 (Go source change).
- Layer 3 DB seed (containers + container_versions rows — only
  `helm_configs` has an aegisctl verb).
- Layer 5 collector YAML.
- Pedestal name = short-code invariant: aegisctl does not cross-check.

## Related

- `.claude/skills/onboard-benchmark-system/` — workload-side half.
- `memory/aegislab_e2e_pitfalls.md` — terse pitfalls list.
- Issue #91 — aegisctl control-plane tooling (mostly landed).
- Issue #92 — `benchmark-charts` umbrella + wrapper-chart design.
- Issues #93–#98 — per-system wrapper chart sub-issues.
