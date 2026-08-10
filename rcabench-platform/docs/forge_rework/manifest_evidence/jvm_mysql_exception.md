# JVMMySQLException — Evidence

## Mechanism summary

JVM-side JDBC exception injection: only spans crossing the JDBC call
boundary on the affected container raise a SQLException-class error.
Methods that do not touch the DB are unaffected. Caller services that
catch the SQLException may retry or wrap-and-rethrow as a generic
RuntimeException; uncaught it surfaces as an HTTP 500 at the service
boundary.

## Granularity gap (method vs service AND DB-touching scope) — explicit

Same double gap as JVMMySQLLatency:

1. Method-scoped injection vs service-scoped manifest.
2. Only DB-touching spans see the effect; non-DB spans on the same
   service stay healthy.

When the GT service is DB-heavy (e.g. order/booking/inventory services in
TrainTicket), service-level error_rate is dominated by failed DB calls and
the manifest matches well. For DB-light services the signal is diluted.

## Distinguishing context from JVMException

JVMMySQLException vs JVMException produce nearly identical caller-layer
error_rate signatures. The discriminator is the same as for the latency
pair: the GT service has an outbound topology edge to the `mysql` service,
and `ground_truth.service` typically lists both. The verification engine
uses this topology context to disambiguate; the manifest itself does not
encode the discriminator.

## Entry signature — bands are theoretical

Zero dataset cases of fault_type=30 (`JVMMySQLException`) exist as of
authoring. All bands are `magnitude_source: theoretical` and chosen to
mirror JVMException (which is the closest mechanistic analogue): a
SQLException at the JDBC bridge presents at the service span boundary
the same way an injected RuntimeException at any method does, modulo the
DB-touching scope filter.

`required: span.error_rate in [0.30, 1.0]` — same as JVMException, since
mechanism-wise it is "throw at one specific kind of method" and the
caller-layer reaction is identical.

## Derivation layers

Mirrors JVMException: 2 layers, layer 1 expects error_rate >= 0.10 with
slight latency inflation, layer 2 expects residual error >= 0.05.

## Hand-offs

- `JVMException` (within-family) at error_rate >= 0.3 layer 1: callers
  that wrap-and-rethrow produce a JVMException-shaped span at their own
  boundary, so the cascade is observationally a JVMException at the
  caller node.
- `HTTPResponseAbort` at error_rate >= 0.5 layer 2 (universal trigger).

## Sample cases

- **Gap acknowledged**: zero dataset cases of fault_type=30 exist; the
  manifest is purely mechanism-derived. Bands MUST be re-calibrated
  empirically once cases are collected.
- Adjacent evidence: JVMException cases (error_rate ~0.65) and the single
  JVMMySQLLatency case (`ts0-ts-travel-service-mysql-28wmss`) inform the
  general shape; JVMException's empirical band is reused unchanged.
