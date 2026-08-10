# TimeSkew — manifest evidence

## Family ambiguity acknowledgment

Family F (DNS / Time) is the most ambiguous family. TimeSkew is
the **most ambiguous fault type within the most ambiguous family**.
Many TimeSkew injections have no telemetry signature at all and
should legitimately be classified as `ineffective` outcome — that
is the documented expectation in `phase2_F_dns_time.md` ("Many
TimeSkew injections will land as `ineffective` outcome — that's
expected. Loose entry signature.").

Name-pattern scan of `/home/ddq/AoyangSpace/dataset/rca/` returned
2 time cases (`ts0-ts-travel-plan-service-time-rjdx4x`,
`ts1-ts-consign-service-time-hslmgs`), below the 3-case threshold.
All bands are `magnitude_source: theoretical`. **Confidence is LOW.**

## Mechanism

timechaos shifts the container's monotonic / wall clock by a
configured `time_offset` (e.g., `-84` seconds in
`ts0-…-rjdx4x`). The fault is silent unless application code:

- Validates token / JWT / cookie expiry against the now-skewed clock.
- Compares timestamps for cache TTL, dedup windows, idempotency keys.
- Schedules work using local time (cron, retries with backoff).
- Logs / traces with timestamps that downstream systems reject as
  "out of allowed clock-skew window".

Notable evidence from sampled cases: the `injection.json` of
`ts0-…-rjdx4x` lists `ground_truth.span: null` — i.e., the
ground-truth annotators could NOT identify a span-level signature
for the fault, consistent with TimeSkew often being telemetry-silent.
This case shaped the decision to keep the manifest very loose and
to expect `ineffective` outcomes.

## Entry signature choices

`required_features = [span.error_rate ≥ 0.05]`. Floor of 5% is
above noise but well below any specific fault-pattern threshold.
The orchestrator separately requires a concurrent
`do(v_root, TimeSkew)` injection record, so we do not need a
discriminative entry signature; we only need to admit cases where
*something* is happening at v_root. If `error_rate` is below 0.05
during the entry window, classification of `ineffective` is the
correct outcome and the manifest should not match.

`optional_features` cover the two most plausible non-error modes
(latency bloat from clock-related timeouts, generic timeouts), with
`optional_min_match = 0` — i.e., they're hints for verification
rather than admission gates.

`entry_window_sec = 60` (vs. default 30) because clock-shift
effects are sometimes paced by token refresh cycles rather than
immediate, and the upper schema limit is 60.

## Derivation layers

**Layer 1 only.** The cascade is application-specific and
unbounded in shape; going to layer 2 invites false positives by
sweeping in background errors. We accept `error_rate ≥ 0.05` at
the immediate caller / dependent and stop. `max_fanout = 8` is
intentionally tight.

## Hand-offs

**None.** `phase2_F_dns_time.md` lists `TimeSkew → JVMException`
as a candidate "only if data supports". The 2 sampled cases do
not show JVM exception traces (one has `ground_truth.span: null`,
the other was not parsed in detail), so this hand-off is omitted
for now. If Phase 4 validation finds frequent auth/token error
patterns, this hand-off should be added with rationale.

## Sample cases consulted

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-plan-service-time-rjdx4x`
  (`fault_type: 16`, `time_offset: -84`s,
  `injection_point.container_name: ts-travel-plan-service`,
  `ground_truth.span: null`).
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-consign-service-time-hslmgs`
  (named, not parsed in detail).

Insufficient for empirical calibration — flagged.

## Confidence

**LOW.** This manifest is intentionally loose. Expected behavior:

- Most TimeSkew injections classify as `ineffective` (correct).
- A minority that hit auth/token/TTL paths classify as `attributed`
  with a layer-1 cascade (correct, low-confidence).
- Background auth-error noise risks false positives — mitigated by
  the orchestrator-side requirement that a TimeSkew injection
  record actually exist concurrently. Without that gate this
  manifest is too permissive on its own.

Re-tuning priorities for Phase 4: (a) require empirical
`error_rate` band calibration once more samples exist; (b)
add `TimeSkew → JVMException` hand-off if data supports.
