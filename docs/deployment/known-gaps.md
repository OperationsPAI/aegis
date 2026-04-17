# Known Gaps

### Kind bootstrap fails before Kubernetes is ready

**Where**: `01-kind-cluster.md` bootstrap step  
**Symptom**: `kind create cluster` ends with `ERROR: failed to create cluster: could not find a log line that matches "Reached target .*Multi-User System.*|detected cgroup v1"` and retained node logs contain `Failed to create control group inotify object: Too many open files`  
**Root cause / guess**: host-level Docker/systemd/inotify limits are too low for `kindest/node:v1.34.0` on this machine; the repo does not document required sysctl settings or host prerequisites for `kind`  
**Workaround applied**: reran with `--retain -v 1` and inspected `docker logs` to identify the real cause; attempted `sysctl -w fs.inotify.max_user_instances=1024` and `sysctl -w fs.inotify.max_user_watches=524288`, but non-root writes were denied  
**Suggested fix (NOT applied here)**: follow-up issue title: `Document and validate host sysctl requirements for local kind bootstrap`; scope: add a preflight script or README section that checks inotify/file-descriptor limits before invoking `kind`

### Repo prerequisite check assumes `kubectx`

**Where**: `prerequisites.md` host-tool check and `AegisLab/justfile` `check-prerequisites`  
**Symptom**: `❌ kubectx not installed` and `error: Recipe 'check-prerequisites' failed with exit code 1`  
**Root cause / guess**: the repo treats `kubectx` as mandatory but does not install it or document it in the parent workspace  
**Workaround applied**: none in this pass; noted it as a required host dependency  
**Suggested fix (NOT applied here)**: follow-up issue title: `Document kubectx as a required dev dependency or remove it from hard prerequisite checks`; scope: align README/devbox/prerequisite scripts

### Frontend container build requires GitHub Packages auth

**Where**: `05-frontend.md` Docker build step  
**Symptom**: `cat: can't open '/run/secrets/NPM_TOKEN': No such file or directory` followed by `ERR_PNPM_FETCH_401 ... Unauthorized - 401` while fetching `@OperationsPAI/client`  
**Root cause / guess**: [AegisLab-frontend/.npmrc](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/.npmrc) points `@OperationsPAI` to GitHub Packages and the Dockerfile expects a BuildKit secret named `NPM_TOKEN`  
**Workaround applied**: host-side `pnpm install` and `pnpm build` succeeded in this workspace, likely due local cache/store state; no clean container workaround was available without a real token  
**Suggested fix (NOT applied here)**: follow-up issue title: `Document required NPM_TOKEN flow for frontend container builds`; scope: add explicit PAT scopes, `docker build --secret ...` example, and fallback guidance for fresh machines

### Test deployment profile assumes a private container registry

**Where**: `04-backend.md` cluster deployment path using `AegisLab/manifests/test/rcabench.yaml` and `AegisLab/skaffold.yaml`  
**Symptom**: the checked-in test profile points images at `pair-diag-cn-guangzhou.cr.volces.com/pair/...` instead of a kind-local or public registry path  
**Root cause / guess**: the test deployment was validated in an internal environment with pre-published images rather than from a fresh local checkout  
**Workaround applied**: replaced the live kind resources with public/local image refs at deploy time: `quay.io/coreos/etcd:v3.6.7`, `redis:8.0-M02-alpine3.20`, `mysql:8.0.43`, `jaegertracing/all-in-one:latest`, and `aegislab-backend:local`; also patched the backend pod off the private `pair/...` path instead of editing submodule manifests
**Suggested fix (NOT applied here)**: follow-up issue title: `Provide a truly local skaffold/kind profile for AegisLab`; scope: load locally built images into kind or switch test values to public/local image references

### Backend dev/test configs still encode internal endpoints

**Where**: `04-backend.md` config review  
**Symptom**: [AegisLab/src/config.dev.toml](/home/ddq/AoyangSpace/aegis/AegisLab/src/config.dev.toml) sets `k8s.service.internal_url = "http://10.10.10.161:8082"`, `loki.address = "http://10.10.10.161:3100"`, and `k8s.init_container.busybox_image = "10.10.10.240/library/busybox:1.35"`  
**Root cause / guess**: dev defaults were copied from a team environment instead of a local-only profile  
**Workaround applied**: used the chart-owned ConfigMap replacements for `internal_url`, replaced the busybox image with `busybox:1.35`, switched the single local backend pod to `both` mode with `/health` on port `4319`, and treated `loki.address` as non-blocking for this liveness-only pass because the local probe terminates before Loki is queried
**Suggested fix (NOT applied here)**: follow-up issue title: `Replace internal AegisLab dev defaults with localhost-or-env-based settings`; scope: parameterize internal URLs and image registries through env vars or dedicated local config

