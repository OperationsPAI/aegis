# Byte Cluster handoff checklist

This directory is the ByteDance/Volcengine deployment pack derived from the repo's kind-native path.

## 1. What must change vs. the current repo defaults

- Image sources:
  - repo defaults still mix `pair-diag-cn-guangzhou.cr.volces.com`, `10.10.10.240`, and external Helm OCI refs
  - this pack now points all locally proven platform and workload images directly at `pair-diag-cn-guangzhou.cr.volces.com/pair/*`
  - the remaining `opspai/*` runtime images and OCI chart refs are mirrored to `pair-cn-shanghai.cr.volces.com`
- Seed data:
  - upstream prod seed data still points at raw `opspai/clickhouse_dataset:*` and `opspai` OCI chart refs
  - this pack ships patched copies under `initial-data/` so benchmark/runtime jobs and system chart installs resolve from `pair-cn-shanghai.cr.volces.com/opspai/*`
- Backend config:
  - the Helm chart did not previously emit a `[database.clickhouse]` block even though datapack and `aegisctl cluster preflight` need it
  - this pack adds chart support for ClickHouse connection settings and points them at the operator-managed headless Service `clickstack-clickhouse-clickhouse-headless.monitoring.svc.cluster.local:8123` / database `otel` (see step 2 in `README.md` and `aegislab/vendor/clickstack-chart/SOURCE.txt`)
- Image pull secrets:
  - the Helm chart service account did not previously support `imagePullSecrets`
  - this pack adds that support, but the current Byte-cluster values do not require an image pull Secret because both `pair/*` and `pair-cn-shanghai.cr.volces.com/opspai/*` are expected to be directly reachable
- Observability stack:
  - the repo's `cn_mirror/otel-kube-stack.yaml` still carries the old prod-only receivers (`httpcheck/frontend-proxy`, `nginx`, `postgresql`, `redis`), opensearch exporter, and Prometheus jobs that do not help this cluster
  - this pack keeps the kind pipeline shape: daemon collector for filelog/kubeletstats, deployment collector for OTLP + generic pod/endpoints Prometheus scrape + spanmetrics + k8s events
- Autoscaling:
  - the deployment collector now starts at `6` replicas and enables HPA on both CPU and memory
  - HPA targets (`cpu=55%`, `memory=60%`) are intentionally stricter than the collector `memory_limiter` (`75%`) so scale-out happens before the limiter engages
- Cluster prerequisites:
  - Chaos Mesh must run with `containerd` runtime
  - the cluster must expose `metrics.k8s.io` for HPA; if `kubectl get --raw /apis/metrics.k8s.io/v1beta1/nodes` fails, install/fix metrics-server first
  - storage classes must provide both RWO (`rcabench`) and RWX/PVC semantics expected by the chart's `volcengine` storage profile

## 2. Main files in this pack

- `README.md`: deployment order and smoke-test commands
- `chaos-mesh.values.yaml`: Chaos Mesh values for containerd-based clusters
- `clickstack.values.yaml`: ClickStack values for the shared ClickHouse backend (operator-managed cluster from the vendored unreleased chart at `aegislab/vendor/clickstack-chart`)
- `otel-kube-stack.values.yaml`: trimmed OTel Kube Stack based on the kind variant, with collector HPA
- `daemon-scrape-configs.yaml`: daemon collector scrape config aligned with the kind variant
- `rcabench.values.yaml`: aegislab backend/runtime values for Byte cluster
- `frontend.yaml`: standalone frontend deployment/service, avoids the remote Helm subchart dependency
- `initial-data/*.yaml`: mirror-adjusted seed data for benchmark and algorithm images
