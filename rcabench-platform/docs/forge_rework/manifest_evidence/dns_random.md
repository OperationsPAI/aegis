# DNSRandom — manifest evidence

## Family ambiguity acknowledgment

Family F (DNS / Time) is the most ambiguous of the manifest families.
Name-pattern scan of `/home/ddq/AoyangSpace/dataset/rca/` returned a
single DNS case (`ts2-ts-station-service-dns-nn49s2`), and that one
case is a `dnschaos` action whose `display_config` does not
distinguish `error` vs `random` mode at the file-name level. As a
result, DNSRandom has effectively **0 confirmed sample cases**;
all bands are `magnitude_source: theoretical`. Acknowledged per
`phase2_template.md`.

## Mechanism

dnschaos with `action: random` returns syntactically valid IPs that
don't correspond to the requested service. The DNS lookup itself
returns success; the symptom surfaces one network-stack hop later
when the caller dials the bogus address. Outcomes are heterogeneous
and depend on what host happens to live at the random IP:

- **No listener** → connection refused (RST).
- **Random unreachable** → SYN timeout, the most common case.
- **Random reachable** with TLS / protocol mismatch → handshake
  failure observed as connect-time error or short-lived span.
- **Rare collision** → call succeeds against the wrong target.

Like DNSError, this is outbound-isolation: the injected pod
continues to serve inbound traffic, so `pod.silent` is the wrong
entry primitive.

## Entry signature choices

DNSRandom's lookup-step does NOT fail, so `dns_failure_rate` is a
poor required feature. We use generic egress error / timeout /
refused-connection rates, all from the bootstrap vocabulary:

- `required_features = [span.error_rate ≥ 0.1]` — the most
  fault-mode-agnostic primitive.
- `optional_features` cover the three observable downstream modes
  (`connection_refused_rate`, `timeout_rate`, residual
  `dns_failure_rate`) with `optional_min_match = 1`. At least one of
  the three should fire for the entry to be admitted.

These bands are theoretical floors above plausible background;
calibration against observed cases is deferred until more DNSRandom
samples exist.

## Derivation layers

- **Layer 1** (immediate callers / dependents): same egress error /
  timeout / refused signature as the entry, just with weaker
  thresholds.
- **Layer 2** (transitive callers): error rate ≥ 0.05 AND/OR
  latency p99 ratio ≥ 1.5 (because timeouts that don't propagate
  as errors still bloat tail latency).

`max_fanout = 16` to limit per-layer expansion and avoid sweeping
in unrelated background error spans.

## Hand-off rationale

`DNSRandom → NetworkPartition` on `span.silent`. Permitted by the
universal cross-family trigger (`span.silent` →
`NetworkPartition` / `DNSError`). When the random IP routes into a
black hole that swallows packets without RST, the caller pattern
becomes indistinguishable from a partition between caller and
target.

## Sample cases consulted

Strictly speaking, none. The single DNS case in the canonical
dataset (`ts2-ts-station-service-dns-nn49s2`) was not parsed beyond
its `injection.json` to verify the `action` field. Bands are
purely mechanism-derived. **Flagged for Phase 4.**

## Confidence

**Low.** DNSRandom's symptom space is the broadest in this family,
the dataset gives us nothing to calibrate against, and the
required-feature is generic (`error_rate`) which is shared with many
other fault types. If Phase 4 validation shows DNSRandom collisions
with HTTP* fault types in the ranking, the entry signature should
be tightened with a feature like
`outbound_connect_failure_rate` (vocabulary extension; flagged but
not requested in this round to keep within bootstrap).
