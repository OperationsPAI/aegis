# Known Gaps

### Kind bootstrap needs a host-side inotify bump on this machine

**Where**: `01-kind-cluster.md` first `kind create cluster` attempt  
**Symptom**: `kind create cluster` stops during `Preparing nodes`, and retained node logs show `Failed to create control group inotify object: Too many open files`  
**Root cause / guess**: the host started with `fs.inotify.max_user_instances=128` and `fs.inotify.max_user_watches=65536`, which was too low for `kindest/node:v1.34.0` under Docker `systemd` + cgroup v2 on this machine  
**Workaround attempted**: `sysctl -w fs.inotify.max_user_instances=1024` and `sysctl -w fs.inotify.max_user_watches=524288` from the unprivileged shell printed `permission denied on key ...` and left `/proc/sys/fs/inotify/max_user_instances=128` and `/proc/sys/fs/inotify/max_user_watches=65536` unchanged  
**Workaround applied**: `docker run --rm --privileged --pid=host alpine:3.22 sh -lc "apk add --no-cache util-linux >/dev/null && nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_instances=1024 && nsenter -t 1 -m -u -i -n -p sysctl -w fs.inotify.max_user_watches=524288"`; after that, `/proc/sys/fs/inotify/max_user_instances` was `1024`, `/proc/sys/fs/inotify/max_user_watches` was `524288`, and `kind create cluster --name aegis-local --config AegisLab/manifests/test/kind-config.yaml` succeeded  
