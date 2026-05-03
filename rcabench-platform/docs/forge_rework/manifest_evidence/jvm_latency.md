# JVMLatency — Evidence

## Mechanism summary

JVM method-level latency injection: the chaos agent rewrites a specific Java
method on the affected container to insert an artificial sleep before its
body runs. The injection JSON exposes `display_config.injection_point`
(`app_name`, `class_name`, `method_name`) and `display_config.latency_duration`
in milliseconds. Only spans whose call stack crosses the patched method see
the latency; sibling methods on the same service are unaffected.

## Granularity gap (method vs service) — explicit

The ground truth is method-scoped (e.g.
`route.service.RouteServiceImpl.getRouteById`), but the FORGE manifest
matches at **service** granularity because the IR currently rolls span
metrics per `service_name`, not per operation. Consequence:

- Cases where the injected method is on a hot path produce strong service-
  level signal (e.g. `ts1-ts-route-service-latency-vhk7dz`: P99 ratio 294x).
- Cases where the injected method is rarely called produce weak or no
  service-level signal (e.g. `ts1-ts-config-service-latency-5kkcrc`,
  `deleteConfig` is rarely exercised; service P99 ratio 0.87x — manifest
  will under-attribute these). This coarsening is acknowledged as a known
  limitation; per-operation buckets are deferred to a future IR rework.

## Entry signature

`required: span.latency_p99_ratio in [5.0, .inf]` per Family-E task spec
("entry = span.latency_p99_ratio >= 5.0 on the affected service").

Empirically, across 4 cases that hit hot paths, observed P99 ratios were
{30.6, 41.5, 136.3, 293.9}. p5/1.25 = 24.5, well above the 5.0 threshold,
so 5.0 is a conservative lower bound that still excludes natural traffic
spikes (typically <2x P99 inflation).

## Derivation layers

- **Layer 1** (immediate callers, `calls backward` + `includes backward`):
  expects 2x P99 ratio (decay 0.4 from 5.0). Java callers that block on the
  patched method inherit ~all of its added latency, so 2x is conservative.
- **Layer 2** (transitive callers): 1.3x P99 — Java services often catch /
  short-circuit propagation, so the cascade rarely reaches 3+ hops with
  amplitude.

Optional `timeout_rate` and `error_rate` capture the case where injected
latency exceeds the caller's deadline, which empirically does not happen in
TrainTicket's 4-minute window but is mechanism-plausible.

## Hand-offs

- `HTTPResponseDelay` at p99_ratio >= 10.0 (layer 1): when the caller's
  read deadline is exceeded, downstream the symptom is response delay.
- `HTTPResponseAbort` at error_rate > 0.2 (layer 2): universal cross-family
  trigger.

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-config-service-latency-5kkcrc` (latency_duration=601 ms; cold method, no signal)
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-route-service-latency-vhk7dz` (latency_duration=3169 ms; P99 ratio 294x)
- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-train-service-latency-5gvbsq` (latency_duration=3590 ms; P99 ratio 136x)
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-travel2-service-latency-bxgxm9` (P99 ratio 41x)
- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-consign-service-latency-g5fj65` (P99 ratio 30.6x)
