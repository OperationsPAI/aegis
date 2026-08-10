# NetworkDuplicate — manifest evidence

## Mechanism

Pod-level packet duplication via netem. Chaos-mesh `NetworkChaos`
duplicates a configured fraction of outbound (or inbound) packets;
TCP de-duplicates at the receiver via sequence numbers, so recovery
is essentially free. Net effect is a small bandwidth/CPU overhead
and a slight latency bump on adjacent spans.

## Sample cases

**No `*-duplicate-*` cases exist in `/home/ddq/AoyangSpace/dataset/rca/`
at the time of authoring** (verified via
`ls /home/ddq/AoyangSpace/dataset/rca/ | grep duplicate`, returns 0
matches). This is a known gap. All bands are `magnitude_source:
theoretical`, derived from:

1. The netem duplication mechanism (TCP de-dup is local; transitive
   callers are essentially shielded).
2. The by-construction severity ordering Loss > Corrupt > Duplicate —
   we set Duplicate's floors slightly below NetworkCorrupt's
   (0.02 vs 0.04 for `timeout_rate`; 1.15 vs 1.25 for
   `latency_p99_ratio`).

## Entry signature

- Required: `latency_p99_ratio ≥ 1.15` (only one required feature
  because pure-duplication seldom fires `timeout_rate` strongly).
- Optional: `timeout_rate ≥ 0.02`, `latency_p50_ratio ≥ 1.1`.

## Layer bands

Layer 1 lower bounds at 1.10 and 1.05 for P99/P50 ratios; layer 2
loosens further. This is the shallowest cascade in the family.

## Hand-offs

None. Cross-family universal triggers won't fire at duplicate's gentle
signature; within-family there is no escalation point (Loss/Partition
require packet drops, which duplication does not produce).

## Action item

When `*-duplicate-*` cases are added to the dataset, recompute the
empirical p5/p95 bands and flip `magnitude_source` to `empirical`.
