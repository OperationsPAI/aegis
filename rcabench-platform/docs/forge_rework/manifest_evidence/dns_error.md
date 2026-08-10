# DNSError — manifest evidence

## Family ambiguity acknowledgment

Family F (DNS / Time) is the most ambiguous of the six manifest families.
Sample injection cases for DNSError are sparse in the canonical dataset
(`/home/ddq/AoyangSpace/dataset/rca/`): a name-pattern scan
(`grep -i 'dns'`) returned exactly **1** case
(`ts2-ts-station-service-dns-nn49s2`), well below the per-fault-type
calibration threshold of 3 cases required by `phase2_template.md`.

Per the template's discipline ("If <3 cases exist, mark all bands
`magnitude_source: theoretical` and document the gap"), every band in
`dns_error.yaml` is `magnitude_source: theoretical`. Bands are derived
from the chaos mechanism, not from observed percentiles.

## Mechanism

dnschaos with `action: error` injected at a pod returns NXDOMAIN /
SERVFAIL for outbound DNS queries from the targeted pod. The pod itself
continues to serve inbound traffic and remains live; only its egress
calls that perform a fresh DNS lookup fail at the resolver step. The
canonical signature is therefore on the **caller-side spans** of egress
calls FROM the injected pod, not on the injected pod's inbound spans.
This is why `pod.silent` is NOT used as the entry signature — using
pod silence would systematically miss the symptom and wrongly classify
DNSError injections as `ineffective`.

## Entry signature choices

- `required_features = [span.dns_failure_rate ≥ 0.2]`. The
  `dns_failure_rate` feature is in the bootstrap vocabulary
  (SCHEMA.md, "Feature vocabulary" table). 0.2 is a theoretical floor:
  if more than 20% of egress spans from the pod report DNS-resolver
  errors during a 30-second window, that is well above any plausible
  background rate (typical healthy clusters have DNS error rates near
  zero except under control-plane stress).
- `optional_features = [span.error_rate ≥ 0.1, span.silent = true]`
  with `optional_min_match = 1`. Captures the case where DNS errors
  surface as generic span errors (caller doesn't tag DNS failures as
  such) or as caller silence (caller drops without recording a span).

## Derivation layers

- **Layer 1** (caller's outbound spans, callee's inbound spans): the
  callers that depend on the blocked egress will see immediate fast
  failures — `dns_failure_rate ≥ 0.1` (decay tolerated since errors may
  be partially masked by cache or local resolver fallback) and/or
  `error_rate ≥ 0.1`.
- **Layer 2** (transitive callers, backward `calls`): retries and
  fallback masking flatten the signal; we only require
  `error_rate ≥ 0.05` or `timeout_rate ≥ 0.05`. We deliberately do not
  go deeper.

`max_fanout` is set to 16 (rather than the schema default 32) because
DNS faults are localized and a wide fanout would let layer-1
expansion sweep in unrelated background errors.

## Hand-off rationale

`DNSError → NetworkPartition` on `span.silent`. Permitted by the
universal cross-family trigger (`span.silent` → `NetworkPartition` or
`DNSError`, see `phase2_template.md` "Hand-off authoring rules") and
recommended by `phase2_F_dns_time.md` ("DNSError → NetworkPartition —
both produce silent + caller error patterns"). When all caller spans
go fully silent (no error span emitted, just absence), the symptom
becomes indistinguishable from a partition and verification continues
under NetworkPartition's manifest.

## Sample cases consulted

- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-station-service-dns-nn49s2`
  (`fault_type: 15`, dnschaos against `mysql` egress from
  `ts-station-service`; `display_config.duration: 4` minutes;
  `display_config.injection_point.domain: mysql`).

Only one case — bands cannot be calibrated empirically. Flagged.

## Confidence

**Medium.** Mechanism is well-understood and `dns_failure_rate` is a
clean feature, but the empirical band calibration is missing. If
Phase 4 validation flags low recall on DNSError, the
`dns_failure_rate ≥ 0.2` threshold should be the first knob to
re-tune against observed values once more samples are collected.
