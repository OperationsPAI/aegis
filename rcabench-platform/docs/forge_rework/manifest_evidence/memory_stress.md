# MemoryStress — manifest evidence

## Mechanism

Container-level memory pressure via cgroup memory ballast (chaos-mesh
`StressChaos` of category `memory`). The injector allocates a memory
balloon inside the container; once working_set approaches the cgroup
limit the kernel either applies pressure (page-cache reclaim, swap-out)
or invokes the OOM-killer.

## Sample case calibration

**No MemoryStress (`fault_type=3`) cases exist in the canonical 500-case
dataset.** Bands are theoretical, with corroboration from 65
JVMMemoryStress cases (similar mechanism at JVM layer):

10 sampled JVMMemoryStress cases (see `jvm_memory_stress.md`):
- `k8s.pod.memory_limit_utilization` peak/base p95 ratio: median 3.14,
  range 1.0-3.65. Pods that received traffic during the chaos clustered
  at peak 0.78-0.81 (3-3.6x baseline 0.24).

cgroup-level MemoryStress is mechanistically more aggressive than
JVM-level (no heap cap to bound the balloon early), so the entry band
[0.85, 1.0] is conservative against JVMMemoryStress's observed 0.78-0.81
peaks while reserving the [0.98, 1.0] tail for the ContainerKill hand-off
trigger.

## Entry signature choice

- `memory_usage_ratio in [0.85, 1.0]` on `container`: mechanism-implied
  (chaos-mesh memory stress drives `container.memory.working_set` toward
  `k8s.container.memory_limit`, surfacing in the IR via
  `K8sMetricsAdapter.MEMORY_METRICS`). Lower 0.85 = conservative theoretical
  floor (cgroup memcg pressure mode kicks in around 0.8-0.9); upper 1.0
  is the cgroup limit by definition.
- Optional features (no minimum match): `latency_p99_ratio` and
  `gc_pause_ratio` — both are weak/inconsistent on cgroup memory faults
  (latency only spikes if the app actually allocates during the chaos).

## Derivation layers

- Layer 1: same template as CPUStress. Expected memory pressure on the
  pod rollup, latency on inbound spans.
- Layer 2: callers. Latency 1.2x + error_rate 0.05 admits the OOM-induced
  request-failure path.

## Hand-off

- `ContainerKill` via `memory_usage_ratio > 0.98` on layer 1:
  cgroup memory limit saturation triggers OOM-kill. The IR's
  `JvmAugmenterAdapter` (`ir/adapters/jvm.py`, OOM_METRICS set
  `{k8s.container.oom_killed, jvm.memory.oom, ...}`) emits
  `container.UNAVAILABLE` with `oom_killed` specialization label, which
  is the ContainerKill entry signature. This makes the cascade
  MemoryStress -> OOM -> ContainerKill explicit rather than relying on
  the generic state-machine path.

## Sample-case list

| case dir | fault_type | dur | role |
|---|---|---|---|
| (no fault_type=3 cases) | -- | -- | theoretical |
| ts0-ts-auth-service-stress-nlpsfx | 28 (JVMMemoryStress) | 4m | corroboration |
| ts0-ts-cancel-service-stress-s7tf69 | 28 | 4m | corroboration |
| ts0-ts-config-service-stress-g6rpl9 | 28 | 4m | corroboration |
| ts0-ts-consign-service-stress-7f878v | 28 | 4m | corroboration |
| ts0-ts-food-service-stress-xfwkgh | 28 | 4m | corroboration |
