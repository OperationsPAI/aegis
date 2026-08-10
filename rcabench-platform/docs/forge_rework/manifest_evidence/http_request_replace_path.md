# HTTPRequestReplacePath — manifest evidence

## Mechanism

The chaos sidecar rewrites the inbound request's URL path so it no
longer matches a registered route. The target service returns 404 (or
4xx via framework default handler). Failures are immediate; latency is
not affected.

## Entry-signature feature choice

`error_rate` at span level. The bootstrap feature vocabulary in
SCHEMA.md does **not** include a 4xx-specific span feature, so we fall
back to plain error_rate per the family file's instruction. This is a
known gap and is documented here. If a future revision adds
`status_4xx_rate` we should pin the entry band on it directly to
distinguish path-mangling from broader abort patterns.

## Magnitude bands

Theoretical. The entry lower bound is 0.3 (not 0.5 as for abort)
because some path-rewrite policies in chaos-mesh apply per-request
probabilistically and a non-trivial fraction of callers may use a path
that escapes the rewrite rule (e.g., other endpoints on the same
service). 0.3 still rules out background variation, which sits well
below 0.05 in TrainTicket steady state.

`latency_p99_ratio` is constrained to `[0.0, 2.0]` in optional_features
to differentiate this manifest from a delay manifest at match time.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. No layer 2.
No hand-offs.

## Sample cases (≥ 3 found, 17 total `*-request-replace-path-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-basic-service-request-replace-path-8q599t/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-preserve-service-request-replace-path-jhv25f/`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-travel-plan-service-request-replace-path-zdh4tb/`
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-basic-service-request-replace-path-flkd7v/`

Empirical magnitude extraction not performed; bands theoretical.
