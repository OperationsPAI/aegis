# PodKill — manifest evidence

## Mechanism

`chaos-mesh` `PodKill` issues a `kubectl delete pod` on the target pod.
The pod is removed from the deployment's endpoint set, the container is
SIGKILL-ed, and the deployment controller schedules a fresh replica with a
new pod-name hash. Inbound traffic is dropped from the moment the kubelet
removes the endpoint until the new replica's readiness probe passes.

## Sample cases used (4)

- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-assurance-service-pod-kill-98r2br`
- `/home/ddq/AoyangSpace/dataset/rca/ts5-ts-contacts-service-pod-kill-srvmct`
- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-train-food-service-pod-kill-sscz4x`
- `/home/ddq/AoyangSpace/dataset/rca/ts4-ts-travel-plan-service-pod-kill-44xfx5`

## Entry signature rationale

Empirical span counts in the 30 s entry window vs baseline (per 30 s):

| Case | target service | baseline /30 s | entry /30 s | silent? |
|---|---|---|---|---|
| ts4 assurance | ts-assurance-service | 41.5 | 0 | yes |
| ts5 contacts | ts-contacts-service | 291.5 | 0 | yes |
| ts4 train-food | ts-train-food-service | 231.5 | 5 | yes (>97% drop) |
| ts4 travel-plan | ts-travel-plan-service | 79.4 | 0 | yes |

`service.silent` therefore fires deterministically on every observed PodKill.
`pod.unavailable` is the canonical seed tier from `fault_seed.py` and is
emitted by the K8sMetricsAdapter via the `pod_killed` specialization label
(metric absence in the abnormal-window tail).

We deliberately do NOT use `restart_count` as a required feature: the
original pod is removed and replaced by a new pod (different hash), and the
new pod's `k8s.container.restarts` series starts at 0 (confirmed across all
4 sample cases). Restart-count only fires on ContainerKill where the same
pod restarts the killed container in place.

We do NOT use latency features. PodKill produces immediate connection
failures, not slowdowns; in entry-window samples no spans are emitted at
all by the killed pod, so `latency_p99_ratio` is undefined.

## Magnitude band rationale

- `pod.unavailable: [1.0, .inf]` — boolean feature; theoretical (true by
  construction once the seed is laid).
- `service.silent: [1.0, .inf]` — boolean; empirical 4/4 cases.
- Optional `connection_refused_rate` and `error_rate` bands `[0.1, 1.0]`:
  these fire on caller spans, not at the target service; they are
  optional and do not gate entry.

## Derivation layers

- Layer 1 (`routes_to backward` + `includes forward`): the killed pod's
  service rolls up silent; spans on/into that service either drop out
  (silent) or carry server-side errors. Bands match the entry features.
- Layer 2 (`calls backward`): caller spans observe `error_rate` and
  `connection_refused_rate`. Empirical caller error rates seen across
  cases ranged 22-38% on directly-calling services (e.g. ts-food-service,
  ts-route-plan-service when ts-travel-service was killed in
  pod-failure case ts0-cvrncg, used as an analogue since PodKill caller
  traces are still being collected); we use a conservative `[0.05, 1.0]`
  lower bound = max(error_rate_floor, p5_empirical / 1.25).
- Layer 3 (`calls backward`): transitive callers; same band, smaller fanout.

## Hand-offs

None. The data does not force a DNSError hand-off: in all 4 cases the
service had multiple replicas and DNS resolution remained intact. Caller
errors at Layer 2 are already captured by this manifest's own derivation
tree, so no `HTTPResponseAbort` hand-off is needed either.

## Vocabulary gaps flagged

None. All referenced features (`unavailable`, `silent`,
`connection_refused_rate`, `error_rate`) exist in the bootstrap vocabulary.
