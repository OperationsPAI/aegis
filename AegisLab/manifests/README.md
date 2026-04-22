# manifests/

Environment-specific Kubernetes manifests and Helm values for deploying AegisLab.

## Status

| Dir | Status | Notes |
|---|---|---|
| `kind/` | **Validated 2026-04-22** | Fresh kind-cluster cold-start. Uses public Docker Hub images (`opspai/*`) and in-cluster NFS for RWX. Paired with `manifests/otel-collector/otel-kube-stack.kind.yaml` — the minimal `docs/deployment/otel-pipeline.yaml` is traces-only and is NOT sufficient for datapack build on its own. See `aegis/docs/deployment/cold-start-kind.md` for the full sequence. |
| `chaos-mesh/` | Supporting | Chaos Mesh helm values (runtime=containerd, socketPath=/run/containerd/containerd.sock). Required by all profiles on kind v1.34+. |
| `otel-collector/` | **Validated 2026-04-22** | `otel-kube-stack.kind.yaml` is the end-to-end-viable collector stack for kind. `docs/deployment/otel-pipeline.yaml` only installs ClickHouse + a traces-only collector — insufficient on its own for datapack build (traces, metrics, logs all need to land). Apply `otel-collector-compat-svc.yaml` after install for benchmarks that hardcode `otel-collector` DNS. |
| `cilium/`, `microservices/` | Supporting | Misc infra add-ons, not primary deploy targets. |
| `dev/` | **Unverified** | Likely still wired to internal registries. Treat as reference, not a ready-to-apply profile. |
| `prod/` | **Legacy / unverified** | References `pair-diag-cn-guangzhou.cr.volces.com` and `10.10.10.240` private registries. Not exercised by the cold-start flow — do not use without updating image refs. |
| `test/` | **Legacy / unverified** | Same caveat as `prod/`. |
| `staging/` | **Legacy / unverified** | Same caveat as `prod/`. |
| `cn_mirror/` | **Legacy / unverified** | Mirror profile for CN clouds; references private registries. |

The "validated" label means a fresh kind cold-start (`just rcabench-install` with `manifests/kind/rcabench.yaml`) has been driven end-to-end through chaos injection and datapack build on the date shown.

Unverified profiles are retained because they document historical deploy topologies, but they should not be used as starting points for new environments without first re-pointing image references to public or operator-owned registries.
