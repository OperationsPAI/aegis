# HTTPRequestReplaceMethod — manifest evidence

## Mechanism

The chaos sidecar rewrites the inbound HTTP method (e.g., GET → DELETE)
so the target service either returns 405 Method Not Allowed or 404 if
no route matches the new method. Failures are immediate; latency is
unaffected.

## Entry-signature feature choice

`error_rate` at span level. As with ReplacePath, the bootstrap feature
vocabulary does not provide a 405- or 4xx-specific feature, so we fall
back to plain error_rate. Documented as a vocabulary gap.

## Magnitude bands

Theoretical. Entry lower bound 0.3 — same reasoning as ReplacePath:
some endpoints accept both verbs and a fraction of traffic survives the
mangling. Still well above background variation.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. No layer 2.
No hand-offs.

## Sample cases (≥ 3 found, 24 total `*-request-replace-method-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-route-plan-service-request-replace-method-lrzhl6/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-seat-service-request-replace-method-dchngw/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-security-service-request-replace-method-j6gpxx/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-auth-service-request-replace-method-mqrzv4/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-route-plan-service-request-replace-method-bn6rxm/`

Empirical magnitude extraction not performed; bands theoretical.
