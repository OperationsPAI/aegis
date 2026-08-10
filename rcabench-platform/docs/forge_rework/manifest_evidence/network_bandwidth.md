# NetworkBandwidth — manifest evidence

## Mechanism

Pod-level network bandwidth cap via `tc`/`netem` on the target pod's
veth (chaos-mesh `NetworkChaos` of category `bandwidth`, with
`rate`, `limit`, `buffer`, and `direction` params from `display_config`).
Effective throughput drops toward the configured rate; TCP either
queues bytes against the cap (latency explodes) or fails fast against
back-pressure (request count collapses).

## VOCABULARY GAP — flagged for orchestrator

The bootstrap feature vocabulary in `SCHEMA.md` does NOT contain a
pod-level throughput feature. Concretely:

- The dataset exposes `k8s.pod.network.io` in
  `abnormal_metrics_sum.parquet` as a **monotonic cumulative-bytes
  counter**. Across all 22 canonical cases, abn/base p95 ratio of
  the raw counter sits in `[1.07, 1.45]` because the counter only
  grows. This is **not** a usable entry signal as-is.
- A rate-derived feature (`delta-bytes / delta-time`) is required.
  Proposal: add `network_throughput_ratio` on kind `pod` to
  `manifests/features.py`, with the IR adapter computing
  `(rate_in_window) / (baseline_rate)` from `k8s.pod.network.io`.
- Until the feature exists, `entry_signature` falls back to secondary
  symptoms (request_count drop, latency blow-up). These are noisier
  than a direct throughput observation would be and may admit
  background traffic spikes; expect FP cost on this fault type until
  the gap is closed.

## Sample case calibration (n=22)

22 cases total in canonical dataset (all 22 used; not subsampled):

```
ts0-ts-station-service-bandwidth-bp5k94
ts1-mysql-bandwidth-2xj2mq
ts2-mysql-bandwidth-jlzd96
ts2-ts-food-service-bandwidth-b5qvk5
ts2-ts-travel-service-bandwidth-f9fkg7
ts3-ts-consign-service-bandwidth-pmdbk7
ts4-ts-basic-service-bandwidth-cx9cfm
ts4-ts-basic-service-bandwidth-fn4pnv
ts4-ts-consign-service-bandwidth-6rx829
ts4-ts-consign-service-bandwidth-shcm2h
ts4-ts-preserve-service-bandwidth-f9jq67
ts4-ts-route-plan-service-bandwidth-q5lcsx
... (22 total; full list at fault_type=21 in /tmp/case_ft.txt)
```

Aggregated statistics (per-case ratio of abnormal vs baseline window):

- Span `request_count_ratio` (abn span count / baseline span count, on GT service):
  sorted `[0.01, 0.01, 0.02, 0.02, 0.02, 0.02, 0.03, 0.04, 0.06, 0.07,
  0.08, 0.12, 0.12, 0.13, 0.13, 0.15, 0.20, 0.26, 0.58, 0.66]`,
  median 0.07, p5/p95 [0.01, 0.62]. **Robust drop in 20/20 cases
  with valid traffic** (mysql cases had no spans on the GT service).
- Span `latency_p99_ratio`: sorted
  `[0.20, 0.26, 0.32, 0.37, 0.37, 0.48, 0.55, 1.06, 1.24, 1.41, 2.24,
  4.23, 19.34, 40.64, 77.26, 110.58, 453.76, 616.06, 959.40, 1553.05]`,
  bimodal: half drop, half explode by 10x-1500x. Latency alone is
  not a stable signature.
- `k8s.pod.network.io` (cumulative): abn/base p95
  `[1.07-1.45]` — confirms the vocabulary gap.

## Entry signature choice

- `request_count_ratio in [0.0, 0.6]` (REQUIRED) on span: lower 0.0,
  upper 0.6 chosen as `p95_observed / 1.25 / 1.25` ~= 0.4 widened to
  0.6 because the long-tail mode (p95 0.66) is real. This is the
  primary signal we have access to without the missing feature.
- Optionals (no min): `latency_p99 >= 1.5` (covers half of cases),
  `timeout_rate >= 0.05` (theoretical: TCP back-pressure surfaces
  as upstream timeouts).

## Derivation layers

- Layer 1 (`routes_to` backward, `includes` forward): pod-rollup
  spans + service spans on the throttled pod. Latency 1.5x +
  request_count drop carry forward.
- Layer 2 (`calls` backward): RPC callers. Latency 1.2x + timeout 0.05.

## Hand-off

`HTTPResponseAbort` via universal `timeout_rate > 0.3` on layer 2
(bandwidth-capped requests breach upstream timeout budgets).

## Sample-case list (full 22)

All 22 `fault_type=21` cases under `/home/ddq/AoyangSpace/dataset/rca/`
were used. Coverage spans 7 ts-N namespaces across 14 distinct GT
services (ts-station-service, mysql, ts-food-service, ts-travel-service,
ts-consign-service, ts-basic-service, ts-preserve-service,
ts-route-plan-service, etc.).
