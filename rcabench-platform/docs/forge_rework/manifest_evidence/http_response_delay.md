# HTTPResponseDelay — manifest evidence

## Mechanism

The chaos sidecar holds outbound responses for `delay_duration` ms
before allowing them to reach the caller. Caller spans show inflated
P99 latency; error_rate stays low when the caller's timeout exceeds
the injected delay, and rises only when the delay exceeds the budget.

## Entry-signature feature choice

`latency_p99_ratio` at the span level. Same rationale as
HTTPRequestDelay: errors are only a side effect.

## Magnitude bands

Theoretical, calibrated on `delay_duration` from sample injection.json
files (typically ≥ 3000 ms; baselines O(50–200 ms) → ratio ≥ 15×).
Entry lower bound 5.0 leaves margin; layer 1 lower 2.5 with decay 0.5.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. The family
file flags `HTTPResponseDelay → CPUStress` as a tempting-but-wrong
hand-off (CPU stress is upstream, not downstream, of HTTP delay), so
we explicitly do **not** add it. No cross-family hand-offs.

## Sample cases (≥ 3 found, 19 total `*-response-delay-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-plan-service-response-delay-pfwcqk/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-seat-service-response-delay-cxg9cc/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-station-service-delay-hwcd55/` (delay variant)
- `/home/ddq/AoyangSpace/dataset/rca/ts2-mysql-delay-d427wn/` (cross-system delay reference)

Empirical magnitude extraction not performed; bands theoretical.
