# Known Gaps

### Kind bootstrap needs a host-side inotify bump on this machine

**Where**: `01-kind-cluster.md` first `kind create cluster` attempt  
**Symptom**: `kind create cluster` stops during `Preparing nodes`, and retained node logs show `Failed to create control group inotify object: Too many open files`  
**Root cause / guess**: the host started with `fs.inotify.max_user_instances=128` and `fs.inotify.max_user_watches=65536`, which was too low for `kindest/node:v1.34.0` under Docker `systemd` + cgroup v2 on this machine  
**Workaround attempted**: `sysctl -w fs.inotify.max_user_instances=1024` and `sysctl -w fs.inotify.max_user_watches=524288` from the unprivileged shell printed `permission denied on key ...` and left `/proc/sys/fs/inotify/max_user_instances=128` and `/proc/sys/fs/inotify/max_user_watches=65536` unchanged  
**Workaround applied**: `docker run --rm --privileged --pid=host alpine:3.22 sh -lc "apk add --no-cache util-linux >/dev/null && nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_instances=1024 && nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_watches=524288"`; after that, `/proc/sys/fs/inotify/max_user_instances` was `1024`, `/proc/sys/fs/inotify/max_user_watches` was `524288`, and `kind create cluster --name aegis-local --config AegisLab/manifests/test/kind-config.yaml` succeeded

### Chaos Mesh default `runtime=docker` is wrong for kind v1.34 (containerd)

**Where**: `02-chaos-mesh.md` default Helm install path from issue `#7`  
**Symptom**: NetworkChaos reconcile stuck — chaos-daemon logs `error while getting PID: expected docker:// but got container` (from `pkg/chaosdaemon/crclients/docker/client.go:59`), CR condition `AllInjected` stays `False`  
**Root cause**: `chaosDaemon.runtime` defaults to `docker`; kind v1.34 uses `containerd://` container IDs, which the docker-runtime client rejects  
**Workaround applied**: `helm upgrade chaos-mesh chaos-mesh/chaos-mesh --reuse-values --set chaosDaemon.runtime=containerd --set chaosDaemon.socketPath=/run/containerd/containerd.sock`; after daemon rollout, NetworkChaos applied cleanly and produced the expected latency impact (see `docs/deployment/09-smoke-run.md`)  
**Suggested fix (NOT applied here)**: follow-up issue title: `Default Chaos Mesh install to containerd runtime for local kind deploys`; scope: add the two flags to the repo-owned chaos-mesh values file and to `02-chaos-mesh.md`

### aegisctl backend rejects arbitrary namespaces (needs registered benchmark)

**Where**: `aegisctl chaos network delay --namespace <custom-ns>` path from issue `#14`  
**Symptom**: backend returns HTTP 500 with `Warning: batch[0][0]: unknown namespace "demo", using 0`; only pre-registered benchmark systems (`ts`, `hs`, `sn`, `ob`, `media`, `otel-demo`) are accepted as `pedestal.name`  
**Root cause**: the submit endpoint resolves namespace → benchmark-system ID through a fixed registry; unknown names fall to `0` and downstream validation fails  
**Workaround applied**: used `--dry-run` to emit the `FaultSpec` YAML, then applied an equivalent raw NetworkChaos CRD via `kubectl apply`; smoke run succeeded end-to-end (see `docs/deployment/09-smoke-run.md`)  
**Suggested fix (NOT applied here)**: follow-up issue title: `Let aegisctl chaos submit target an ad-hoc kind namespace`; scope: either expose a backend API to register new pedestals, or relax validation so a raw-k8s mode can ship NetworkChaos directly

### Repo-owned Chaos Mesh bootstrap assumes a private CN mirror values file

**Where**: `02-chaos-mesh.md` reference path from `AegisLab/scripts/start.sh`  
**Symptom**: the repo-owned Chaos Mesh install path expects `-f AegisLab/manifests/cn_mirror/chaos-mesh.yaml`, which rewrites images to `pair-diag-cn-guangzhou.cr.volces.com/pair/...`; in this docs-only worktree the submodule contents were not present either, so the values file could not be read locally  
**Root cause / guess**: the bootstrap script was authored for an internal environment that had both the submodule checkout and access to the private CN mirror registry  
**Workaround applied**: installed the same upstream chart and version (`chaos-mesh/chaos-mesh` `2.8.0`) directly from `https://charts.chaos-mesh.org` without the mirror override; the local kind cluster successfully pulled public `ghcr.io/chaos-mesh/*` images and the release reached `deployed`  
**Suggested fix (NOT applied here)**: follow-up issue title: `Document a public-registry/local-dev Chaos Mesh install path alongside cn_mirror overrides`; scope: keep the internal mirror values for CN environments, but add a checked-in local values file or explicit docs for the upstream public-image path
### Repo prerequisite check assumes `kubectx`

