# HTTPResponsePatchBody — manifest evidence

## Mechanism

The chaos sidecar applies a JSON Patch (RFC 6902) — typically a
field-level rewrite — to the outbound response body. Status code
remains 2xx. Effect is even more conditional than ReplaceBody: only
callers that read the patched fields and validate them against
expectation will see a failure. Callers that ignore those fields
proceed normally.

## Entry-signature feature choice

`error_rate` at span level with a low threshold (0.1). Same
ambiguity caveat as ReplaceBody but with strictly weaker signal: a
full-body replacement at least breaks deserialization for typed
clients, while a single-field patch may still parse correctly.

## Magnitude bands

**Theoretical, low-confidence**. The dataset only contains 2 sample
cases for this fault type (below the ≥ 3 threshold for empirical
calibration), so all bands are theoretical and we explicitly note that
verification recall on this fault type may be lower than peers in the
family. Mark as low-confidence in any downstream report.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. No layer 2.
No hand-offs.

## Sample cases (only 2 found in dataset — below the empirical threshold)

- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-food-service-response-patch-body-qjhx5h/`
- (one additional `*-response-patch-body-*` case observed in the
  directory listing; see `phase2_C_http.md` sample-counter output)

Sample shortage is the reason for the all-theoretical band. If more
patch-body cases land in the dataset later, this manifest should be
re-calibrated empirically.
