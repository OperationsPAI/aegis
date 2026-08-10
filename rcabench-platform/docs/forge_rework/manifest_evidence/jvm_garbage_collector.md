# JVMGarbageCollector — manifest evidence

## Mechanism

Byteman injects a forced `System.gc()` (or equivalent full-GC trigger)
inside the targeted method. Each invocation produces a stop-the-world
pause that blocks the JVM worker; user-visible spans handled during
the pause window see latency increases. JvmAugmenterAdapter's
`frequent_gc` label fires when sustained `jvm.gc.duration` (or the
legacy `jvm.gc.collection.elapsed`) is observed; see
`ir/adapters/jvm.py:GC_DURATION_METRICS`.

## Sample case calibration

**Zero JVMGarbageCollector (`fault_type=26`) cases exist in the
canonical 500-case dataset.** All bands are theoretical.

Additionally, the sample dataset metric set does not include
`jvm.gc.duration` (or the legacy/runtime equivalents); only
`jvm.system.cpu.utilization`, `jvm.system.cpu.load_1m`, and
`jvm.cpu.recent_utilization` are present in
`abnormal_metrics.parquet`. The platform's IR adapter contract still
covers GC duration (the bootstrap feature `gc_pause_ratio` is in the
SCHEMA vocabulary and `JvmAugmenterAdapter` reads
`GC_DURATION_METRICS`), so the manifest references it correctly even
though no canonical sample exercises it. **Empirical calibration is
deferred until JVM-stack fixtures are provided.**

## Entry signature choice

- `gc_pause_ratio in [0.3, 1.0]` on `container`: theoretical floor.
  `gc_pause_ratio` is defined as "GC pause time / window time" in
  the SCHEMA vocabulary; sustained >= 0.3 means the JVM spends >= 30%
  of wall-clock in GC, which is the threshold beyond which user-mode
  throughput collapses. JvmAugmenterAdapter's own internal threshold
  for the `frequent_gc` label is consistent.
- Optionals: `latency_p99 >= 2.0` on spans (forced full-GC pauses are
  typically hundreds of ms; with normal P99 in low-tens of ms this
  is a 2x lower bound), `cpu_throttle_ratio >= 1.5` (GC threads burn
  CPU; modest lower bound).

## Derivation layers

Slimmer than CPUStress because GC pauses are episodic, not sustained:

- Layer 1: contains-spans + caller pod/service rollups; latency 2.0x.
- Layer 2: callers; latency 1.4x + timeout 0.05.

No layer 3: pause-driven cascades dampen quickly.

## Hand-off

`HTTPResponseAbort` via universal `timeout_rate > 0.3` on layer 2:
GC pauses past timeout budget surface as response-abort.

## Sample-case list

| case dir | fault_type | dur | role |
|---|---|---|---|
| (no fault_type=26 cases) | -- | -- | theoretical |

Empirical calibration deferred; flagged for orchestrator: when
JVM-stack GC fixtures land, recompute `gc_pause_ratio` band from
observed `jvm.gc.duration` percentiles.