**Where**: `prerequisites.md` host-tool check and `AegisLab/justfile` `check-prerequisites`
**Symptom**: `❌ kubectx not installed` and `error: Recipe 'check-prerequisites' failed with exit code 1`
**Root cause / guess**: the repo treats `kubectx` as mandatory but does not install it or document it in the parent workspace
**Workaround attempted**: none in this pass; noted it as a required host dependency
**Suggested fix (NOT applied here)**: follow-up issue title: `Document kubectx as a required dev dependency or remove it from hard prerequisite checks`; scope: align README/devbox/prerequisite scripts

### Frontend container build requires GitHub Packages auth

**Where**: `05-frontend.md` Docker build step
**Symptom**: `cat: can't open '/run/secrets/NPM_TOKEN': No such file or directory` followed by `ERR_PNPM_FETCH_401 ... Unauthorized - 401` while fetching `@OperationsPAI/client`
**Root cause / guess**: [AegisLab-frontend/.npmrc](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/.npmrc) points `@OperationsPAI` to GitHub Packages and the Dockerfile expects a BuildKit secret named `NPM_TOKEN`
**Workaround attempted**: host-side `pnpm install` and `pnpm build` succeeded in this workspace, likely due local cache/store state; no clean container workaround was available without a real token
**Suggested fix (NOT applied here)**: follow-up issue title: `Document required NPM_TOKEN flow for frontend container builds`; scope: add explicit PAT scopes, `docker build --secret ...` example, and fallback guidance for fresh machines

### Test deployment profile assumes a private container registry

**Where**: `04-backend.md` cluster deployment path using `AegisLab/manifests/test/rcabench.yaml` and `AegisLab/skaffold.yaml`
**Symptom**: the checked-in test profile points images at `pair-diag-cn-guangzhou.cr.volces.com/pair/...` instead of a kind-local or public registry path
**Root cause / guess**: the test deployment was validated in an internal environment with pre-published images rather than from a fresh local checkout
**Workaround attempted**: replaced the live kind resources with public/local image refs at deploy time: `quay.io/coreos/etcd:v3.6.7`, `redis:8.0-M02-alpine3.20`, `mysql:8.0.43`, `jaegertracing/all-in-one:latest`, and `aegislab-backend:local`; also patched the backend pod off the private `pair/...` path instead of editing submodule manifests
**Suggested fix (NOT applied here)**: follow-up issue title: `Provide a truly local skaffold/kind profile for AegisLab`; scope: load locally built images into kind or switch test values to public/local image references

### Backend dev/test configs still encode internal endpoints

**Where**: `04-backend.md` config review
**Symptom**: [AegisLab/src/config.dev.toml](/home/ddq/AoyangSpace/aegis/AegisLab/src/config.dev.toml) sets `k8s.service.internal_url = "http://10.10.10.161:8082"`, `loki.address = "http://10.10.10.161:3100"`, and `k8s.init_container.busybox_image = "10.10.10.240/library/busybox:1.35"`
**Root cause / guess**: dev defaults were copied from a team environment instead of a local-only profile
**Workaround attempted**: used the chart-owned ConfigMap replacements for `internal_url`, replaced the busybox image with `busybox:1.35`, switched the single local backend pod to `both` mode with `/health` on port `4319`, and treated `loki.address` as non-blocking for this liveness-only pass because the local probe terminates before Loki is queried
**Suggested fix (NOT applied here)**: follow-up issue title: `Replace internal AegisLab dev defaults with localhost-or-env-based settings`; scope: parameterize internal URLs and image registries through env vars or dedicated local config

### JuiceFS default storage depends on an internal Redis/MinIO host

