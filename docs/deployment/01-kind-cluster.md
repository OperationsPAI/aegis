# 01. Kind Cluster

This was the first blocking step in the discovery pass.

## Install `kind`

Executed:

```bash
curl -Lo /tmp/kind https://kind.sigs.k8s.io/dl/v0.30.0/kind-linux-amd64
install -m 0755 /tmp/kind /home/ddq/.local/bin/kind
kind --version
```

Verification:

```text
kind version 0.30.0
```

## Cluster config used by the repo

The repo ships [AegisLab/manifests/test/kind-config.yaml](/home/ddq/AoyangSpace/aegis/AegisLab/manifests/test/kind-config.yaml), which defines:

- 1 control-plane node
- 2 worker nodes
- registry mirror overrides for `docker.io` and `registry.k8s.io`

## Attempted bootstrap

Executed:

```bash
kind create cluster \
  --config AegisLab/manifests/test/kind-config.yaml \
  --name aegis-issue3
```

Captured output:

```text
Creating cluster "aegis-issue3" ...
 • Ensuring node image (kindest/node:v1.34.0) 🖼  ...
 ✓ Ensuring node image (kindest/node:v1.34.0) 🖼
 ✗ Preparing nodes 📦 📦 📦
Deleted nodes: ["aegis-issue3-control-plane" "aegis-issue3-worker2" "aegis-issue3-worker"]
ERROR: failed to create cluster: could not find a log line that matches "Reached target .*Multi-User System.*|detected cgroup v1"
```

## Rerun hygiene

If a retained failed bootstrap leaves exited node containers behind, delete the stale cluster before retrying:

```bash
kind delete cluster --name aegis-issue3
docker ps -a --filter 'name=aegis-issue3'
```

Without this cleanup, the next `kind create cluster` may fail earlier with:

```text
ERROR: failed to create cluster: node(s) already exist for a cluster with the name "aegis-issue3"
```

## Retained-debug rerun

Executed:

```bash
kind create cluster \
  --retain \
  --config AegisLab/manifests/test/kind-config.yaml \
  --name aegis-issue3 \
  -v 1
```

Then inspected retained node logs:

```bash
docker ps -a --filter 'name=aegis-issue3' --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}'
for c in $(docker ps -a --filter 'name=aegis-issue3' --format '{{.Names}}'); do
  echo "### $c"
  docker logs --tail 120 "$c" 2>&1
done
```

Important log lines:

```text
Failed to create control group inotify object: Too many open files
Failed to allocate manager object: Too many open files
[!!!!!!] Failed to allocate manager object.
Exiting PID 1...
```

## Verification target

Do not continue to later steps until this succeeds:

```bash
kubectl cluster-info --context kind-aegis-issue3
kubectl get nodes
```

Expected:
- `kubectl cluster-info` returns API endpoints
- all three nodes reach `Ready`

## Host-level findings

Observed on this machine:

```bash
ulimit -n
cat /proc/sys/fs/inotify/max_user_instances
cat /proc/sys/fs/inotify/max_user_watches
docker info --format '{{json .}}' | jq '{ServerVersion,Driver,CgroupDriver,CgroupVersion}'
```

Observed values:

```text
ulimit -n = 1048576
fs.inotify.max_user_instances = 128
fs.inotify.max_user_watches = 65536
docker: cgroup v2 + systemd driver
```

Attempted non-root sysctl workaround:

```bash
sysctl -w fs.inotify.max_user_instances=1024
sysctl -w fs.inotify.max_user_watches=524288
```

Result:

```text
sysctl: permission denied on key "fs.inotify.max_user_instances", ignoring
sysctl: permission denied on key "fs.inotify.max_user_watches", ignoring
```

Conclusion:
- On this host, cluster bootstrap is blocked until a human with sufficient privileges raises inotify limits or otherwise fixes the Docker/systemd interaction for `kind`.
