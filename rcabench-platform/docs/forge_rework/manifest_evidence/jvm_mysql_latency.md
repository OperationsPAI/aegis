# JVMMySQLLatency — Evidence

## Mechanism summary

JVM-side JDBC latency injection: only spans crossing the JDBC call boundary
on the affected container experience an artificial sleep before the JDBC
call returns. `display_config.latency_ms` carries the duration; observed
3669 ms in the single dataset case. Methods that do not touch the DB are
unaffected.

## Granularity gap (method vs service AND DB-touching scope) — explicit

This is a **double** granularity gap:

1. The injection is method-scoped (specifically the JDBC bridge), but the
   manifest matches at service granularity.
2. Even at the service level, only DB-touching spans show the effect; non-
   DB spans on the same service stay healthy.

Mitigation: the GT service for JVMMySQL faults is typically a service that
fronts MySQL (e.g. `ts-travel-service` accessing the `trip` table — see
the single dataset case `ts0-ts-travel-service-mysql-28wmss`). When the
GT service's overall span volume is dominated by DB-touching calls, the
service-level P99 inflation is visible; otherwise it is diluted. The
manifest accepts this dilution because the alternative (per-operation
matching) requires IR features we do not yet have.

## Distinguishing context from JVMLatency

JVMMySQLLatency and JVMLatency produce nearly identical service-level
signatures (high P99 ratio, no error rate). The discriminator at
verification time is the GT-service ground truth field
`ground_truth.service` containing `mysql` (the case
`ts0-ts-travel-service-mysql-28wmss` lists both `ts-travel-service` and
`mysql`). This is a context cue, not a manifest-internal feature, and is
expected to be resolved by the verification engine through topology
membership (the affected service has an outbound edge to the `mysql`
service).

## Entry signature — bands are theoretical

Per phase2_template.md rule, with <3 sample cases all bands are
`magnitude_source: theoretical`. The single observed case had
latency_ms=3669; lower bound 5.0 mirrors JVMLatency to keep the family
consistent.

## Derivation layers

Same shape as JVMLatency (callers see propagated latency, layer 2 may show
error if timeouts trip). DB-side amplification (mysql container
saturation) is left to the cascading hand-offs since per-DB metrics are
not yet in the IR vocabulary.

## Hand-offs

- `HTTPResponseDelay` at p99_ratio >= 10.0 (layer 1): mirrors JVMLatency
  hand-off; the downstream signature is identical once the DB-touch
  context is no longer visible to far callers.

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-service-mysql-28wmss`
  (latency_ms=3669, table=trip, operation=SELECT; only dataset case).
- **Gap acknowledged**: zero additional fault_type=29 cases exist in the
  dataset as of authoring; bands cannot be empirically calibrated.
  Recommend re-calibration when the dataset is extended.
