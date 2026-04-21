# SockShop integration record — 2026-04-21

Integrated the LGU-SE-Internal/coherence-helidon-sockshop-sample fork onto
aegis-local as the third benchmark system, following the
`register-aegis-system` skill. Recording the concrete pain points that showed
up and the workarounds used, so the next system is smoother.

## Pipeline status

End-to-end flow ran through: `RestartPedestal → FaultInjection → BuildDatapack`.
Datapack validation failed on `abnormal_metrics_sum.parquet has no data
rows` — same class of "upstream stack doesn't emit this signal" issue as
sockshop/ob histograms. Not a control-plane gap; product-side telemetry
coverage.

All five aegis-side layers (compiled registry, etcd config, DB fixtures,
helm chart, OTel pipeline) were validated working.

## Artifacts produced

- `benchmark-charts/charts/sockshop-aegis/` 0.1.1 — wrapper chart (embeds
  upstream `sockshop` subchart + patches Coherence CR templates to
  propagate `app: <name>` labels to pods). Pushed to
  `oci://registry-1.docker.io/opspai/sockshop-aegis:0.1.1`.
- Jib-built 6 Coherence/Helidon services: `opspai/ss-{carts,catalog,orders,
  payment,shipping,users}:2.12.3` and `opspai/ss-frontend:propagator`.
  Loaded into kind `aegis-local` nodes.
- `AegisLab/data/initial_data/{prod,staging}/data.yaml` — added sockshop
  pedestal entry + 7 `injection.system.sockshop.*` dynamic_configs.
- `AegisLab/regression/sockshop-guided.yaml` — canonical smoke case.

## Blockers hit (what burned time)

### 1. Coherence pods don't inherit `app: <name>` label

Coherence Operator reads `spec.labels` from the `Coherence` CR, not
`metadata.labels`. Upstream chart templates only set `metadata.labels`, so
the resulting StatefulSet pods carry `coherenceDeployment=carts` but **no**
`app=carts`. aegis's `app_label_key=app` resolver sees only the
non-Coherence pods (front-end) and reports `available apps: front-end`.

Fix: patch each Coherence template to also include `spec.labels:` with the
same `{app: <name>}` block. One-time edit per service; bumped wrapper
version 0.1.0 → 0.1.1.

### 2. Producer pod image is stale (missing ca-certificates)

Our #17 fix (install ca-certificates in the runner stage) is in `main`
but the image running on this cluster predates it. Remote OCI install
fails with `tls: failed to verify certificate: x509: certificate signed
by unknown authority`.

Workaround: `kubectl cp` the chart tgz into the producer pod at
`/var/lib/rcabench/dataset/charts/sockshop-aegis-0.1.1.tgz` and point
`helm_configs.local_path` to it. aegis's `RestartPedestal` falls through
to the local tgz when remote fetch fails.

Proper fix: rebuild `aegislab-backend:local` off current `main` (includes
#17) and reload into kind.

### 3. etcd keys live under `/rcabench/config/global/` prefix

First attempt wrote `injection.system.sockshop.*` at the etcd root.
Backend listener reads from `/rcabench/config/<scope>/`. The Viper-style
key looks the same in logs but the actual etcd path must include the
prefix, else `Removed runtime-only system registration: sockshop`.

Fix: always prepend `/rcabench/config/global/` for Global-scoped
`injection.system.*` keys.

### 4. data.yaml seed → DB happens automatically, → etcd does NOT

`initializeDynamicConfigs` (consumer.go:118) only writes the DB rows.
Only `MigrateLegacyInjectionSystem` publishes to etcd, and that path
only fires when the legacy `systems` table has rows — it's a no-op on
new systems added via data.yaml.

**This is the #1 pain point for new-system bootstrap on fresh clusters.**
The fix is a ~10-line extension to `initializeDynamicConfigs` (or a
separate `publishDynamicConfigDefaults` step) that writes each row's
`default_value` to `/rcabench/config/<scope>/<key>` on first seed.

Tracked as a new issue (to be filed).

### 5. Wrapper-chart publishing is manual

`helm package` + `helm push oci://registry-1.docker.io/opspai/...` has to
be run by the operator. For any `data.yaml` that references a chart
under OCI, there's no "aegis publishes the chart for you" path today.

Partial mitigations: `local_path` works around unavailable remote; a
dynamic_config `helm.repo.<repo_name>.url` can override the URL.

### 6. Cluster prerequisites are manual

`coherence-operator`, `otel-kube-stack` — neither is currently managed
by aegis. Operator had to run `helm repo add coherence …` and
`helm install operator coherence/coherence-operator` by hand before
sockshop-aegis can install.

Ideal: a per-system `prerequisites:` entry in data.yaml seed that
aegis idempotently applies on enable, similar to how Layer 4's
`helm.repo.<repo_name>.url` overrides work today but for full helm
releases.

### 7. Submit-time guided resolution runs before RestartPedestal

First-submit fail: submit's groundtruth resolver lists pods in the
target namespace, but the namespace is empty (workload hasn't been
installed yet). Our namespace auto-create fix (#25) created the ns but
not the pods.

Known issue already tracked (#91 first-submit pattern). Workaround:
`helm install sockshop oci://… -n sockshop0` manually once, then submit.

### 8. SockShop services don't emit OTel sum metrics

Coherence MP services do emit metrics via Prometheus, but the aegis
datapack validator requires non-empty `abnormal_metrics_sum.parquet`.
Needs to be relaxed per-system via `RCABENCH_OPTIONAL_EMPTY_PARQUETS`
on a sockshop-bench `container_versions.env_vars` row (which we
haven't created yet — benchmark container for sockshop is a separate
follow-up).

## What went well

- `register-aegis-system` skill's 5-layer mental model matched reality 1:1;
  nothing was surprising.
- Our #25 namespace auto-create feature removed the old `namespaces "X"
  not found` 500 on first submit.
- Our #23 app-list enrichment ("available apps: front-end") surfaced the
  Coherence label bug immediately instead of forcing a kubectl spelunk.
- Enhanced `aegisctl trace list --format tsv --columns` made inspecting
  the chain of trace failures instant.

## Remaining follow-ups

1. **[new]** Extend `initializeDynamicConfigs` to publish defaults to
   etcd on fresh seed. Without this, new data.yaml systems need manual
   etcdctl puts.
2. **[new]** Per-system `prerequisites:` list in data.yaml + aegis
   reconciler that idempotently `helm upgrade --install`s them.
3. **[new]** Create `sockshop-bench` container_versions row with
   `RCABENCH_OPTIONAL_EMPTY_PARQUETS` env_vars so sockshop datapacks can
   validate without metrics_sum/histogram.
4. **[existing]** #91 first-submit chicken-and-egg between guided
   resolution and RestartPedestal — suggested fix: defer app validation
   to runtime for truly empty-ns submits.
5. **[existing]** Rebuild `aegislab-backend:local` off current `main`
   (includes #17 ca-certificates fix) and reload into kind.
