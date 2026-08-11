# NetworkLoss — manifest evidence

## Mechanism

Pod-level packet loss injection via netem. Chaos-mesh
`NetworkChaos.loss.loss` sets the per-packet drop probability (1–100%);
sampled cases hit 53–97%. TCP detects gaps via ACK and retransmits,
producing a moderate latency lift plus a non-trivial timeout/error
rate when retry budgets exhaust. Pod stays reachable.

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts0-mysql-loss-67k278` — loss=53%, direction=both, source=mysql → ts-train-service.
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-station-service-loss-hs8vrm` — loss=87%, direction=from (egress).
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-price-service-loss-ffld5s` — loss=67%, direction=from.
- `/home/ddq/AoyangSpace/dataset/rca/ts3-ts-contacts-service-loss-6lcnb9` — loss=97%, direction=from.
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-user-service-loss-tfz45k`.

## Entry signature

- Required: `span.timeout_rate ≥ 0.05` AND `span.latency_p99_ratio ≥ 1.3`.
  Both must hold — pure timeouts without latency suggests partition;
  pure latency without timeouts suggests delay. The conjunction is the
  loss-specific signature.
- Bands: `timeout_rate ≥ 0.05` is the conventional 5%-baseline timeout
  threshold (sample cases observed at 0.05–0.4); `latency_p99_ratio ≥ 1.3`
  is the empirical p5 of latency observed across the sample, widened
  by ×0.8.
- Optional `error_rate ≥ 0.05`. `optional_min_match: 0` — at moderate
  loss rates the error counter may not fire if upstream retries succeed.

## Layer bands

- Layer 1 (forward callees + service rollup): same `timeout_rate`
  signature, `latency_p99_ratio` decayed to 1.2.
- Layer 2 (transitive callers backward via `calls`): `error_rate ≥ 0.05`
  appears once retry budget is exhausted; `timeout_rate` decayed.

## Hand-offs

- **Within-family**: `NetworkLoss → NetworkPartition` when
  `span.timeout_rate ≥ 0.5` at layer 1. Justification: the dataset
  includes loss=97% (`ts3-ts-contacts-service-loss-6lcnb9`); at this
  severity TCP retransmits never succeed within the 4-minute window
  and the link is functionally indistinguishable from a partition.
  Verification continues under `NetworkPartition`'s silence-aware
  derivation rather than mis-attributing the deeper cascade to loss
  recovery.
- **No cross-family hand-offs** — universal triggers (`error_rate > 0.2`
  → HTTPResponseAbort, `silent` → NetworkPartition) are already
  encoded in those manifests' entries; adding them here would be
  redundant.
