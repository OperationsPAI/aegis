# HTTPRequestAbort — manifest evidence

## Mechanism

The chaos sidecar intercepts inbound HTTP requests on the target service
and aborts the connection before the request reaches the application
handler. The caller observes a connection drop / 502 / network error and
the affected service's inbound spans are recorded as errors. CPU,
memory, GC, and other resource counters are unaffected because the
service code never runs.

## Entry-signature feature choice

`error_rate` at span level is the dominant signal: aborted requests are
counted as error spans by the trace collector. Because abort is
immediate, latency does not inflate, so `latency_p99_ratio` is
deliberately not part of `required_features`. `connection_refused_rate`
and a depressed `request_count_ratio` are listed as optional supporting
evidence (a reachable-but-aborting endpoint reduces successfully
completed request count).

## Magnitude bands

Bands are theoretical. Mechanism: when the abort policy fires it
applies to a high fraction of requests inside the window (the
`display_config.duration` is in minutes and chaos-mesh injects on every
matching request), so observed `error_rate` clusters near 1.0. We use
`[0.5, 1.0]` for the entry band (lower bound 0.5 to leave headroom for
partial duration overlap with the entry window) and `[0.2, 1.0]` at
layer 1 because an upstream caller's traffic mix dilutes the signal.

## Cascade and hand-offs

Layer 1 captures upstream callers via `calls backward`. We do not add
layer 2: HTTP errors typically do not propagate two hops because
intermediate callers either retry, fail fast, or convert the error into
a different code. No cross-family hand-offs were added (PLAN risk
register restricts cross-family hand-offs to the two universal triggers,
and HTTPRequestAbort is itself a candidate destination for those
triggers via HTTPResponseAbort / HTTPResponseReplaceCode).

## Sample cases (≥ 3 found, 25 total `*-request-abort-*` cases)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-basic-service-request-abort-bgq9qs/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-preserve-service-request-abort-lpql72/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-route-plan-service-request-abort-x2bhww/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel2-service-request-abort-nnvxn4/`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-service-request-abort-6nm66v/`

Empirical magnitude extraction was not performed in this pass; bands
remain theoretical. The injection.json in each case shows
`display_config.duration` in minutes and `injection_point.app_name` set
to the targeted service.
