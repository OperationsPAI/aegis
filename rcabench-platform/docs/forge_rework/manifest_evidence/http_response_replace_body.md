# HTTPResponseReplaceBody — manifest evidence

## Mechanism

The chaos sidecar replaces the outbound HTTP response body with
attacker-supplied content (usually a fixed string or empty object).
The HTTP status code remains 2xx — only the payload changes. Whether
this surfaces as an error depends entirely on the caller:

- Callers that deserialize and validate the payload will fail (e.g.,
  JSON parse error, schema validation failure → application-level
  error).
- Callers that ignore the body, or that only check the status code,
  will register a successful span.

This manifest is therefore intrinsically ambiguous and is documented
as low-confidence.

## Entry-signature feature choice

`error_rate` at span level, with a low threshold of 0.1 reflecting the
expected partial caller-fail rate. We do not require any latency
inflation; the body-rewrite path adds negligible time.

## Magnitude bands

Theoretical. Lower bound 0.1 is a deliberate compromise: lower than
abort manifests (because the failure is conditional on caller
behavior) but high enough to clear background variation. This is
explicitly low-confidence — verification may legitimately miss cases
where every caller tolerates the mangled body.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. No layer 2.
No hand-offs.

## Sample cases (≥ 3 found, 18 total `*-response-replace-body-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-basic-service-response-replace-body-85cnwx/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-preserve-service-response-replace-body-644lf4/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-plan-service-response-replace-body-mns47j/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-seat-service-response-replace-body-hvd8x2/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-travel2-service-response-replace-body-wsbwjq/`

Empirical magnitude extraction not performed; bands theoretical.
