# Cold-start on kind (validated end-to-end 2026-04-22)

Single-path runbook from zero to a Completed inject→datapack→algorithm trace
on a fresh `kind` cluster. Walked through literally on 2026-04-22, ending with
`state=Completed`, all 5 tasks green (RestartPedestal, FaultInjection,
BuildDatapack, RunAlgorithm, CollectResult).

**Scope**: local kind cluster named `aegis-local`. Uses public Docker Hub
images, in-cluster NFS for RWX, `opentelemetry-kube-stack` for
traces+metrics+logs. Does **not** cover JuiceFS, Volcengine, Harbor, or any
internal registry.

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

The kind profile pins `aegislab-backend:local` (expected to be
built locally and `kind load`'d). For a quick cold-start, override to the
public image. The `--atomic --timeout 10m` combo from `just rcabench-install`
is too aggressive for first-pull — use a longer timeout and drop atomic so
the release survives long pulls:

```bash
cd AegisLab
helm upgrade -i rcabench ./helm \
  --namespace exp --create-namespace \
  --values ./manifests/kind/rcabench.yaml \
  --set-file initialDataFiles.data_yaml=data/initial_data/prod/data.yaml \
  --set-file initialDataFiles.otel_demo_yaml=data/initial_data/prod/otel-demo.yaml \
  --set-file initialDataFiles.ts_yaml=data/initial_data/prod/ts.yaml \
  --set images.rcabench.name=opspai/rcabench \
  --set images.rcabench.tag=latest \
  --set images.rcabench.pullPolicy=IfNotPresent \
  --timeout 15m
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
kubectl port-forward -n exp svc/rcabench-api-gateway 8082:8082 &

echo admin123 | /tmp/aegisctl auth login \
  --server http://localhost:8082 --username admin --password-stdin
/tmp/aegisctl context set --name default --default-project pair_diagnosis

# Pre-install the pedestal chart so submit doesn't race namespace creation.
/tmp/aegisctl pedestal chart install otel-demo --wait

# Submit a PodFailure. --reset-config avoids the "network pair not found"
# trap from stale ~/.aegisctl/inject-guided.yaml.
/tmp/aegisctl inject guided \
  --reset-config --apply --project pair_diagnosis \
  --pedestal-name otel-demo --pedestal-tag 0.1.1 \
  --benchmark-name clickhouse --benchmark-tag 1.0.0 \
  --chaos-type PodFailure --namespace otel-demo0 --app cart \
  --duration 2 --interval 2 --pre-duration 1 \
  --non-interactive
```

Poll to terminal state:

```bash
TRACE_ID=<id from submit output>
while true; do
  L=$(/tmp/aegisctl trace list --format tsv --columns id,state,last_event | grep "$TRACE_ID")
  S=$(echo "$L" | awk -F'\t' '{print $2}')
  case "$S" in
    Succeeded|Failed|Completed|Error|Cancelled) echo "$L"; break ;;
    *) echo "[$(date +%H:%M:%S)] $L"; sleep 30 ;;
  esac
done
```

Expected: `Completed` in roughly 4 minutes (pre=1min + duration=2min +
build+run).

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

| # | Layer | Workaround used here |
|---|---|---|
| 9 | kind image | Override `images.rcabench` to `opspai/rcabench:latest` |
| 17 | seed race | `DROP DATABASE` + double `rollout restart` (step 6b) |
| 22 | kube-stack chart | Leave `scrape_configs_file` unset; use chart default |
| 23 | redis lock | `redis-cli del monitor:ns:<ns>` after a failed trace |
| 25 | compat svc | Manual `otel-collector` Service in `otel` ns (step 5b) |
| 8 | helm atomic | Drop `--atomic`, raise `--timeout` to 15m |
| 11 | pod ordering | Tolerate 1–3 CrashLoops while mysql provisions |
| 12 | admin creds | `admin / admin123` (from `data/initial_data/prod/data.yaml`) |
| 15 | guided cache | Always pass `--reset-config` on first inject |
| 18 | bitnami chart | Needed for `ts` pedestal only; not used in this runbook |
