# otel-collector — aegis-local (kind) variant

`otel-kube-stack.kind.yaml` + `daemon-scrape-configs.kind.yaml` are the
`aegis-local` kind-cluster localisation of the upstream `otel-kube-stack.yaml`
(the production variant targeting our external cluster).

## What was changed vs. the production manifests

| Area | Production | kind variant |
| --- | --- | --- |
| `clusterName` | `otel` | `aegis-local` |
| filelog `resource["cluster"]` value | `aiopsgogo` | `aegis-local` |
| filelog `include` | hardcoded `ts*_mysql-0*` / `ts*_ts-ui-dashboard*` | all pod logs, excludes `kube-system`, `otel`, `local-path-storage` |
| clickhouse exporter endpoint | `tcp://10.10.10.58:9000` (external IP) | `tcp://clickhouse.otel.svc.cluster.local:9000` (in-cluster Service, `password=clickhouse`) |
| Deploy collector — opensearch exporter | points at `opensearch.otel-demo:9200` | removed (+ dropped from logs pipeline) |
| Deploy collector — otel-demo receivers (`httpcheck/frontend-proxy`, `nginx`, `postgresql`, `redis`) | present | removed (none of those services run in kind) |
| Prometheus scrape — `kube-prom-exporter` + `federate` jobs | present | removed (no kube-prom-stack, no cilium-monitoring in kind) |
| daemon scrape — `node-exporter` (`:9100`) + `federate` | present | removed for the same reason |
| metrics pipeline receivers | `[httpcheck/frontend-proxy, nginx, otlp, postgresql, redis, spanmetrics]` | `[otlp, prometheus, spanmetrics]` |

Anything a benchmark chart wants to scrape just needs the standard
`prometheus.io/scrape=true` + `prometheus.io/port=<n>` pod annotations.

## Applying to the cluster

```bash
# ensure the opentelemetry-kube-stack chart repo is available
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm repo update

helm -n otel upgrade --install otel-kube-stack \
  open-telemetry/opentelemetry-kube-stack \
  -f otel-kube-stack.kind.yaml \
  --wait --timeout 10m
```

**Do not** pass `--set-file collectors.daemon.scrape_configs_file=...` — chart
≥ 0.144 resolves `scrape_configs_file` via `.Files.Get`, which only reads
files packaged inside the chart, not CWD-relative paths or --set-file content.
The values file here leaves the field unset so the chart falls back to its
bundled `daemon_scrape_configs.yaml` (the pod-annotation scrape config we
want). `daemon-scrape-configs.kind.yaml` in this directory is retained for
reference and should be kept in sync with the chart's bundled file.

Before installing, delete the ad-hoc `otel-collector` Deployment currently
running in the `otel` namespace (the kube-stack chart creates its own
collectors via the operator). The existing `clickhouse` StatefulSet and
its `clickhouse` Service are the destination for log/metric/trace
exporters; leave those in place.

After install, benchmarks that hardcode `otel-collector` as the OTLP DNS
(otel-demo, sockshop, etc.) need a compat Service in the `otel` namespace
pointing at the deployment collector — see step 5 of
`aegis/docs/deployment/cold-start-kind.md`.

## Verifying after install

```bash
# pods up (expect one "collector-daemon-*" per node + one "collector-deployment-*")
kubectl -n otel get pods -l app.kubernetes.io/name=opentelemetry-collector

# log rows landing in ClickHouse (any namespace)
kubectl -n otel exec clickhouse-0 -- \
  clickhouse-client --password clickhouse \
  --query "SELECT COUNT(*) FROM otel.otel_logs WHERE Timestamp >= now() - INTERVAL 1 MINUTE"
```

Logs for the injection namespace (e.g. `ob0`) must be non-empty before
`BuildDatapack` will produce a `.valid` datapack.
