# Cold-start on kind (last validated end-to-end 2026-05-12)

Single-path runbook from zero to a Completed inject→datapack→algorithm trace
on a fresh `kind` cluster. Each validation run lands with `status=passed`
and all 5 tasks green (RestartPedestal, FaultInjection, BuildDatapack,
RunAlgorithm, CollectResult).

**Scope**: local kind cluster named `aegis-local`. Uses public Docker Hub
images, in-cluster NFS for RWX, `opentelemetry-kube-stack` for
traces+metrics+logs. Does **not** cover JuiceFS, Volcengine, Harbor, or any
internal registry.

## Validation log

| Date | Repo SHA | Case | Result | Environment | Notes |
|---|---|---|---|---|---|
| 2026-04-22 | (pre-relayout) | otel-demo PodFailure on `cart` | Completed | kind v?, kube v?, helm v? | Initial walkthrough; needed seed-race DROP DATABASE recovery. |
| 2026-04-23 | (pre-relayout) | otel-demo PodFailure on `cart` | Completed (5/5 tasks, 12 parquets, 4176 spans) | kind v?, kube v? | Re-walk green on first try; DROP DATABASE not needed. |
| 2026-05-12 | `b3d1afa` | `regression run otel-demo-guided` (PodFailure on `frontend`) | passed; final event `datapack.no_anomaly` | kind 0.30.0, kube 1.34.0 (3 nodes), helm 3.20.1, kubectl 1.35.3 | Drove via regression runner; surfaced inject-guided flag drift, app-label-key, and pedestal version drift (see Rough edges #26–#28). |

For per-benchmark onboarding (registering a new system, chart push, etc.)
see [`../troubleshooting/benchmark-integration-playbook.md`](../troubleshooting/benchmark-integration-playbook.md)
and the `register-aegis-system` skill.

---

## 0. Host prerequisites

Tools: `docker`, `kind` (≥ 0.30), `kubectl` (≥ 1.33), `helm` (≥ 3.18),
`just`, `uv`, `jq`, and an `aegisctl` binary (build from
`AegisLab/src/cmd/aegisctl` with `go build -o /tmp/aegisctl`).

Bump inotify limits before creating the cluster (kind v1.34 control plane
deadlocks with the default 128 instances):

```bash
docker run --rm --privileged --pid=host alpine:3.22 sh -lc \
  "apk add -q util-linux && \
   nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_instances=1024 && \
   nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_watches=524288"
```

These do **not** persist across host reboot. For persistence, write
`fs.inotify.max_user_instances=1024` + `fs.inotify.max_user_watches=524288`
into `/etc/sysctl.d/99-kind.conf`.

## 1. Kind cluster

The only kind-config in the repo lives under `manifests/test/` (the `test/`
profile itself is legacy, but the `kind-config.yaml` inside it is the one
used for aegis-local):

```bash
cd AegisLab
kind create cluster --name aegis-local --config manifests/test/kind-config.yaml
```

## 2. Chaos Mesh

On kind v1.34+, chaos-daemon must point at the containerd socket — the
chart default (Docker runtime) fails:

```bash
helm repo add chaos-mesh https://charts.chaos-mesh.org --force-update
helm repo update
helm install chaos-mesh chaos-mesh/chaos-mesh \
  --namespace chaos-mesh --create-namespace --version 2.8.0 \
  --set chaosDaemon.runtime=containerd \
  --set chaosDaemon.socketPath=/run/containerd/containerd.sock
```

## 3. NFS provisioner (ReadWriteMany)

The helm chart's `containers` / `juicefs` PVCs use `storageClass: nfs` in the
kind profile. Provide it in-cluster:

```bash
helm repo add nfs-ganesha-server-and-external-provisioner \
  https://kubernetes-sigs.github.io/nfs-ganesha-server-and-external-provisioner/
helm repo update
helm install nfs-server \
  nfs-ganesha-server-and-external-provisioner/nfs-server-provisioner \
  --namespace nfs --create-namespace \
  --set storageClass.name=nfs \
  --set persistence.enabled=true \
  --set persistence.size=20Gi \
  --set persistence.storageClass=standard
```

## 4. ClickHouse

The kube-stack collector writes to a ClickHouse that is **not** part of the
chart. Install it via the ClickHouse manifest in `otel-pipeline.yaml`, but
immediately remove the minimal traces-only collector it also ships — that
collector is insufficient for datapack build:

```bash
kubectl apply -f docs/deployment/otel-pipeline.yaml
kubectl -n otel delete configmap otel-collector-cfg
kubectl -n otel delete deploy otel-collector
kubectl -n otel delete svc otel-collector
```

Keep the `clickhouse` StatefulSet/Service; the kube-stack collector exports
into it.

## 5. OpenTelemetry kube-stack collector

This is the production-intent collector for kind (traces + metrics + logs
into ClickHouse, `k8sattributes` + `transform/service_namespace` for
datapack compatibility):

```bash
cd AegisLab/manifests/otel-collector
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm repo update
helm -n otel upgrade --install otel-kube-stack \
  open-telemetry/opentelemetry-kube-stack \
  -f otel-kube-stack.kind.yaml \
  --wait --timeout 10m
```

**Known issue (chart 0.144, 2026-04-22)**: the `collectors.daemon.scrape_configs_file`
override in older README copies does not work — the chart uses `.Files.Get`
which only resolves chart-packaged files. Leave `scrape_configs_file` unset
so the chart falls back to its bundled `daemon_scrape_configs.yaml`.
The values file in this repo already has it commented out.

### Compatibility Service for legacy `otel-collector` DNS

Benchmarks (otel-demo, sockshop, etc.) hardcode `otel-collector:4317` as
the OTLP endpoint. After kube-stack, the real service is
`otel-kube-stack-deployment-collector`. Create a compat Service so the
`otel-collector` externalname in each benchmark namespace resolves:

```bash
kubectl apply -f AegisLab/manifests/otel-collector/otel-collector-compat-svc.yaml
```

Verify: `kubectl -n otel get endpoints otel-collector` should be non-empty.

## 6. rcabench

The kind profile now defaults to `opspai/rcabench:latest` (public Docker
Hub image) so fresh clones don't need a local build + `kind load`. The
`--atomic --timeout 10m` combo from `just rcabench-install` is too
aggressive for first-pull — use a longer timeout and drop atomic so the
release survives long pulls:

```bash
cd AegisLab
helm upgrade -i rcabench ./helm \
  --namespace exp --create-namespace \
  --values ./manifests/kind/rcabench.yaml \
  --set-file initialDataFiles.data_yaml=data/initial_data/prod/data.yaml \
  --set-file initialDataFiles.otel_demo_yaml=data/initial_data/prod/otel-demo.yaml \
  --set-file initialDataFiles.ts_yaml=data/initial_data/prod/ts.yaml \
  --timeout 15m
```

For local-dev iteration on top of a locally-built image:

```bash
kind load docker-image aegislab-backend:local --name aegis-local
helm upgrade -i rcabench ./helm ... \
  --set images.rcabench.name=aegislab-backend \
  --set images.rcabench.tag=local
```

### Seed-race recovery (always do this once)

On a fresh cluster the producer + runtime-worker race the seed, and
`dynamic_configs` gets DB rows but etcd is left empty. Symptom:
`kubectl logs -n exp deploy/rcabench-api-gateway` shows
`Chaos system config manager loaded 0 systems (0 enabled)`.

Recovery is a drop-and-restart dance; until the root cause (`#17` in
`project_cold_start_gaps_2026_04_22.md`) is fixed this is mandatory:

```bash
kubectl scale deploy -n exp rcabench-api-gateway rcabench-runtime-worker-service --replicas=0
kubectl wait --for=delete pod -n exp -l app=rcabench-api-gateway --timeout=60s
kubectl exec -n exp rcabench-mysql-0 -- env MYSQL_PWD=yourpassword mysql -uroot \
  -e 'DROP DATABASE rcabench; CREATE DATABASE rcabench;'
kubectl exec -n exp rcabench-etcd-0 -- etcdctl --endpoints=http://localhost:2379 \
  del --prefix /rcabench/config/global/
kubectl scale deploy -n exp rcabench-api-gateway rcabench-runtime-worker-service --replicas=1
# wait for seed to run then restart so InitializeSystems re-reads etcd
sleep 60
kubectl rollout restart -n exp deploy/rcabench-api-gateway deploy/rcabench-runtime-worker-service
kubectl rollout status -n exp deploy/rcabench-api-gateway --timeout=180s
```

Verify with `aegisctl system list` — expect 8 rows (ts, otel-demo, ob,
sockshop, hs, sn, media, teastore) with `Builtin=true`.

## 7. aegisctl + first inject

```bash
kubectl port-forward -n exp svc/rcabench-edge-proxy 8082:8082 &

echo admin123 | /tmp/aegisctl auth login \
  --server http://localhost:8082 --username admin --password-stdin
/tmp/aegisctl context set --name default --default-project pair_diagnosis
```

Verify the seed: `/tmp/aegisctl system list` should show 8 builtin systems
(hs, media, ob, otel-demo, sn, sockshop, teastore, ts).

The canonical end-to-end smoke is the regression runner (step 7b). It
carries the required `interval` + `pre_duration` fields in the case YAML —
the `inject guided` CLI no longer accepts `--duration` / `--interval` /
`--pre-duration` flags (the backend still validates them as required, so
submitting via `guided --apply` alone fails with
`Field validation for 'Interval' failed on the 'required' tag`).

If you want a hand-rolled submit anyway, copy a regression YAML, edit the
`pedestal.version` to match what `aegisctl container version list-versions
<system>` returns, and run `regression run` against it (see 7b).

### 7b. Regression tests per benchmark

The tracked regression cases in `AegisLab/regression/*.yaml` are the
canonical E2E smoke. `--auto-install` makes aegisctl install the chart
into the pedestal namespace if it isn't there yet.

Two flags you'll almost always need on kind:

- `--app-label-key app.kubernetes.io/name` — every shipped chart labels
  pods with `app.kubernetes.io/name`, not the bare `app` key the runner
  defaults to. Without this the preflight fails `namespace X has no pods
  matching app=Y` even though the workload is healthy.
- A version override when the regression YAML's `pedestal.version` has
  drifted from what the seed actually has. Check with
  `aegisctl container version list-versions <name>`; if it mismatches,
  copy the YAML to a temp dir, edit, and point `--cases-dir` at it. (As
  of 2026-05-12: `otel-demo-guided.yaml` pins `0.1.4` while the seed in
  `data/initial_data/prod/data.yaml` only has `0.1.1`.)

```bash
# Run one case end-to-end (validated 2026-05-12 on kind-aegis-local):
/tmp/aegisctl regression run otel-demo-guided \
  --cases-dir AegisLab/regression \
  --auto-install \
  --app-label-key app.kubernetes.io/name \
  --output json
# Expected: passed, observed_task_chain = [RestartPedestal, FaultInjection,
# BuildDatapack, RunAlgorithm, CollectResult], final event datapack.no_anomaly.

# Run all shipped cases (one at a time — they share the aegis runner).
for case in otel-demo-guided hotelreservation-guided socialnetwork-guided \
            mediamicroservices-guided teastore-guided ob-guided \
            sockshop-guided; do
  /tmp/aegisctl regression run "$case" \
    --cases-dir AegisLab/regression --auto-install \
    --app-label-key app.kubernetes.io/name --output json || break
done
```

Benchmark-specific prereqs the runner does NOT auto-handle:

| Benchmark | Extra step before first run | Why |
|---|---|---|
| `sockshop` | `helm repo add coherence https://oracle.github.io/coherence-operator/charts && helm repo update && helm upgrade -i coherence-operator coherence/coherence-operator -n coherence-test --create-namespace --wait --version 3.5.11 --set image.registry=pair-cn-shanghai.cr.volces.com/opspai --set image.name=coherence-operator --set image.tag=3.5.11 --set defaultCoherenceImage.registry=pair-cn-shanghai.cr.volces.com/opspai --set defaultCoherenceImage.name=coherence-ce --set defaultCoherenceImage.tag=14.1.2-0-3` | Coherence CRs don't render without the operator. |
| `teastore` | none | Jaeger-client-java bridge is inside the chart. |
| `hs`/`sn`/`mm` | none | `dsb-wrk2` loader + Jaeger bridge are inside each chart. |

The sockshop case additionally needs two chart-value overrides in the
aegis seed because the upstream chart hardcodes
`monitoring/opentelemetry-kube-stack` for the OTel injector — the kind
profile puts both the Instrumentation CR and the collector in the
`otel` namespace instead. These are now shipped in
`AegisLab/data/initial_data/prod/data.yaml` under sockshop's
`helm_config.values`:

- `otel.instrumentation=otel/otel-kube-stack`
- `otel.collectorEndpoint=http://otel-kube-stack-deployment-collector.otel:4317`

Without these, BuildDatapack fails with
`Parquet file has no data rows: abnormal_traces.parquet` because the
OTel operator webhook silently skips injection when the Instrumentation
CR's namespace/name doesn't resolve.

If the chart was installed with stale values (e.g. the run raced the
`aegisctl system reseed` that propagates data.yaml drift), uninstall
and let `--auto-install` retry:

```bash
helm uninstall sockshop0 -n sockshop0
/tmp/aegisctl regression run sockshop-guided \
  --cases-dir AegisLab/regression --auto-install
```

`RunAlgorithm` uses `docker.io/opspai/detector` (public) by default;
the `random` and `traceback` algos in the aegis seed point at a
private Volces CN registry (`pair-diag-cn-guangzhou.cr.volces.com`)
and will hang on `Pulling` from overseas until configured. Swap to
`detector` in the regression YAML if that registry isn't reachable.

## 8. If a previous trace failed, clear the namespace lock

rcabench holds a per-namespace lock in redis; a failed trace does not
always release it, and the next inject loops on
`failed to acquire lock for namespace, retrying`:

```bash
kubectl exec -n exp rcabench-redis-0 -- redis-cli del monitor:ns:otel-demo0
```

---

## Known rough edges (as of 2026-04-22)

Ordered roughly by how likely they block a first-timer. Full details with
backend log signatures are in
`~/.claude/projects/-home-ddq-AoyangSpace-aegis/memory/project_cold_start_gaps_2026_04_22.md`.

| # | Layer | Workaround used here | Status |
|---|---|---|---|
| 9 | kind image | Fixed: defaults to `opspai/rcabench:latest` | **resolved** |
| 17 | seed race | `DROP DATABASE` + double `rollout restart` (step 6b) | open (#124) |
| 22 | kube-stack chart | Leave `scrape_configs_file` unset; use chart default | resolved |
| 23 | redis lock | Fixed: fault-injection path now releases on every error exit | **resolved** |
| 25 | compat svc | Manual `otel-collector` Service in `otel` ns (step 5b) | resolved |
| 8 | helm atomic | Drop `--atomic`, raise `--timeout` to 15m | workaround |
| 11 | pod ordering | Tolerate 1–3 CrashLoops while mysql provisions | workaround |
| 12 | admin creds | `admin / admin123` (from `data/initial_data/prod/data.yaml`) | as-designed |
| 15 | guided cache | Always pass `--reset-config` on first inject | workaround |
| 18 | bitnami chart | Needed for `ts` pedestal only; not used in this runbook | as-designed |
| 19 | `task list --state Running` | Fixed: backend accepts both names ("Running") and ints ("2") | **resolved** |
| 26 | inject guided time flags | `--duration` / `--interval` / `--pre-duration` removed from CLI but still required by the backend; use the regression runner (or hand-edit a regression YAML) instead of `inject guided --apply` | open |
| 27 | regression preflight label | Charts label pods with `app.kubernetes.io/name`; pass `--app-label-key app.kubernetes.io/name` or preflight always fails | workaround |
| 28 | regression yaml version drift | `otel-demo-guided.yaml` pins pedestal `0.1.4` but seed has `0.1.1`; copy + override `--cases-dir` until seed/yaml are aligned | open |
