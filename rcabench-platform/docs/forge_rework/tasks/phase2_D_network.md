# Phase 2 Family D — Network-level injection

**Owner**: 1 agent (`mfst-D`)
**Worktree**: yes
**Reads first**: `phase2_template.md`, then `SCHEMA.md`

## Fault types in scope (5)

| fault_type_name | seed_tier | target_kind | mechanism |
|---|---|---|---|
| `NetworkDelay` | slow | pod | Outbound packet delay; latency added |
| `NetworkLoss` | degraded | pod | Outbound packet loss; retries kick in |
| `NetworkDuplicate` | degraded | pod | Outbound packet duplication; protocol-level recovery |
| `NetworkCorrupt` | degraded | pod | Outbound packet corruption; checksums fail |
| `NetworkPartition` | silent | pod | Outbound flow blocked entirely |

## Sample injection directories

```bash
ls /home/ddq/AoyangSpace/dataset/rca/ | grep -iE 'partition|delay|loss|corrupt|duplicate' | head -30
```

Cross-check `injection.json` — fault types 17–22 per `models/injection.py` comments.

## Family-specific guidance

### Critical: target is the SOURCE pod, effects are at the DESTINATION

Network injection at pod X affects X's **outbound** traffic. Downstream effects appear at:

- Spans where X is the **callee** (and the caller observes timeout/error)
- Spans where X is the **caller** (X's outbound RPC sees the impact directly)

Disambiguate by reading the chaos-mesh policy (in `display_config` of `injection.json`) — it specifies `egress` or `ingress` direction.

### NetworkPartition is special

- Entry signature: `pod.silent: true` AND callers observe `span.timeout_rate >= 0.5` quickly.
- Layer 1: `service.silent: true` (full traffic isolation).
- Layer 2: caller spans show `error_rate >= 0.5` due to timeouts.
- Hand-off: NetworkPartition → DNSError if outbound DNS lookups go through the partitioned interface (rare, document if empirical).

### NetworkDelay / Bandwidth (not in this family but cross-reference)

NetworkDelay is the gentlest network fault:
- Entry: caller spans show `latency_p99_ratio >= 1.5`. Pod-level features don't necessarily fire.
- Layer 1: directly-called services see latency increase.
- Layer 2: minimal cascade unless retries trigger.

### NetworkLoss / Duplicate / Corrupt

These three are similar in observable signature:
- TCP recovers via retransmission → moderate latency increase + occasional timeout.
- Entry: `span.timeout_rate >= 0.05` AND `latency_p99_ratio >= 1.3`.
- They differ in degree: Loss > Corrupt > Duplicate by impact severity (Loss forces retransmit; Duplicate is mostly tolerated).
- Document the ranking; don't pretend they're identical.

### Hand-off candidates

- Most network faults can hand off to HTTPResponseAbort (timeouts → response abort observed by upstream) — universal trigger applies.
- NetworkLoss → NetworkPartition when loss rate saturates (`loss_rate >= 0.95`) — within-family hand-off, allowed.

### Derivation layer hints

- Layer 1: directly called services (forward `calls`). Expected: `timeout_rate`, `error_rate`, or `latency_p99_ratio`.
- Layer 2: Transitive callers backward via `calls`. Expected: same signals, decayed.
- Max layer typically 3 because retry-and-fail is fast.

## Acceptance bar (family D)

- 5 manifests with explicit handling of egress vs ingress where chaos-mesh policy specifies.
- NetworkPartition uses `silent` features in entry; others use `timeout_rate` / `latency_p99_ratio`.
- The Loss > Corrupt > Duplicate severity ordering is reflected in band lower bounds (Loss tightest, Duplicate loosest).
- At most one within-family hand-off (Loss → Partition); no speculative cross-family hand-offs.