### JuiceFS default storage depends on an internal Redis/MinIO host

**Where**: `prerequisites.md` and `06-observability.md` storage notes  
**Symptom**: [AegisLab/helm/values.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/helm/values.yaml) uses `juicefs.metaurl = "redis://10.10.10.119:6379/1"`, and [rcabench-platform/infra/README.md](/home/ddq/AoyangSpace/aegis/rcabench-platform/infra/README.md) instructs users to mount JuiceFS from `10.10.10.119`  
**Root cause / guess**: the default storage path assumes access to a long-lived shared internal JuiceFS deployment  
**Workaround applied**: avoided JuiceFS entirely for this local backend bring-up by creating a `local-path` StorageClass alias backed by `rancher.io/local-path`, then binding the backend PVCs there instead of trying to use `redis://10.10.10.119:6379/1`
**Suggested fix (NOT applied here)**: follow-up issue title: `Provide a local-storage fallback for JuiceFS-backed Aegis deployments`; scope: add a documented local profile using hostPath/OpenEBS/MinIO-in-cluster instead of internal Redis/MinIO

### Backend local overlay still needs runtime-only fixes beyond the checked-in chart

**Where**: `04-backend.md` local kind deploy path
**Symptom**: the first local startup failed because the ServiceAccount existed in `exp` while the backend Deployment ran in `default`, the `init-etcd-data` container used an image without `sh`, and the `aegislab-backend-initial-data` ConfigMap had been created with empty strings
**Root cause / guess**: the repo does not yet provide a single self-consistent local overlay for namespace, init-container image, and seed-data wiring
**Workaround applied**: created `aegislab-backend-sa` in `default`, patched the ClusterRoleBinding to include that subject, replaced `init-etcd-data` with a no-op `busybox:1.35` init container, and recreated `aegislab-backend-initial-data` from `AegisLab/data/initial_data/prod/*.yaml`
**Suggested fix (NOT applied here)**: follow-up issue title: `Add a supported local backend overlay for kind`; scope: check in one kustomize/Helm overlay that keeps namespace, init containers, seed data, and selectors aligned for a single-pod local backend

### rcabench-platform SDK defaults target internal Aegis endpoints

**Where**: downstream handoff after `07-smoke-test.md`  
**Symptom**: [rcabench-platform/src/rcabench_platform/v2/config.py](/home/ddq/AoyangSpace/aegis/rcabench-platform/src/rcabench_platform/v2/config.py) defaults `dev` to `http://10.10.10.161:8082` and `prod` to `http://10.10.10.220:32080`  
**Root cause / guess**: rcabench-platform was authored against internal environments without a parent-repo local profile  
**Workaround applied**: none in this pass; noted for future datapack-to-RCA handoff work  
**Suggested fix (NOT applied here)**: follow-up issue title: `Parameterize rcabench-platform base_url defaults for local parent-repo deployments`; scope: move default URLs behind env vars or a local config profile

### Test bootstrap hardcodes a team-specific proxy

**Where**: `03-microservices.md` and `04-backend.md` bootstrap review  
**Symptom**: [AegisLab/scripts/start.sh](/home/ddq/AoyangSpace/aegis/AegisLab/scripts/start.sh) exports `HTTP_PROXY=http://crash:crash@172.18.0.1:7890` and `HTTPS_PROXY=http://crash:crash@172.18.0.1:7890` for the `kind create cluster` step  
**Root cause / guess**: the checked-in bootstrap script assumes a developer workstation or lab network with a preconfigured proxy at `172.18.0.1:7890`  
**Workaround applied**: did not use the script for cluster creation; ran `kind create cluster` directly from the workspace to avoid coupling the reproduction path to an undocumented proxy  
**Suggested fix (NOT applied here)**: follow-up issue title: `Make start.sh proxy settings optional for local kind bootstrap`; scope: gate proxy env vars behind explicit env toggles or remove them from the default local path

### Frontend docs disagree on the local API target

**Where**: `05-frontend.md` local dev-server step  
**Symptom**: [AegisLab-frontend/README.md](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/README.md) says the dev proxy targets `http://10.10.10.220:32080`, but [AegisLab-frontend/vite.config.ts](/home/ddq/AoyangSpace/aegis/AegisLab-frontend/vite.config.ts) actually defaults to `http://127.0.0.1:8082` unless `VITE_API_TARGET` is set  
**Root cause / guess**: the README lagged behind a later Vite config change from internal-cluster defaults to local-backend defaults  
**Workaround applied**: documented the actual runtime source of truth and used `VITE_API_TARGET=http://127.0.0.1:8082 pnpm dev` as the copy-pasteable command  
**Suggested fix (NOT applied here)**: follow-up issue title: `Align frontend README with Vite proxy defaults`; scope: update docs so the local API target matches checked-in configuration
