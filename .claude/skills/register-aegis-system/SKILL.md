---
name: register-aegis-system
description: Methodology for making the aegis control plane aware of a newly added microservice benchmark — what layers of the platform (systemconfig registry, etcd dynamic config, DB seed, helm chart config, OTel pipeline) must be told about the new system, and what fails silently if any layer is skipped. Use whenever the user wants to "add a new benchmark", "register a new system", "support X workload in aegis", "做一个新的 benchmark", "加一个新的微服务", or is debugging why a freshly-deployed workload isn't selectable in the guided inject flow, returns 500 on submit, or goes to `0 enabled` after backend restart. Complements `onboard-benchmark-system` (which covers deploying the workload and wiring OTLP); this skill covers the aegis-side registration that most new-benchmark failures trace back to.
---

# Registering a new system with the aegis control plane

The workload (pods, charts, OTel collectors) is only half of adding a new
benchmark. The other half is telling the aegis **control plane** it exists.
The control plane state lives in five separate places, each one silently
failing closed if it's missing. The symptoms look unrelated but almost
always come back to one of these five layers being under-populated.

Use this skill to build a mental model of what to wire up and what goes
wrong if you miss a layer. This document is methodology only. Concrete
commands, schemas, and reconciliation recipes live in `references/`:

- `references/registry.md` — Layer 1 compiled systemconfig registry
- `references/etcd.md`     — Layer 2 etcd keys, dynamic_configs, value_type
- `references/db.md`       — Layer 3 containers / helm_configs schemas
- `references/chart.md`    — Layer 4 wrapper chart, values, /tmp gotcha
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

Details (seven-key layout, dynamic_configs rows, value_type enum,
three-vs-seven-key trap, `/rcabench/config/<scope>/` prefix, DB-row-
before-etcd-put ordering, data.yaml → etcd one-way gap, reconcile
`etcdctl` commands): `references/etcd.md`.

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

**Critical constraint — pedestal `containers.name` = short system code
from Layer 1.** The submit validator checks `pedestal.name ==
system_type`, where `system_type` is the short Go constant (`ts`, `ob`,
`hs`, `sn`, `media`, `tea`, …), *not* the display-facing name. Seeding
a pedestal row as `hotelreservation` while the registry uses `hs`
produces `mismatched system type hs for pedestal hotelreservation`.
This is the constraint the hotelreservation integration hit.

Details (schemas, `type=1` vs `type=2` name collision, seed SQL):
`references/db.md`.

### Layer 4 — Helm values + ephemeral caches

The pedestal helm install runs inside the producer pod. The chart tgz
(if `local_path` is set) lives at `/tmp/<chart>.tgz`; values.yaml lives
at `/var/lib/rcabench/dataset/helm-values/`.

**Skip it and:** every backend `rollout restart` wipes `/tmp`, so a
pipeline assuming the local tgz exists fails with
`failed to locate chart /tmp/<chart>.tgz: path ... not found` on the
next inject. Upstream values that bake in dev-invalid DNS (e.g.
`opentelemetry-kube-stack-deployment-collector.monitoring`) won't
survive a `helm upgrade`; `kubectl set env` patches are not durable.
Single-image Go stacks crash-loop with `exec: "./frontend": no such
file or directory` when `WorkingDir` ≠ binaries dir.

Two mitigations coexist: (a) `restart_pedestal.go` `os.Stat`s
`LocalPath` and falls through to remote install when absent, and (b)
etcd key `helm.repo.<repo_name>.url` supplies a URL when
`helm_configs.repo_url` is empty.

Details (wrapper chart layout, values conventions, `kubectl cp`
commands, WorkingDir/PATH trap, pre-install release-name rule, operator
prerequisites): `references/chart.md`.

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
(DSB Go/C++ and Java stacks all ship Jaeger clients, not OTLP). The
bridge is a generic collector with a Jaeger receiver alongside OTLP;
no benchmark code changes needed.

Details (three-pipeline collector config, `service.name` derivation for
non-SDK apps, `service.namespace` backfill transform, filelog gotchas,
jaeger→OTLP bridge, operator-managed pod labels, optional-parquets
env): `references/otel.md`.

## How to think about a new-system request

When the user says "I want to add X", walk the five layers mentally
before touching anything:

1. Is the Go code registered? (Layer 1 — one-time compile-in.)
2. Are all seven etcd keys + their `dynamic_configs` rows present and
   does `status` read 1 after restart? (Layer 2 — silent fail class 1.)
3. Are the three DB fixture rows present with matching types, the
   pedestal name equal to the short system code, and non-empty
   commands? (Layer 3 — silent fail class 2.)
4. Is the chart findable — local tgz, configured repo URL, or etcd
   override? Does the values file avoid baking in dev-env DNS?
   (Layer 4 — restart fragility.)
5. Does the OTel collector tag namespace on all three signal types and
   export all three to ClickHouse? (Layer 5 — silent fail class 3.)

If a new benchmark is failing, the log message usually points at the
layer; the fix is almost never "more code", it's "fill in the row in
layer N that the seed forgot".

## Decision tree — which layer broke?

Match the symptom to the layer, then open the corresponding reference:

