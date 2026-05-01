# PodFailure — manifest evidence

## Mechanism

`chaos-mesh` `PodFailure` flips the pod into a failure state by patching
the pod spec with an unschedulable image, causing readiness probes to
fail. The kubelet removes the pod from service endpoints but does NOT
restart the container; the existing process keeps running, just isolated.
Recovery is via chaos-mesh removing the patch.

## Sample cases used (4)

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-contacts-service-pod-failure-j42hd8`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-seat-service-pod-failure-c87xdg`
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-travel-service-pod-failure-cvrncg`
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-route-service-pod-failure-nhm7f9`

## Entry signature rationale

Empirical span counts in the 30 s entry window vs baseline (per 30 s):

| Case | target | baseline /30 s | entry /30 s | silent ratio |
|---|---|---|---|---|
| ts0 contacts | ts-contacts-service | 152.7 | 5 | 0.97 |
| ts0 seat | ts-seat-service | 954.0 | 11 | 0.99 |
| ts0 travel | ts-travel-service | 463.1 | 44 | 0.91 |
| ts1 route | ts-route-service | 2638.9 | 59 | 0.98 |

All 4 cases see ≥91% drop in span count. The few residual spans are
likely tail spans whose start timestamp pre-dates `t0`. We treat
`service.silent` as a boolean over the entry window (no spans, OR span
count <10% of baseline rate), aligned with the `silent` feature
definition.

`pod.unavailable` is the canonical seed tier emitted by InjectionAdapter
per `fault_seed.py` ("PodFailure" → unavailable). `restart_count` is
deliberately not required: across all 4 cases, the restart counter on
the target pod's container shows value 1 (preset from earlier rolling
restarts) but delta = 0 — PodFailure does not generate a new restart
event, distinguishing it from ContainerKill.

We do NOT use latency features for the same reason as PodKill: failed
readiness produces silence, not slowdown.

## Magnitude band rationale

- `pod.unavailable: [1.0, .inf]` — theoretical, by seed construction.
- `service.silent: [1.0, .inf]` — empirical 4/4.
- Optional caller-side `error_rate`/`connection_refused_rate` bands
  `[0.1, 1.0]` are downstream effects, optional at entry.

## Derivation layers

- Layer 1: identical structure to PodKill (silent at service + spans).
- Layer 2: caller spans. We added `timeout_rate` alongside `error_rate`
  here (in PodKill we used `connection_refused_rate`) because PodFailure
  leaves the existing TCP handshake intact for half-open connections;
  callers experience timeouts on already-established connections rather
  than connection-refused on new ones. Empirical observation: in
  ts0-cvrncg (travel-service), caller `ts-food-service` showed ~38%
  error rate within entry window; status codes were a mix of 502 and
  504, supporting both error_rate and timeout_rate as Layer 2 features.
- Layer 3: transitive callers; smaller fanout cap.

## Hand-offs

None. As with PodKill, samples did not exhibit DNS failure (services had
multiple replicas) so a DNSError hand-off would be speculative. Caller
errors are captured by Layer 2 of this manifest itself.

## Vocabulary gaps flagged

None. All features used (`unavailable`, `silent`, `error_rate`,
`timeout_rate`, `connection_refused_rate`) are in the bootstrap vocabulary.