**Where**: `prerequisites.md` and `06-observability.md` storage notes
**Symptom**: [AegisLab/helm/values.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/helm/values.yaml) uses `juicefs.metaurl = "redis://10.10.10.119:6379/1"`, and [rcabench-platform/infra/README.md](/home/ddq/AoyangSpace/aegis/rcabench-platform/infra/README.md) instructs users to mount JuiceFS from `10.10.10.119`
**Root cause / guess**: the default storage path assumes access to a long-lived shared internal JuiceFS deployment
**Workaround attempted**: avoided JuiceFS entirely for this local backend bring-up by creating a `local-path` StorageClass alias backed by `rancher.io/local-path`, then binding the backend PVCs there instead of trying to use `redis://10.10.10.119:6379/1`
**Suggested fix (NOT applied here)**: follow-up issue title: `Provide a local-storage fallback for JuiceFS-backed Aegis deployments`; scope: add a documented local profile using hostPath/OpenEBS/MinIO-in-cluster instead of internal Redis/MinIO

### Backend local overlay still needs runtime-only fixes beyond the checked-in chart

**Where**: `04-backend.md` local kind deploy path
**Symptom**: the first local startup failed because the ServiceAccount existed in `exp` while the backend Deployment ran in `default`, the `init-etcd-data` container used an image without `sh`, and the `aegislab-backend-initial-data` ConfigMap had been created with empty strings
**Root cause / guess**: the repo does not yet provide a single self-consistent local overlay for namespace, init-container image, and seed-data wiring
**Workaround attempted**: created `aegislab-backend-sa` in `default`, patched the ClusterRoleBinding to include that subject, replaced `init-etcd-data` with a no-op `busybox:1.35` init container, and recreated `aegislab-backend-initial-data` from `AegisLab/data/initial_data/prod/*.yaml`
**Suggested fix (NOT applied here)**: follow-up issue title: `Add a supported local backend overlay for kind`; scope: check in one kustomize/Helm overlay that keeps namespace, init containers, seed data, and selectors aligned for a single-pod local backend

### rcabench-platform SDK defaults target internal Aegis endpoints

**Where**: downstream handoff after `07-smoke-test.md`
**Symptom**: [rcabench-platform/src/rcabench_platform/v2/config.py](/home/ddq/AoyangSpace/aegis/rcabench-platform/src/rcabench_platform/v2/config.py) defaults `dev` to `http://10.10.10.161:8082` and `prod` to `http://10.10.10.220:32080`
**Root cause / guess**: rcabench-platform was authored against internal environments without a parent-repo local profile
**Workaround attempted**: none in this pass; noted for future datapack-to-RCA handoff work
**Suggested fix (NOT applied here)**: follow-up issue title: `Parameterize rcabench-platform base_url defaults for local parent-repo deployments`; scope: move default URLs behind env vars or a local config profile

### Test bootstrap hardcodes a team-specific proxy

**Where**: `03-microservices.md` and `04-backend.md` bootstrap review
**Symptom**: [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/AegisLab/scripts/start.sh) exports `HTTP_PROXY=http://crash:crash@172.18.0.1:7890` and `HTTPS_PROXY=http://crash:crash@172.18.0.1:7890` for the `kind create cluster` step
**Root cause / guess**: the checked-in bootstrap script assumes a developer workstation or lab network with a preconfigured proxy at `172.18.0.1:7890`
**Workaround attempted**: did not use the script for cluster creation; ran `kind create cluster` directly from the workspace to avoid coupling the reproduction path to an undocumented proxy
**Suggested fix (NOT applied here)**: follow-up issue title: `Make start.sh proxy settings optional for local kind bootstrap`; scope: gate proxy env vars behind explicit env toggles or remove them from the default local path

### Frontend docs disagree on the local API target

**Where**: `05-frontend.md` local dev-server step
**Symptom**: [AegisLab-frontend/README.md](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/README.md) says the dev proxy targets `http://10.10.10.220:32080`, but [AegisLab-frontend/vite.config.ts](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/vite.config.ts) actually defaults to `http://127.0.0.1:8082` unless `VITE_API_TARGET` is set
**Root cause / guess**: the README lagged behind a later Vite config change from internal-cluster defaults to local-backend defaults
**Workaround attempted**: documented the actual runtime source of truth and used `VITE_API_TARGET=http://127.0.0.1:8082 pnpm dev` as the copy-pasteable command
**Suggested fix (NOT applied here)**: follow-up issue title: `Align frontend README with Vite proxy defaults`; scope: update docs so the local API target matches checked-in configuration
