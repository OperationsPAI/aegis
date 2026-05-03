# NetworkPartition — manifest evidence

## Mechanism

Pod-level network partition via chaos-mesh `NetworkChaos.action=partition`.
All traffic on the configured direction is blackholed: no SYN/ACK,
no RST, no ICMP. The pod process remains alive (distinct from PodKill
/ PodFailure); the *link* is silent. This is the strongest fault in
Family D.

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-user-service-partition-77jfkk` — direction=from (egress), source=ts-user-service → mysql.
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-station-service-partition-7z428n` — direction=both.
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-train-service-partition-lb8gv4` — direction=to (ingress).
- `/home/ddq/AoyangSpace/dataset/rca/ts3-ts-basic-service-partition-w5hbjw` — direction=to.
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-seat-service-partition-gtmt4k`.

## Entry signature

Per Family-D spec ("NetworkPartition is special"):

- Required: `pod.silent: true` (band [1.0, .inf] — boolean encoded
  as 1.0 satisfies the band). This is the ONLY network fault that
  legitimately fires `silent` at pod granularity, so requiring it
  here is what separates partition from loss/corrupt.
- Optional: `span.timeout_rate ≥ 0.5`, `span.error_rate ≥ 0.3`,
  `pod.unavailable: true`. `optional_min_match: 1` — at least one
  must accompany the silence (otherwise it could be a stopped pod
  rather than a partition; `optional` distinguishes the cases).

## Layer bands

- **Layer 1**: `service.silent: true` (Family-D spec — full traffic
  isolation seen at the service rollup) plus `span.silent: true`.
  Captured via `routes_to` backward (pod → service) and `includes`
  forward (service → spans).
- **Layer 2**: caller spans (`calls` backward) show
  `timeout_rate ≥ 0.5` AND `error_rate ≥ 0.3`. Decay is null because
  partitions are all-or-nothing rather than attenuating.
- **Layer 3**: transitive callers — bands relax to 0.1 / 0.1 because
  upstream retry/circuit-breakers may dilute the symptom.

## Hand-offs

None.

- Cross-family `silent` → NetworkPartition is circular at this manifest.
- Cross-family `error_rate > 0.2` → HTTPResponseAbort would fire at
  layer 2, but Family-D spec says only allow within-family
  `Loss → Partition`; HTTPResponseAbort is outside Family D and
  would create a speculative cross-family chain. Skipped.
- Within-family — partition is the terminal node of the network
  severity ordering; nowhere stronger to escalate to.

## Direction handling

Read `display_config.direction` from `injection.json`:
- `from` → egress: pod cannot send → outbound RPCs from pod are silent.
- `to` → ingress: pod cannot receive → callers' RPCs to pod time out.
- `both` → full isolation; both sides observed.

The entry's `pod.silent` requirement holds across all three directions
because the *partitioned* link goes silent regardless of direction;
the optional `timeout_rate` / `error_rate` features fire on the side
opposite to the direction (egress partition → caller-from spans
silent + timeout; ingress partition → caller-to spans timeout).
