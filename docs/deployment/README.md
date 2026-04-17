# Aegis Local Deployment Discovery Runbook

This directory records a real discovery pass for standing up the Aegis stack on a local `kind` cluster from a fresh parent-repo checkout.

Status of this attempt:
- Host tooling inspection: completed
- Submodule initialization: completed
- `kind` install: completed
- `kind` cluster bootstrap: attempted, failed on this host before Kubernetes became ready
- Backend host build: completed
- Frontend host install/build: completed
- Frontend container build: attempted, failed without valid `NPM_TOKEN`
- Chaos Mesh, workload, observability, backend-in-cluster, smoke test: blocked by cluster bootstrap failure

Attempted up to:
- [01-kind-cluster.md](./01-kind-cluster.md)

Stopped because:
- `kind create cluster` failed on this host with `Failed to create control group inotify object: Too many open files`, so no working local Kubernetes API server was available for the remaining steps.

Order of operations:
1. Read [prerequisites.md](./prerequisites.md).
2. Create the cluster with [01-kind-cluster.md](./01-kind-cluster.md).
3. Install Chaos Mesh with [02-chaos-mesh.md](./02-chaos-mesh.md).
4. Deploy the benchmark workload with [03-microservices.md](./03-microservices.md).
5. Deploy the backend with [04-backend.md](./04-backend.md).
6. Deploy the frontend with [05-frontend.md](./05-frontend.md).
7. Verify the observability stack with [06-observability.md](./06-observability.md).
8. Run the end-to-end smoke test in [07-smoke-test.md](./07-smoke-test.md).
9. Review all blockers in [known-gaps.md](./known-gaps.md).

What worked in this workspace:
- `git submodule update --init --recursive`
- local Go build in `AegisLab/src`
- local `pnpm install` and `pnpm build` in `AegisLab-frontend`
- Docker pulls from public `docker.io` during frontend image build

What still does not work:
- Local `kind` bootstrap on this machine
- Any Kubernetes-based deployment or verification
- Frontend Docker build without a valid `NPM_TOKEN` secret
- Any path that depends on internal `10.10.10.*` addresses or the team JuiceFS deployment
- Any chart or manifest path that expects private registries such as `pair-diag-cn-guangzhou.cr.volces.com` or `10.10.10.240`

Top blockers by impact:
- Host-level `kind` failure before the cluster is ready
- Private package auth for `@OperationsPAI/client`
- Internal-only registry and storage defaults in the backend and benchmark configs