**"System doesn't show up in guided flow"**
→ Layer 1 (unregistered) or Layer 2 (all-disabled because status keys
missing). Check startup log's enabled count first.
See `references/registry.md`, `references/etcd.md`.

**"Submit returns 500 with `system does not match`"**
→ Layer 2. Count enabled systems in startup logs before debugging
anything else. See `references/etcd.md`.

**"data.yaml seed is correct, backend still says `loaded N systems
(0 enabled)` on a fresh cluster"**
→ Layer 2 etcd-publishing gap: `initializeDynamicConfigs` seeds DB
metadata rows only; first-boot needs a manual etcdctl pass. See
`references/etcd.md` "data.yaml to etcd is one-way and incomplete".

**"Submit returns `mismatched system type X for pedestal Y`"**
→ Layer 3 pedestal-name constraint. `containers.name` for the pedestal
must equal the short `SystemType` code. See `references/db.md`.

**"Submit succeeds, pipeline fails at RestartPedestal"**
→ Layer 3 (`helm_configs` missing/wrong) or Layer 4 (chart gone /
values broken). See `references/db.md` and `references/chart.md`.

**"Submit returns `available apps: <only a few of the services>`"**
→ Layer 5 operator-managed workloads don't propagate CR metadata labels
to generated pods. Patch the CR's pod-facing labels field; don't change
`app_label_key`. See `references/otel.md` "Operator-managed pod labels".

**"Pipeline fully completes but `datapack.no_anomaly` or empty parquets"**
→ Layer 5 (collector attrs missing) or the chaos window is shorter
than the algorithm's required sample window. See `references/otel.md`.

**"It worked yesterday, now fails after backend restart"**
→ Layer 4 (`/tmp` wiped) or Layer 2 (new keys added to code that
aren't yet in etcd). See `references/chart.md`, `references/etcd.md`.

**"RestartPedestal hangs forever / helm install in `pending-install`"**
→ A rendered resource can never become Ready. `installAction.Wait=true`
+ one crashlooping Deployment (GCP-bound collector without metadata
server) or a `LoadBalancer` Service (no LB provider in kind) blocks the
whole install. Post-install hooks do NOT fire under deadlock — patch
the chart, don't use hook Jobs. See `references/chart.md`.

**"Pipeline runs green but `abnormal_logs.parquet` has no data rows"**
→ The system emits only traces over OTLP, AND the cluster
`otel-collector` has no `filelog`/`prometheus` receivers. Universal fix
is cluster-side: deploy the kube-stack to harvest logs/metrics
passively regardless of system instrumentation. Config lives at
`AegisLab/manifests/otel-collector/otel-kube-stack.kind.yaml`.

**"abnormal_logs OK but abnormal_traces empty"**
→ Apps point OTLP at the legacy `otel-collector` Service, but the
kube-stack creates new collector pods with different labels. Patch the
Service selector — see `references/otel.md` "Legacy Service selector fix".

**"abnormal_trace_id_ts empty but otel_traces has rows"**
→ Traces land with `k8s.namespace.name` but `service.namespace` blank.
BuildDatapack filters on `service.namespace` only. Add the transform
in `references/otel.md` "service.namespace backfill".

**"abnormal_metrics_histogram empty — ob/sockshop"**
→ Those stacks don't emit histograms (OpenCensus / Prometheus-only).
Set `RCABENCH_OPTIONAL_EMPTY_PARQUETS` on the benchmark container. See
`references/otel.md` "Per-system optional parquets".

**"ServiceName blank in otel_logs for an app without OTel SDK"**
→ k8sattributes `service.name` extraction needs
`app.kubernetes.io/name`. Add label-mapping rules covering `app`,
`name`, etc. See `references/otel.md` "service.name derivation".

**"Submit 500s with `namespaces "<ns>" not found`"**
→ First-use chicken-and-egg: submit's groundtruth resolution runs
before RestartPedestal creates the namespace. Workaround: `helm
install` the chart manually once (release name = namespace — see
`references/chart.md` "Pre-install release name"). Tracked as aegisctl
gap on issue #91.

## What's worth pushing upstream as `aegisctl` commands

Every one of the five layers currently requires SQL + etcdctl +
`kubectl cp` gymnastics. A single `aegisctl system register --name foo
--ns-pattern ... --app-label-key ...` that writes all seven etcd keys +
all three DB fixture rows atomically would remove the biggest failure
mode. `aegisctl cluster preflight` that checks all five layers is the
companion tool. Until those exist, walking the five layers by hand is
the only reliable way.

## Related

- `docs/troubleshooting/e2e-cluster-bootstrap.md` — exact commands for
  each layer on the current kind cluster.
- `docs/troubleshooting/e2e-repair-record-2026-04-21.md` — session
  where layers 2 and 4 were root-caused and partially code-fixed.
- `.claude/skills/onboard-benchmark-system/` — complementary skill for
  the workload-side (deploy pods, instrument OTLP) half of onboarding.
- `memory/aegislab_e2e_pitfalls.md` — terse list of pitfalls hit in
  previous sessions.
- Issue #91 — `aegisctl` control-plane tooling (atomic `system
  register`, `cluster preflight`, `trace cancel`).
- Issue #92 — `benchmark-charts` umbrella + wrapper-chart design.
- Issues #93–#98 — per-system wrapper chart sub-issues.
