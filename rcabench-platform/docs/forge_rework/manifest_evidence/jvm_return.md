# JVMReturn — Evidence

## Mechanism summary

JVM method-level forced-return injection: a specific method on the affected
container is rewritten to skip its body and return a fixed value (often
null, 0, or empty collection — see `display_config.return_type` and
`return_value_opt`). The caller may treat this as success (silent
corruption) or fault (NullPointerException / ClassCastException).

## Granularity gap (method vs service) — explicit

Ground truth is method-scoped; manifest matches at service granularity
because the IR rolls span metrics per service. Where the patched method is
on a cold path, service-level error_rate stays near baseline (no signal);
where it is on a hot path, the cascade produces JVMException-shaped errors
at the GT service or at the caller.

## Silent-vs-cascade ambiguity — explicit

Across 4 dataset cases, observed GT-service error_rate is {0.357, 0.0,
0.667, 0.22}, span 0–67%. The bimodality reflects the inherent ambiguity:

- `ts3-ts-station-service-return-4z45w8` (return_type=1, return_value=0,
  method=`StationApplication.main`): error_rate=0 — silent absorption /
  cold method. GT-service P99 ratio jumped 5x though, suggesting some
  caller noticed but recovered.
- `ts1-ts-travel-plan-service-return-kp5bqw` (`getServiceUrl`):
  error_rate=0.667 — caller dereferenced bogus URL, classic NPE cascade.
- `ts2-ts-consign-service-return-rc77nz` (`restTemplate`): error_rate=0.357 —
  intermediate. Some callers crashed, others retried.

Implication: the entry signature must be lenient (`error_rate >= 0.10`
rather than the 0.30 used for JVMException) to admit partial-cascade cases,
while accepting that fully-silent cases will simply not match this manifest
and will be classified as Class E (silent absorption) by the verification
loop. This is a deliberate manifest-design choice, not a defect.

## Entry signature

`required: span.error_rate in [0.10, 1.0]` (empirical, lenient).

Optional `latency_p99_ratio in [1.5, .inf]` and `request_count_ratio in
[0.0, 0.8]` are provided to admit cases that are mostly silent but show
up as latency spikes (caller retried) or as request-volume drops (caller
short-circuits). `request_count_ratio` upper bound 0.8 = "at most 80% of
baseline volume" since chaos-touched calls fall out of the success path.

## Derivation layers

- **Layer 1** (callers): error_rate >= 0.05 OR latency_p99_ratio >= 1.3 —
  catches both NPE-cascading callers and retry-burdened callers.
- **Layer 2** (transitive callers): error_rate >= 0.05 only.

## Hand-offs

- `JVMException` at layer 1 when caller error_rate >= 0.3 (within-family):
  caller's NPE / dereference failure is observationally identical to a
  direct JVMException injection at that node. This hand-off is the
  programmatic encoding of the cascade mode.
- `HTTPResponseAbort` at layer 2 (universal trigger).

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-consign-service-return-rc77nz`
  (return_type=2, return_value_opt=1; partial cascade, error_rate=0.357)
- `/home/ddq/AoyangSpace/dataset/rca/ts3-ts-station-service-return-4z45w8`
  (return_type=1, value=0; silent — error_rate=0, but P99 ratio 5x)
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-travel-plan-service-return-kp5bqw`
  (return_type=1, value_opt=1; full cascade, error_rate=0.667)
- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-travel-service-return-j872pp`
  (intermediate, error_rate=0.22)
