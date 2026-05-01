# NetworkCorrupt — manifest evidence

## Mechanism

Pod-level packet corruption via netem. Chaos-mesh
`NetworkChaos.corrupt.corrupt` flips random bits in a configured
fraction of packets (sampled values 47–84%). TCP detects via checksum,
discards the packet, and the sender retransmits — observable signature
is similar to NetworkLoss but slightly less severe (no MAC/PHY-layer
overhead and corrupt packets still consume bandwidth on the first
transmission).

## Severity ranking

Loss > Corrupt > Duplicate. Reflected in entry-signature lower bounds:

| Manifest         | timeout_rate floor | latency_p99 floor |
|------------------|--------------------|-------------------|
| NetworkLoss      | 0.05               | 1.30              |
| NetworkCorrupt   | 0.04               | 1.25              |
| NetworkDuplicate | 0.02 (optional)    | 1.15              |

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts0-mysql-corrupt-kwx8n5` — corrupt=84%, direction=to.
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-order-other-service-corrupt-wkdp68` — corrupt=47%, direction=both.
- `/home/ddq/AoyangSpace/dataset/rca/ts3-ts-config-service-corrupt-rvz9sf` — corrupt=77%, direction=both.
- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-basic-service-corrupt-trd2kh` — corrupt=79%, direction=to.
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-config-service-corrupt-qjwhfb`.

## Entry signature

- Required: `timeout_rate ≥ 0.04` AND `latency_p99_ratio ≥ 1.25`.
  Same shape as NetworkLoss but lower thresholds — corrupt still
  retransmits, but TCP recovers a touch faster (no PHY-layer hop).
- Optional: `error_rate ≥ 0.04`.

## Layer bands

Mirrors NetworkLoss with each lower bound shifted down ~20% to encode
the severity gap. Layer 3 omitted; cascade halts at the second hop in
all sampled cases.

## Hand-offs

None. The `NetworkLoss → NetworkPartition` saturation hand-off does
not apply here: corrupt percentages in the dataset top out at 84% —
TCP retransmit eventually succeeds on retried packets at that level.
Adding a corrupt-saturation hand-off would be speculative without
≥95% corrupt samples to support it.
