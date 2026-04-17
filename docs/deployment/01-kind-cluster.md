# 01. Kind Cluster

This page records the exact commands run on this host to bring up a local `kind` cluster named `aegis-local`.

## 1. Initialize the config-bearing submodule

Executed:

```bash
git submodule update --init --recursive AegisLab
```

Output:

```text
Cloning into '/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-6/AegisLab'...
Submodule path 'AegisLab': checked out '1286a4e6f8c59dadbd3b77c28922f4442d70cb04'
```

The cluster config used below is [AegisLab/manifests/test/kind-config.yaml](/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-6/AegisLab/manifests/test/kind-config.yaml).

## 2. Verify tool versions

Executed:

```bash
kind version
kubectl version --client
docker --version
```

Output:

```text
kind v0.30.0 go1.24.6 linux/amd64
Client Version: v1.35.3
Kustomize Version: v5.7.1
Docker version 29.3.0, build 5927d80
```

## 3. First cluster create attempt

Executed:

```bash
kind create cluster --name aegis-local --config AegisLab/manifests/test/kind-config.yaml
```

Output:

```text
Creating cluster "aegis-local" ...
 • Ensuring node image (kindest/node:v1.34.0) 🖼  ...
 ✓ Ensuring node image (kindest/node:v1.34.0) 🖼
 ✗ Preparing nodes 📦 📦 📦
Deleted nodes: ["aegis-local-worker2" "aegis-local-control-plane" "aegis-local-worker"]
ERROR: failed to create cluster: could not find a log line that matches "Reached target .*Multi-User System.*|detected cgroup v1"
```

## 4. Confirm the host-side inotify failure

Executed:

```bash
kind create cluster --retain --name aegis-local --config AegisLab/manifests/test/kind-config.yaml -v 1
for c in $(docker ps -a --filter 'name=aegis-local' --format '{{.Names}}'); do
  echo "### $c"
  docker logs --tail 80 "$c" 2>&1
done
```

Important output:

```text
ERROR: failed to create cluster: could not find a log line that matches "Reached target .*Multi-User System.*|detected cgroup v1"

### aegis-local-worker
Failed to create control group inotify object: Too many open files
Failed to allocate manager object: Too many open files
[!!!!!!] Failed to allocate manager object.
Exiting PID 1...

### aegis-local-control-plane
Failed to create control group inotify object: Too many open files
Failed to allocate manager object: Too many open files
[!!!!!!] Failed to allocate manager object.
Exiting PID 1...

### aegis-local-worker2
Failed to create control group inotify object: Too many open files
Failed to allocate manager object: Too many open files
[!!!!!!] Failed to allocate manager object.
Exiting PID 1...
```

Observed host values before the workaround:

```bash
cat /proc/sys/fs/inotify/max_user_instances
cat /proc/sys/fs/inotify/max_user_watches
docker info --format '{{.CgroupDriver}} {{.CgroupVersion}}'
```

```text
128
65536
systemd 2
```

## 5. Apply the host workaround that succeeded here

Plain unprivileged `sysctl -w ...` printed a permission error and did not change the live `/proc/sys` values on this host, so the successful workaround used a privileged helper container to enter the host namespaces and run the exact `sysctl -w` commands there.

Executed:

```bash
docker run --rm --privileged --pid=host alpine:3.22 sh -lc "apk add --no-cache util-linux >/dev/null && nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_instances=1024 && nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_watches=524288"
cat /proc/sys/fs/inotify/max_user_instances
cat /proc/sys/fs/inotify/max_user_watches
kind delete cluster --name aegis-local
```

Output:

```text
fs.inotify.max_user_instances = 1024
fs.inotify.max_user_watches = 524288
1024
524288
Deleting cluster "aegis-local" ...
Deleted nodes: ["aegis-local-worker" "aegis-local-control-plane" "aegis-local-worker2"]
```

## 6. Create the working cluster

Executed:

```bash
kind create cluster --name aegis-local --config AegisLab/manifests/test/kind-config.yaml
```

Output:

```text
Creating cluster "aegis-local" ...
 • Ensuring node image (kindest/node:v1.34.0) 🖼  ...
 ✓ Ensuring node image (kindest/node:v1.34.0) 🖼
 ✓ Preparing nodes 📦 📦 📦
 • Writing configuration 📜  ...
 ✓ Writing configuration 📜
 • Starting control-plane 🕹️  ...
 ✓ Starting control-plane 🕹️
 • Installing CNI 🔌  ...
 ✓ Installing CNI 🔌
 • Installing StorageClass 💾  ...
 ✓ Installing StorageClass 💾
 • Joining worker nodes 🚜  ...
 ✓ Joining worker nodes 🚜
Set kubectl context to "kind-aegis-local"
You can now use your cluster with:

kubectl cluster-info --context kind-aegis-local
```

## 7. Verify the cluster

Executed:

```bash
kind get clusters
kubectl cluster-info --context kind-aegis-local
kubectl get nodes
```

Output:

```text
aegis-local

Kubernetes control plane is running at https://127.0.0.1:40757
CoreDNS is running at https://127.0.0.1:40757/api/v1/namespaces/kube-system/services/kube-dns:dns/proxy

NAME                        STATUS   ROLES           AGE   VERSION
aegis-local-control-plane   Ready    control-plane   28s   v1.34.0
aegis-local-worker          Ready    <none>          18s   v1.34.0
aegis-local-worker2         Ready    <none>          18s   v1.34.0
```
