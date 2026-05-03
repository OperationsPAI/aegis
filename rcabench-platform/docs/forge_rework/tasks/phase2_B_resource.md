# Phase 2 Family B — Resource pressure

**Owner**: 1 agent (`mfst-B`)
**Worktree**: yes
**Reads first**: `phase2_template.md`, then `SCHEMA.md`

## Fault types in scope (6)

| fault_type_name | seed_tier | target_kind | mechanism |
|---|---|---|---|
| `CPUStress` | degraded | container | cgroup CPU throttle; thread queue buildup |
| `MemoryStress` | degraded | container | cgroup memory pressure; potential OOM |
| `JVMCPUStress` | degraded | container | JVM-level CPU saturation; GC pressure |
| `JVMMemoryStress` | degraded | container | JVM-level memory pressure; GC pressure |
| `JVMGarbageCollector` | slow | container | Forced GC pauses; user-visible latency |
| `NetworkBandwidth` | slow | pod | Bandwidth cap on egress/ingress |

## Sample injection directories

Find via:
```bash
ls /home/ddq/AoyangSpace/dataset/rca/ | grep -iE 'stress|gc|bandwidth' | head -20
```

For JVM-prefixed faults: many will share `*-stress-*` naming; differentiate by reading `injection.json` for `fault_type` int and matching against `models/injection.py`'s mapping.

## Family-specific guidance

### Resource pressure is gradual, not binary

- Entry features are **ratios**, not booleans: `cpu_throttle_ratio >= 3.0`, `memory_usage_ratio >= 0.85`, `gc_pause_ratio >= 0.2`.
- DON'T use `unavailable` or `silent` in entry_signature — these faults degrade, they don't sever.
- Span-level signature: `latency_p99_ratio >= 2.0`. Entry magnitude depends on injection params (read each case's `injection.json`).

### CPUStress vs JVMCPUStress

- CPUStress (cgroup-level): `cpu_throttle_ratio` is the primary signal; thread queue and JVM-internal metrics are secondary.
- JVMCPUStress (JVM-level): primary signal is `gc_pause_ratio` and `latency_p99_ratio`; cpu_throttle may NOT show because it's app-level CPU burn, not cgroup throttle. Check the IR adapter `K8sMetricsAdapter` to confirm what it emits for this fault type.

### MemoryStress vs JVMMemoryStress vs JVMGarbageCollector

- MemoryStress: `memory_usage_ratio >= 0.85` + possibly `oom_killed` label. Hand-off to OOM-killed → ContainerKill.
- JVMMemoryStress: heap usage proxy; may surface as `gc_pause_ratio` increase.
- JVMGarbageCollector: explicit GC injection; `gc_pause_ratio >= 0.3` is the strong entry signal.

### NetworkBandwidth

- Targets pod, NOT container. Entry: pod-level outbound throughput drop (would need a new feature; flag if not present in vocabulary).
- Downstream: spans see latency increase due to reduced effective throughput. `latency_p99_ratio` and `request_count_ratio` (because slow requests pile up, less complete).

### Hand-off candidates

- MemoryStress → ContainerKill when `oom_killed` label fires (validates as cascade).
- CPUStress → HTTPResponseAbort when downstream timeout rate exceeds 0.3 (covered by universal trigger).

### Derivation layer hints

- Layer 1: contains-spans + caller services. Expected features: `latency_p99_ratio >= 1.5`.
- Layer 2: callers. Expected: `latency_p99_ratio >= 1.2`, possibly `timeout_rate >= 0.05`.
- Layer 3: usually marginal; cap if data doesn't support.

## Acceptance bar (family B)

- All entry signatures use ratio features, not booleans.
- Magnitude bands have empirical justification from ≥3 sample cases each (or explicit `theoretical` mark with rationale).
- MemoryStress includes hand-off to ContainerKill triggered by `oom_killed` if the adapter emits this label.
- NetworkBandwidth manifest may flag a missing feature in vocabulary; don't fake it — surface to orchestrator.
