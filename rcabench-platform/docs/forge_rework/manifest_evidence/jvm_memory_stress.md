# JVMMemoryStress — manifest evidence

## Mechanism

Byteman/JVM-level memory pressure: the injector rewrites the targeted
method to allocate large heap or stack regions on each invocation
(`mem_type` 1=Heap or 2=Stack from `display_config`). Heap pressure
surfaces as `k8s.pod.memory_limit_utilization` rising toward the cgroup
cap; allocation-walk also burns CPU. JvmAugmenterAdapter's
`high_heap_pressure` label fires when `jvm.memory.used / jvm.memory.limit`
sustains >= 0.90.

## Sample case calibration (n=10)

10 cases sampled from `ts0-*-stress-*` (all `fault_type=28`, dur=4m,
mem_type=2 stack):

```
ts0-ts-auth-service-stress-nlpsfx       svc=ts-auth-service
ts0-ts-cancel-service-stress-s7tf69     svc=ts-cancel-service
ts0-ts-config-service-stress-g6rpl9    svc=ts-config-service
ts0-ts-consign-price-service-stress-t67vtg svc=ts-consign-price-service
ts0-ts-consign-service-stress-7f878v   svc=ts-consign-service
ts0-ts-food-service-stress-xfwkgh      svc=ts-food-service
ts0-ts-order-service-stress-cklk2p     svc=ts-order-service
ts0-ts-price-service-stress-n787pd     svc=ts-price-service
ts0-ts-route-service-stress-kstvv2     svc=ts-route-service
ts0-ts-station-food-service-stress-j5qdln svc=ts-station-food-service
```

Ratio of abnormal-window peak to baseline-window p95 (per case),
sorted across the 10 cases:

- `k8s.pod.memory_limit_utilization` peak/base p95:
  `[1.0, 1.0, 3.02, 3.09, 3.13, 3.14, 3.26, 3.26, 3.56, 3.65]`
  median 3.14, p5/p95 [1.0, 3.6]. Bimodal: 2 of 10 pods didn't
  allocate (method probably never called); the other 8 cluster
  tightly at peak ~0.78-0.81 absolute (3.0-3.6x baseline ~0.24).
- `k8s.pod.cpu_limit_utilization` peak/base p95:
  `[3.19, 4.52, 4.68, 5.68, 6.09, 10.76, 16.67, 42.05, 121.81, 310.18]`
  median 8.42, p5/p95 [3.79, 225]. CPU burn from allocation-walk
  is consistent across all 10 cases.
- Span `latency_p99` ratio (abn/base on GT service):
  `[0.70, 0.79, 1.04, 1.27, 4.75, 7.87, 9.00, 14.41, 53.04, 55.94]`
  median 6.31, p5/p95 [0.74, 54.6]. Bimodal: same pods that didn't
  allocate also didn't see traffic.

## Entry signature choice

- `memory_usage_ratio in [0.7, 1.0]` on `container`: lower 0.7 is
  derived as `p50_observed_peak / 1.25` ~= 0.78 / 1.25 ~= 0.62, rounded
  up to 0.7 (we want to filter out the no-traffic mode at base ~0.24).
  Note: this is **absolute** memory ratio, not ratio-vs-baseline; the
  feature definition in `manifests/features.py` (per SCHEMA.md
  vocabulary table: `memory_usage_ratio = memory bytes / limit`)
  treats it as utilisation, so 0.7 means "70% of cgroup memory limit",
  which our sample tail breaches.
- Optionals: `cpu_throttle_ratio >= 3.0`, `latency_p99 >= 1.5`,
  `gc_pause_ratio >= 0.1` (theoretical). At least 1 must match — this
  excludes the bottom mode of pods that never received the chaos
  payload.

## Derivation layers

Layer 1: pod rollup + spans, same template as MemoryStress.
Layer 2: callers, latency 1.2x + error 0.05.

## Hand-off

`ContainerKill` via `memory_usage_ratio > 0.98` on layer 1: empirical
peaks are 0.78-0.81 (below threshold), so this hand-off only fires on
the pathological tail where heap saturates the cgroup limit and
OOM-kill triggers (oom_killed label per JvmAugmenterAdapter). It's not
a default propagation step.

## Sample-case list

10 cases enumerated above; all are fault_type=28, mem_type=2 (stack),
dur=4m, namespace=ts. Cases sourced from
`/home/ddq/AoyangSpace/dataset/rca/`.
