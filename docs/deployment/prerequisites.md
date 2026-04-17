# Prerequisites

This page lists the tools and access assumptions observed during the discovery pass.

## Host tools

Observed on this machine:

```bash
kind --version
kubectl version --client -o yaml
helm version --short
docker --version
just --version
devbox version
uv --version
```

Observed output:

```text
kind version 0.30.0
kubectl: v1.35.3
helm: v3.20.1+ga2369ca
docker: Docker version 29.3.0, build 5927d80
just 1.48.1
devbox 0.17.1
uv 0.10.9
```

Also required by the repo, but missing on this host when `AegisLab/just check-prerequisites` was run:

```bash
kubectx
```

Captured output:

```text
🔍 Checking development environment dependencies...
  - devbox installed
  - docker installed
  - helm installed
❌ kubectx not installed
error: Recipe `check-prerequisites` failed with exit code 1
```

## Access and secrets

Fresh contributors should assume the following are needed before a full deployment will work:

- GitHub Packages read access for `@OperationsPAI/client`
- Docker registry access for internal/private registries referenced by manifests and scripts
- Permission to change host sysctls if `kind` fails on inotify/file-descriptor limits
- Access to any storage backend replacing the internal JuiceFS deployment

## [MANUAL] GitHub Packages token for frontend image builds

The frontend Dockerfile uses a BuildKit secret named `NPM_TOKEN`.

Command shape:

```bash
cd AegisLab-frontend
docker build \
  --secret id=NPM_TOKEN,env=NPM_TOKEN \
  -t aegis-frontend-test .
```

Expected prerequisite:
- `NPM_TOKEN` must be a GitHub PAT with at least `read:packages`

Without a valid token, the build fails with `ERR_PNPM_FETCH_401`. See [05-frontend.md](./05-frontend.md).

## Internal service assumptions found in code

These are not safe defaults for a fresh local setup:

- `AegisLab/src/config.dev.toml` points `k8s.service.internal_url` and `loki.address` at `10.10.10.161`
- `AegisLab/helm/values.yaml` points JuiceFS metadata to `redis://10.10.10.119:6379/1`
- `rcabench-platform/src/rcabench_platform/v2/config.py` defaults to `10.10.10.161:8082` and `10.10.10.220:32080`
- `AegisLab/manifests/test/rcabench.yaml` pulls from `pair-diag-cn-guangzhou.cr.volces.com/pair/...`
- `AegisLab/manifests/staging/rcabench.yaml` and `AegisLab/skaffold.yaml` reference `10.10.10.240/...`
