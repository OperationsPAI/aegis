# HTTPResponseAbort — manifest evidence

## Mechanism

The chaos sidecar aborts the outbound HTTP response after the target
service has produced it. The caller observes a connection reset or
truncated response and counts the span as an error. The target
service's own application code ran successfully, so server-side spans
may even look healthy; the failure manifests on the caller side.

## Entry-signature feature choice

`error_rate` at span level. The threshold is intentionally relaxed to
**0.2** (as opposed to the 0.5 used for HTTPRequestAbort) because:

1. The family file `phase2_C_http.md` requires the entry signature to
   accept `span.error_rate >= 0.2` so that cross-family universal
   hand-offs (`span.error_rate > 0.2`) can land on this manifest.
2. Response-abort traffic is mediated by the caller's connection pool
   and retry policy, so observed error_rate is often diluted relative
   to request-side abort.

## Magnitude bands

Theoretical. Entry `[0.2, 1.0]` per the cross-family hand-off
requirement. Layer 1 lower 0.1 because callers further upstream see
even more dilution.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. No layer 2.
No outbound hand-offs.

This manifest is a **destination** for cross-family hand-offs from
families A/B/D/E/F whose effects bottom out as "downstream service
returns errors". The `entry_signature.required_features` shape was
chosen specifically to be reachable by the universal trigger
`span.error_rate > 0.2`.

## Sample cases (≥ 3 found, 21 total `*-response-abort-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-preserve-service-response-abort-7gp2mq/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-preserve-service-response-abort-mj9pbn/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-seat-service-response-abort-fddpcv/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-seat-service-response-abort-nggfmq/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-travel-service-response-abort-mqhzdf/`

Empirical magnitude extraction not performed; bands theoretical.
