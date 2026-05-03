# HTTPResponseReplaceCode — manifest evidence

## Mechanism

The chaos sidecar overwrites the outbound HTTP status line so the
caller sees a non-2xx code (commonly 500 or 503). The body and timing
are otherwise normal. Callers count the span as an error
unconditionally because the status code is the dominant error signal in
nearly every HTTP client library and tracing collector.

## Entry-signature feature choice

`error_rate` at span level. Threshold is **0.2** (relaxed from 0.5)
because:

1. Family file requires this manifest to accept
   `span.error_rate >= 0.2` so cross-family universal hand-offs land
   here cleanly.
2. Rewrite policies in chaos-mesh can be probabilistic (e.g., apply
   to 30 % of responses), so observed error_rate is sometimes bounded
   well below 1.0.

## Magnitude bands

Theoretical. Entry `[0.2, 1.0]`. Layer 1 lower 0.1 to allow for
caller-side dilution.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. No layer 2.
No outbound hand-offs.

This manifest is, alongside HTTPResponseAbort, a canonical
**destination** for cross-family universal hand-offs. Other families'
manifests whose terminal effect is "downstream service returns errors"
should hand off to one of these two manifests via the
`span.error_rate > 0.2` trigger; the choice between abort vs.
replace-code is heuristic (abort matches connection-level failures;
replace-code matches application-level failures).

## Sample cases (≥ 3 found, 28 total `*-response-replace-code-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-security-service-response-replace-code-fbsfls/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-plan-service-response-replace-code-7ps8tm/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-auth-service-response-replace-code-xn6gk2/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-basic-service-response-replace-code-6d6shc/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-preserve-service-response-replace-code-bndht9/`

Empirical magnitude extraction not performed; bands theoretical.
