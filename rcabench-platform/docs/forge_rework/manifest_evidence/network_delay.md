# NetworkDelay — manifest evidence

## Mechanism

Pod-level netem latency injection. The chaos-mesh `NetworkChaos` resource
configures `delay.latency` (1–2000 ms, dataset values seen at 1381 ms)
and `delay.jitter` (0–1000 ms). Direction is one of `from` (egress),
`to` (ingress), or `both`. Pod-level resource counters (cpu, memory,
throttle) do not fire — it's a transport-layer perturbation only.

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-config-service-delay-vlf2nr` — latency=1381 ms, jitter=967 ms, direction=both, source=ts-config-service → mysql.
- `/home/ddq/AoyangSpace/dataset/rca/ts2-mysql-delay-d427wn` — pod=mysql-0.
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-station-service-delay-hwcd55` — pod=ts-station-service.
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-consign-service-delay-k5d7zl`.
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-travel-service-delay-gbx5sb`.

(`*-delay-*` cases that match the canonical `<svc>-delay-<id>` pattern;
HTTP request/response delay variants under `*-request-delay-*` /
`*-response-delay-*` are excluded — those belong to families B/C.)

## Entry signature

- Required: `span.latency_p99_ratio ≥ 1.5`. Empirical: with injected
  latency 1381 ms (the max in the sampled cases) and TrainTicket
  baseline RPC P99 of ~50–200 ms, expected ratio is ≥ 6×; the
  conservative lower bound at 1.5× covers the lower-end injections
  (smaller `latency` values within the [1, 2000] ms range).
- Optional: `latency_p50_ratio ≥ 1.3` and `timeout_rate ≥ 0.02` to
  capture jitter-driven tail timeouts. `optional_min_match: 0` because
  large-jitter injections hit P50, low-jitter ones don't.

Pod-level `silent`, `unavailable` are deliberately NOT used — sample
inspection confirms the pod stays reachable.

## Layer bands

- Layer 1 (forward callees): P99 decays via 0.7 (latency cascades but
  the next hop's own work dilutes it), so 1.5 × 0.7 ≈ 1.05 → use 1.3
  as a safety floor against background variation.
- Layer 2 (transitive callers backward): `latency_p99_ratio ≥ 1.15`
  plus `timeout_rate ≥ 0.02` for caller-side retry symptoms.

## Hand-offs

None. NetworkDelay does not naturally promote to other fault types
within the family (it is the gentlest); cross-family universal triggers
(`error_rate > 0.2`, `silent`) would not fire at the entry of a delay
case in any sampled instance.
