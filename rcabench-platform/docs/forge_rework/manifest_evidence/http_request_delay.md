# HTTPRequestDelay — manifest evidence

## Mechanism

The chaos sidecar holds inbound HTTP requests for a configured
`delay_duration` (milliseconds) before forwarding to the application
handler. The caller observes a slow but successful request: P99 latency
inflates by roughly the delay amount; the application processes
normally so error_rate remains low (until callers' timeout budgets are
exceeded).

## Entry-signature feature choice

`latency_p99_ratio` at the span level is the dominant signal. Errors
are not expected unless the configured delay exceeds caller timeout. We
intentionally pin `error_rate` low in `optional_features` (band
`[0.0, 0.2]`) so that an injection that produces both delay and high
errors is steered toward the abort manifest rather than this one.

## Magnitude bands

Bands are theoretical, derived from chaos-mesh injection params we
inspected. Example: in
`ts0-ts-travel2-service-request-delay-lzpl9v/injection.json`,
`display_config.delay_duration = 3512` ms. Typical TrainTicket span
P99 baselines are O(50–200 ms), so the expected `latency_p99_ratio`
during injection is at least 17×. We set the entry lower bound at 5.0
to leave generous safety margin against shorter-delay injections and
windowing artifacts. Layer 1 lower is 2.5 (× decay 0.5) to catch
upstream callers whose own P99 is dominated by, but not equal to, the
delayed downstream call.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. Tempting
hand-off `HTTPResponseDelay → CPUStress` (downstream callers' threads
block) is **not** added: it is the wrong direction (CPU stress is
upstream of HTTP delay typically) and the family file calls this out as
a "do not add without empirical evidence" trap. No cross-family
hand-offs were added.

## Sample cases (≥ 3 found, 14 total `*-request-delay-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel2-service-request-delay-lzpl9v/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-basic-service-request-delay-9w85fg/`
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-consign-service-delay-l8j2tw/` (general delay variant)

Empirical magnitude extraction not performed; bands theoretical.
Injection-param inspection of the cited cases confirms
`delay_duration` ≥ 3000 ms typical.
