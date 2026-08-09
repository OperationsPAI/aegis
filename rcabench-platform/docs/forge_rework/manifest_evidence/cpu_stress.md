# CPUStress — manifest evidence

## Mechanism

Container-level CPU contention via cgroup CPU throttle (chaos-mesh
`StressChaos` of category `cpu`). Throttling causes thread queue buildup
inside the application; on JVM stacks this also raises GC pressure.
Inbound spans handled by the throttled container show P99 latency increase.

## Sample case calibration

**No CPUStress (`fault_type=4`) cases exist in the canonical 500-case
dataset.** Bands are therefore theoretical, with corroboration from the
2 JVMCPUStress cases (similar mechanism, different layer):

- `ts5-ts-basic-service-stress-zf2fd7` (fault_type=27, cpu_count=5, dur=4m):
  - `k8s.pod.cpu_limit_utilization` peak 1.0 vs base p95 0.095 -> ratio 10.6.
  - `container.cpu.usage` peak 5.0 vs base p95 0.47 -> ratio 10.6.
- `ts5-ts-cancel-service-stress-d8xbsn` (fault_type=27, cpu_count=8, dur=4m):
  - `k8s.pod.cpu_limit_utilization` peak 1.0 vs base p95 0.003 -> ratio 328.
  - `container.cpu.usage` peak 5.0 vs base p95 0.015 -> ratio 328.

Both cases drive the cgroup limit utilisation to saturation. cgroup-level
CPUStress should produce equivalent or stronger throttle signal.

## Entry signature choice

- `cpu_throttle_ratio >= 3.0` on `container`: directly mechanism-implied
  (chaos-mesh CPUStress targets a `workers`/`load` budget that drives
  the throttle counter; the IR adapter `K8sMetricsAdapter` (CPU_METRICS
  set) emits `degraded + high_cpu` off `k8s.pod.cpu_limit_utilization`).
  Lower bound 3.0x is the conservative theoretical floor; observed
  JVMCPUStress sample peaks are 10x and 328x.
- `thread_queue_depth` and `latency_p99_ratio` are optional; only one
  must match. `latency_p99_ratio` is set conservatively to 1.5 because
  in JVMCPUStress samples some pods receive no traffic during the 4-min
  chaos and show no latency change.

## Derivation layers

- Layer 1 (`runs` backward, `routes_to` backward, `includes` forward):
  contains-spans + caller pod/service rollups. Expected `latency_p99 >= 1.5`
  on spans, `cpu_throttle >= 3.0` on the rollup pod.
- Layer 2 (`calls` backward): immediate RPC callers. Latency relaxes to
  1.2x; `timeout_rate >= 0.05` admits cases where back-pressure surfaces
  as upstream timeouts.
- Layer 3 (`calls` backward): transitive callers. Latency 1.1x (boundary
  with background variance), error_rate >= 0.05 admits errors-as-cascade.

## Hand-off

- `HTTPResponseAbort` via universal `timeout_rate > 0.3` trigger on layer 2:
  CPU-stalled upstream blows past timeout budgets, producing
  response-abort symptoms at far callers.

## Sample-case list

| case dir | fault_type | dur | role |
|---|---|---|---|
| (no fault_type=4 cases) | -- | -- | theoretical |
| ts5-ts-basic-service-stress-zf2fd7 | 27 (JVMCPUStress) | 4m | corroboration |
| ts5-ts-cancel-service-stress-d8xbsn | 27 (JVMCPUStress) | 4m | corroboration |
