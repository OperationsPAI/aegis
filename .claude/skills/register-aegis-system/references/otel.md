# Layer 5 reference: OTel collector + jaeger→OTLP bridge

Concrete collector config patterns. Methodology lives in `../SKILL.md`
layer 5.

## Contents

- [Required resource attributes](#required-resource-attributes)
- [Three pipelines, one collector](#three-pipelines-one-collector)
- [Database name](#database-name)
- [service.name derivation for non-SDK apps](#servicename-derivation-for-non-sdk-apps)
- [service.namespace backfill](#servicenamespace-backfill)
- [Filelog gotchas](#filelog-gotchas)
- [Jaeger → OTLP bridge (shared DSB pattern)](#jaeger--otlp-bridge-shared-dsb-pattern)
- [Operator-managed pod labels](#operator-managed-pod-labels)
- [Per-system optional parquets](#per-system-optional-parquets)
- [Legacy Service selector fix](#legacy-service-selector-fix)

## Required resource attributes

The collector must run `k8sattributes` with pod + namespace RBAC, and
enrich every signal with **both**:

- `k8s.namespace.name` — drives k8sattributes pod association.
- `service.namespace` — what BuildDatapack filters on
  (`WHERE ResourceAttributes['service.namespace'] = '<ns>'`).

Miss either and every parquet is empty even though the trace completes
and the job succeeds.

## Three pipelines, one collector

Traces, metrics, AND logs must all export to ClickHouse. Tables
auto-create on first batch, so "table missing" means no batch of that
type ever flowed. A traces-only config is the most common
misconfiguration.

## Database name

The ClickHouse exporter writes to whatever `database:` the exporter
names (defaults to `default`). BuildDatapack hard-codes
`otel.otel_logs` / `otel.otel_traces` / `otel.otel_metrics_*`. If the
exporter writes to `default.*`, counters look healthy, zero errors, and
parquets are empty.

## service.name derivation for non-SDK apps

For ob/sockshop (OpenCensus only) and similar, `k8sattributes`
`service.name` extraction only fires if the pod has
`app.kubernetes.io/name`. Derive via label rules covering common
conventions — first non-empty wins:

```yaml
k8sattributes:
  extract:
    metadata: [...]
    labels:
      - {tag_name: service.name, key: app.kubernetes.io/name, from: pod}
      - {tag_name: service.name, key: app, from: pod}
      - {tag_name: service.name, key: name, from: pod}
```

Without this, `otel_logs.ServiceName=''` for every entry and per-service
analysis is impossible.

## service.namespace backfill

When the SDK didn't set `service.namespace`, copy from
`k8s.namespace.name`:

```yaml
transform:
  log_statements:
    - context: resource
      statements:
        - set(attributes["service.namespace"], attributes["k8s.namespace.name"]) where attributes["service.namespace"] == nil
  trace_statements:
    - context: resource
      statements:
        - set(attributes["service.namespace"], attributes["k8s.namespace.name"]) where attributes["service.namespace"] == nil
  metric_statements:
    - context: resource
      statements:
        - set(attributes["service.namespace"], attributes["k8s.namespace.name"]) where attributes["service.namespace"] == nil
```

Symptom without this: traces land with `k8s.namespace.name=ob0` but
`abnormal_trace_id_ts` is empty.

## Filelog gotchas

Daemon-mode collector receiver quirks:

- `start_at: beginning` replays historical logs on every restart. In a
  long-lived cluster this pushes the current minute behind a backlog;
  BuildDatapack (queried immediately after the chaos window) hits an
  empty table. Use `start_at: end` unless backfill is wanted.
- The default severity_parser with `parse_from: attributes.level` drops
  entries lacking that field. Benchmarks have heterogeneous log shapes
  — strip the severity parser, parse best-effort.
- Filelog emits `resource["service.namespace"]` from the path parser,
  but the resource also needs `k8s.namespace.name` for k8sattributes
  pod association. **Copy** the parsed namespace into both keys.
- Default `pod_association` rules require `k8s.pod.ip` / `k8s.pod.uid` /
  connection info. Filelog entries carry none of these — add a
  `{k8s.pod.name, k8s.namespace.name}` rule or k8sattributes silently
  no-ops.

## Jaeger → OTLP bridge (shared DSB pattern)

All four DSB-family benchmarks (hotelreservation, mediamicroservices,
socialnetwork, teastore) instrument with the Jaeger client, not OTLP.
Instead of re-instrumenting every service, run a Jaeger→OTLP bridge as
a sidecar/Deployment:

- Expose the collector's Jaeger receiver (`jaeger:` on port 14250 gRPC
  or 6831 UDP).
- Redirect app env (`JAEGER_AGENT_HOST` / `JAEGER_COLLECTOR_URL`) at
  the bridge.
- The bridge just re-exports to OTLP/ClickHouse via the standard
  pipeline — no code changes in the benchmark services.

Working configs live in `aegislab/manifests/otel-collector/` — the
bridge is a generic otel-collector instance with a Jaeger receiver
alongside OTLP. Same binary, same exporter, extra receiver. This is
stack-agnostic: confirmed on DSB Go (hotelreservation), DSB C++
(mediamicroservices, socialnetwork), and Java (teastore).

## Operator-managed pod labels

Custom resources owned by operators (Coherence → StatefulSet, Strimzi →
pods) don't propagate `metadata.labels` to generated pods. The operator
decides. Most require a dedicated CR field:

- Coherence: `spec.labels`
- Strimzi: `spec.template.pod.metadata.labels`

Symptom: aegis submit returns `available apps: <only plain-Deployment
services>`, skipping operator-managed ones. Fix: patch the CR templates
to write the pod-facing labels field in addition to `metadata.labels`.
**Don't** change aegis's `app_label_key` as a workaround — pods from
different owners may use different keys; standardize on `app` and patch
the chart to reach it.

## Per-system optional parquets

`rcabench_platform.v3.sdk.datasets.rcabench.valid()` requires all 12
parquets non-empty. onlineboutique/sockshop never emit OTel histograms;
train-ticket has no filelog entries if stdout capture is off.

Relax per-system via `RCABENCH_OPTIONAL_EMPTY_PARQUETS` env on the
benchmark container (comma-separated filenames). The
`opspai/clickhouse_dataset:e2e-kind-20260421` image bakes the histograms
in as a default; override per benchmark via `container_versions.env_vars`.

## Legacy Service selector fix

Apps often hard-code OTLP at the legacy `otel-collector` Service in
namespace `otel`. After deploying the kube-stack chart (which creates
its own collector pods with different labels), patch the Service
selector:

```bash
kubectl -n otel patch svc otel-collector --type merge -p \
  '{"spec":{"selector":{"app":null,"app.kubernetes.io/name":"otel-kube-stack-deployment-collector"}}}'
```

Keeping the legacy name stable beats repointing every benchmark's
`COLLECTOR_SERVICE_ADDR`.
