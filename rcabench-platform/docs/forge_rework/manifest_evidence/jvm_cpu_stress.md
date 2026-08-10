# JVMCPUStress — manifest evidence

## Mechanism

Byteman/JVM-level CPU saturation: the injector rewrites the targeted
method to enter a busy-loop, burning user-mode cycles inside the JVM
worker thread. Unlike cgroup CPUStress, no `cpu.cfs_throttled_us` is
incremented at the kernel level until the cgroup quota is hit; instead
`container.cpu.usage` rises to the cgroup limit and `cpu_limit_utilization`
saturates near 1.0. `K8sMetricsAdapter.CPU_METRICS` (which includes
`jvm.cpu.recent_utilization`) captures both signals and emits
`pod.DEGRADED + high_cpu`.

## Sample case calibration

Only **2** JVMCPUStress (`fault_type=27`) cases exist in the canonical
dataset. Both show identical signature shape:

| case dir | cpu_count | dur | cpu_lim_util peak | base p95 | ratio |
|---|---|---|---|---|---|
| ts5-ts-basic-service-stress-zf2fd7 | 5 | 4m | 1.0 | 0.095 | 10.6x |
| ts5-ts-cancel-service-stress-d8xbsn | 8 | 4m | 1.0 | 0.003 | 328x |

`container.cpu.usage` saturates to the cgroup limit (5.0 cores) in both
cases. Memory and other signals are at baseline.

n=2 is below the n>=3 threshold for empirical bands per the protocol,
so the entry band is marked `magnitude_source: empirical` only on
`cpu_throttle_ratio` (where both samples agree) and the `latency_p99_ratio`
optional (where one case shows 1.83x). `gc_pause_ratio` is theoretical:
JVM-level CPU burn often correlates with GC-pause pressure but the
dataset does not surface `jvm.gc.duration` so this cannot be calibrated.

## Entry signature choice

- `cpu_throttle_ratio >= 3.0` on `container`: lower bound = floor of
  observed ratios / 1.25 = 10.6 / 1.25 ~= 8.5 would be tight, but with
  n=2 we keep the conservative theoretical floor of 3.0 and let the
  optional latency feature carry residual specificity.
- Optionals (no min): `latency_p99` 1.5x (one case showed 1.83);
  `gc_pause_ratio` 0.1 theoretical.

## Derivation layers

Same template as CPUStress.

## Hand-off

`HTTPResponseAbort` via universal `timeout_rate > 0.3` on layer 2.

## Sample-case list

| case dir | fault_type | dur | role |
|---|---|---|---|
| ts5-ts-basic-service-stress-zf2fd7 | 27 | 4m | empirical |
| ts5-ts-cancel-service-stress-d8xbsn | 27 | 4m | empirical |
