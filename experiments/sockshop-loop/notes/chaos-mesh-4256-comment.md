### `exit status 101` is a catch-all — multiple distinct root causes share this code

Adding two data points that may help triage. We hit the **same** `Failed to apply chaos: rpc error: code = Unknown desc = exit status 101` on chaos-mesh **v2.8.0**, but on a workload **unrelated** to the original report's `xiang13225080/helloworld:v1.0`. Comparing the two cases suggests the underlying error is being squashed by chaos-daemon's gRPC wrapper, hiding at least two independent failure modes behind the same code.

#### Case A (this report's image): `xiang13225080/helloworld:v1.0`

We pulled the image and inspected it:

```
$ docker run --rm --entrypoint sh xiang13225080/helloworld:v1.0 -c \
    'cat /etc/os-release; ls /bin/sh; java -version'
NAME="Alpine Linux"
VERSION_ID=3.13.1
/bin/sh
openjdk version "16-ea" 2021-03-16
OpenJDK Runtime Environment (build 16-ea+32)
OpenJDK 64-Bit Server VM (build 16-ea+32, mixed mode, sharing)
```

So this image **does** have a shell (`/bin/sh` from busybox via Alpine). The exit-101 here is *not* about a missing shell. Likely candidates for this case:

- **JDK version drift** — chaos-daemon's bundled byteman targets older JDKs. Alpine + JDK 16-ea (early-access) has known attach-API differences (cf. #4184 about target/daemon JDK mismatch).
- **musl vs glibc** — Alpine uses musl libc, byteman ships native bits.
- **`-XX:+DisableAttachMechanism`** or container security context that blocks `/proc/<pid>/root` access.

We don't have hardware to confirm which of these is operative for #4256, but **none of them are "image lacks shell"**.

#### Case B (our cluster, sockshop coherence on v2.8.0): distroless target image, no shell

Our image is `gcr.io/distroless/java21-debian12` (no shell at all). Same `exit status 101` from JVMChaos with `action: latency`. From chaos-daemon's log:

```
chaos-daemon.daemon-server.background-process-manager.process-builder
  pb/chaosdaemon.pb.go:4458    build command
  {"namespacedName": "<ns>/<name>", "command":
   "/usr/local/bin/nsexec -m /proc/<pid>/ns/mnt -- sh -c mkdir -p /usr/local/byteman/lib/"}
```

The `nsexec -m` step enters the **target pod's** mount namespace and tries to invoke `sh`, which doesn't exist in the distroless image. We confirmed by `kubectl exec`:

```
$ kubectl exec <pod> -c <java-container> -- sh -c 'echo ok'
exec: "sh": executable file not found in $PATH
$ kubectl exec <pod> -c <java-container> -- ls /
exec: "ls": executable file not found in $PATH
```

Switching the workload's base image to `gcr.io/distroless/java21-debian12:debug` (which adds `/busybox/sh` and friends) fixes our exit-101 without any other change. So Case B *is* "image lacks shell" — the implementation needs `sh` *inside the target container's filesystem* to stage byteman, not just inside chaos-daemon.

#### What this suggests for chaos-mesh

The actionable observation isn't case-specific:

1. **`exit status 101` is the gRPC-level wrapper masking the real subprocess error.** The user has no way to discriminate Case A from Case B from a #3530-style attach permission error (#3530 closed but resurfaces) without daemon logs. **Capturing the failing subprocess's stderr and surfacing it in the JVMChaos CRD `status.experiment.containerRecords[].events[]` (or at least the chaos-daemon log line, not just the `exit status 101`) would close almost every "JVMChaos failed apply" issue without anyone needing to file one.** This is the highest-leverage fix.

2. **The implementation's dependency on a shell + writable directory layout *inside* the target pod** (Case B) overlaps with the architectural concern in #4184 (closed without resolution). This is increasingly visible as distroless / minimal Java images become common. Long-term, staging byteman entirely on the chaos-daemon side and entering the target only via PID namespace would eliminate Case B without trading anything off.

#### Workarounds for users hitting this today

- For Case A pattern (target image has shell, JDK ≥ 16, possibly Alpine): try a `eclipse-temurin:11-jre` or `openjdk:11` base; confirm `-XX:+DisableAttachMechanism` is NOT set; ensure security-context allows process-namespace access.
- For Case B pattern (distroless target): switch to the upstream's `:debug` tag — it adds busybox via `/busybox` while keeping the same Java runtime. ~10MB image-size hit.
