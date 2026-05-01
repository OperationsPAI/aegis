# ContainerKill — manifest evidence

## Mechanism

`chaos-mesh` `ContainerKill` issues a SIGKILL to a single container
inside a pod (typically the application container, not the sidecar). The
pod itself stays scheduled; the kubelet detects the dead container and
restarts it in place, incrementing `k8s.container.restarts` by 1. Inbound
requests during the kill window abort mid-flight or fail to connect
until the container restarts.

## Sample cases used (4)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-mysql-container-kill-9t6n24`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-inside-payment-service-container-kill-cxt5lv`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-assurance-service-container-kill-qw48fm`
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-food-service-container-kill-cqcxsh`

## Entry signature rationale

Empirical observations in 30 s entry window:

| Case | target | bl /30 s | entry /30 s | silent | max_restart | delta_restart |
|---|---|---|---|---|---|---|
| ts0 mysql | mysql | 0.0 | 0 | yes | 1 | 1 |
| ts0 inside-payment | ts-inside-payment-service | 82.6 | 0 | yes | 1 | 0 |
| ts1 assurance | ts-assurance-service | 95.5 | 0 | yes | 1 | 0 |
| ts2 food | ts-food-service | 261.4 | 0 | yes | 1 | 1 |

`service.silent` fires 4/4. `container.restart_count` reaches 1 in all 4
cases (max value); the delta within the abnormal window is 1 in 2 cases
and 0 in 2 cases (where the restart happened just before our window
sampling). Both are consistent with the K8sMetricsAdapter's
`crash_loop` specialization label, which emits container.UNAVAILABLE on
restart-counter delta > 0 OR absolute count > 0.

We use `restart_count >= 1` (boolean-ish absolute) rather than
`pod.unavailable`, per family-A guidance: ContainerKill is gentler than
PodKill — the pod stays up, only one container restarts. Requiring
`pod.unavailable` would cause false negatives on this fault type.

We do NOT require latency features: like other lifecycle faults, the
signal is silence + restart, not slowdown.

## Magnitude band rationale

- `container.restart_count: [1.0, .inf]` — empirical, 4/4 cases hit ≥1
  within the abnormal window. Lower bound 1.0 is the natural floor (any
  positive restart event suffices).
- `service.silent: [1.0, .inf]` — empirical 4/4.
- Optional `error_rate` and `connection_refused_rate` bands `[0.1, 1.0]`
  on caller spans; optional at entry.

## Derivation layers

- Layer 1: same `routes_to backward` + `includes forward` pattern. The
  service goes silent and immediate spans either drop out or carry
  errors. Bands match entry.
- Layer 2 (`calls backward`): caller spans see `error_rate` and
  `connection_refused_rate`. ContainerKill's recovery is faster (~5-10 s
  for container restart vs ~20-30 s for pod reschedule), so we cap at
  Layer 2 (no Layer 3) — circuit breakers typically absorb the blast
  radius before it reaches transitive callers.

## Hand-offs

`ContainerKill → HTTPResponseAbort` on `span.error_rate > 0.2` at Layer 2.

Rationale: at the moment of SIGKILL, requests already in-flight against
the dying container have an established TCP connection; the response is
truncated mid-stream and far-downstream callers parse this as a
response-abort (HTTP/2 RST_STREAM, gRPC code UNAVAILABLE) rather than a
fresh connection failure. This is the empirically motivated cross-family
hand-off allowed by PLAN.md (universal trigger
`span.error_rate > 0.2`). Threshold 0.2 matches the worked CPUStress
example.

We do NOT add a DNSError hand-off (data does not support it: services
had multiple healthy replicas; DNS continued resolving correctly).

## Vocabulary gaps flagged

None. `restart_count` is in the bootstrap vocabulary and is already
extracted by `K8sMetricsAdapter` (`RESTART_METRICS` set).
