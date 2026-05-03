# Phase 2 Family A — Lifecycle faults

**Owner**: 1 agent (`mfst-A`)
**Worktree**: yes
**Reads first**: `phase2_template.md`, then `SCHEMA.md`

## Fault types in scope (3)

| fault_type_name | seed_tier | target_kind | mechanism |
|---|---|---|---|
| `PodKill` | unavailable | pod | Pod terminated by chaos-mesh; restart cycle visible |
| `PodFailure` | unavailable | pod | Pod stops serving without termination; readiness probe fails |
| `ContainerKill` | unavailable | container | Single container in pod terminated; pod may survive |

## Sample injection directories (use these to calibrate)

```
/home/ddq/AoyangSpace/dataset/rca/ts4-ts-assurance-service-pod-kill-98r2br
/home/ddq/AoyangSpace/dataset/rca/ts5-ts-contacts-service-pod-kill-srvmct
/home/ddq/AoyangSpace/dataset/rca/ts0-mysql-container-kill-9t6n24
```
Plus all directories matching `*-pod-kill-*`, `*-pod-failure-*`, `*-container-kill-*` patterns. Pick 3–5 per fault type.

## Family-specific guidance

### PodKill / PodFailure / ContainerKill share traits

- Entry signature pivots on **discrete events** (`restart_count > 0`, `unavailable: true`) rather than ratios. Use the boolean / absolute features.
- Downstream span effects are dominated by `silent` and `connection_refused_rate` rather than latency.
- ContainerKill is gentler than PodKill: the pod stays scheduled, just one container restarts. Its entry should require `container.restart_count >= 1` AND NOT require pod-level `unavailable`.
- PodKill / PodFailure: require `pod.unavailable: true` in the entry window.

### Hand-off candidates

- PodKill → DNSError when service has only one healthy pod and removal causes inbound DNS lookups to time out (rare; check empirically before adding).
- ContainerKill → HTTPResponseAbort when the dead container had in-flight requests.

### Notes on derivation layers

- Layer 1 should target `service` and `span` kinds via `routes_to backward` + `includes forward`. Expected features: `silent` for service, `silent` or `error_rate` for span.
- Layer 2 (caller spans): `error_rate` and `connection_refused_rate` — NOT latency, because killed pods produce immediate failures, not slowdowns.
- Max layer 3 typically (service drop usually doesn't propagate beyond 2–3 hops before circuit breakers kick in).

## Acceptance bar (family A)

Beyond the common protocol:

- All three manifests must use `silent` or `unavailable` features in entry_signature, NOT latency-based features.
- Layer 1 must target both `service` and `span` (the immediate observable consequences).
- Hand-offs justified empirically (no speculative DNSError hand-off unless data supports it).
