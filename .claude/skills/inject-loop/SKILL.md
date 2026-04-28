---
name: inject-loop
description: Drive a closed-loop fault-injection campaign against an aegis-deployed benchmark system. Use when the user asks to "run an injection loop", "iterate on fault candidates", "pick the next batch", "score candidates and submit", or wants you to repeatedly choose-submit-watch-update over a candidate pool of (app, chaos_type, params) tuples on systems like sockshop / otel-demo / ts / tea / hs / sn / mm. Trigger words: inject loop, candidate pool, batch injection, fault campaign, posterior, reward, adversarial inject, anti-detector inject.
---

# Fault-Injection Loop

> **See also**: the `aegisctl` skill for general CLI composition (`--all -o ndjson | jq | sort | uniq -c`, name-not-id filters, never-loop-pages). Specific `aegisctl …` invocations below are illustrative — `aegisctl <noun> [verb] --help` is the source of truth and supersedes anything that drifts here.

Drive a closed-loop fault-injection campaign: a stable candidate pool, K parallel submissions per round, and reward updates that bias the next round toward "interesting" faults.

## When to use

- User wants you to run multiple rounds of injections, each round informed by the previous.
- The campaign has a *candidate pool* (app × chaos_type × params), not a single fault.
- Reward signal exists in task terminal events (the platform emits one per task).

## Inputs you need from the user before the first round

1. **Target system** — short code (sockshop, otel-demo, ts, tea, hs, sn, mm, …) and which pedestal/benchmark version pair.
2. **Pool size and shape** — how many candidates, which services to cover, which chaos types are in scope.
3. **Batch size K** — typical 10–12 per round; cluster-bound by the system's namespace pool max (often 20).
4. **Reward signal preferences** — at minimum what counts as +1, what counts as -1. Defaults below if user doesn't specify.

## Loop state

Maintain per-round files under `experiments/<system>-loop/`:

- `candidates_round<N>.json` — the round's pool: `{round, system, system_type, pedestal, benchmark, defaults, _strategy, _results, candidates: [...]}`. Each candidate is `{id, app, chaos_type, params, duration_override?, paired_with?, trace_id?, ns?, terminal?, reward?, _note?, _outcome?, _failure?}`. The fields with `_` prefix are free-form annotations; `_strategy` is one paragraph at the top describing why this round is shaped the way it is, and `_results` is a one-paragraph postmortem appended after reaping.
- `runs_round<N>.jsonl` — append-only, one line per submission attempt: `{ts, candidate_id, paired_with, group_id, trace_id, task_id, ns}` or `{ts, candidate_id, error}` on submit failure.
- `terminals_round<N>.tsv` — one row per unique trace_id with `{trace_id, state, last_event}`. Refresh by running `experiments/lib/loop_iter.sh <system> <round>`.

State files persist between rounds and across sessions. Don't regenerate them; update in place.

## Submission helper

Use `experiments/lib/submit_dual.py`:

```
python3 lib/submit_dual.py \
  --candidates <system>-loop/candidates_round<N>.json \
  --runs-out  <system>-loop/runs_round<N>.jsonl \
  --pair-prob 0.0|0.3 \
  --seed <int> \
  --submit-sleep 4
```

It reads candidates, walks them in order, and either submits each as a single-spec batch or pairs the head with the next remaining (probability `--pair-prob`) into a multi-spec batch sharing one ns + trace. When a pair lands `+1`, both candidates inherit it. The script defaults `container` to `app` when a chaos type needs a container and neither `defaults.container` nor a per-candidate override is set — fixes CPUStress / MemoryStress / ContainerKill on systems without a `defaults.container`.

`--submit-sleep` controls inter-submit delay (default 2s). Bump to 4s+ when running back-to-back same-app candidates: bursting the same `(app, chaos_type, params)` fingerprint hits the backend's regression dedup.

## Two critical unit traps

1. **`duration` is in MINUTES, not seconds.** `aegisctl inject guided --apply --duration 5` means a 5-MINUTE chaos. The seed-data and candidate JSONs use `duration_override` which feeds the same field. If you set `duration_override: 20` thinking "20 seconds", you've actually pinned 20 minutes of chaos and the trace will hang in `fault.injection.started` for that whole window. Default to 5–10 for most chaos types; longer mostly slows iteration without changing detector verdicts.
2. **Stress numerical fields are integers, not strings with units.** `memory_size: 512` (MiB), not `"512MB"`. `cpu_load: 80` (percent), not `"80%"`. `time_offset` for TimeSkew is integer seconds. The JSON-decode error message names the field clearly when you get it wrong.

## Backend dedup — the silent failure mode

Submitting the same `(system, ns, app, chaos_type, params, duration, interval, pre_duration)` fingerprint a second time within the dedup window returns HTTP 200 with `data.items: []` and `data.warnings.batches_exist_in_database: [<batch index>]`. The submit looks "successful" but no trace is created. `submit_dual.py` logs this as `ns=None trace=None`.

**Bypass:** vary one of the fingerprint fields per candidate. Easiest is to stagger `duration_override` (5/6/7/8/9 across an N=5 stability sweep). Same chaos params with different durations are distinct fingerprints and all submit cleanly.

## Enumerate the candidate space

Use `aegisctl inject candidates ls --system <code> --namespace <ns> -o json` to dump the full `(app, chaos_type, params)` tree for a system+namespace in one call. That's the platform's guided resolver knowledge — JVM class+method targets (from chart annotations + observed JVM stack), HTTP routes (from observed traffic), Network pairs (from observed traffic), static chaos types like PodFailure / DNSError / TimeSkew.

Don't walk `aegisctl inject guided` step-by-step to enumerate — that's per-(app, chaos_type) round-trip and was only the path before the bulk endpoint shipped.

For systems with Java services, **also clone the upstream source repo** and extract `*ServiceImpl` / `*Endpoint` FQNs for JVMException candidates. The chart annotations only list classes that already had instrumentation; the source repo lists every method on the loadgen primary path. Maintain a cache under `/home/ddq/AoyangSpace/refs/` so this is a one-time clone.

## One round = pick → submit → watch → score

### 1. Pick K candidates — coarse-then-fine

**Coarse: chaos_type budget** — proportional to that chaos_type's candidate-space size, but with floor and ceiling so no single type dominates.
- `coarse_share[c] = clamp(space[c] / Σ space, floor=1/(2·n_types·K), ceiling=0.5)`
- Renormalize so shares sum to 1.
- Apply success boost: `share[c] *= 1 + α · (success_rate[c] − mean_success_rate)`, with `α ≈ 0.3`. Small, so success doesn't snowball into bias.
- A chaos_type that hit zero successes drops to its floor, not to zero — keeps exploration alive.

**Fine: candidate valuation** — driven by the system's source repo, not by ClickHouse.
- For **JVM** candidates `(app, class, method)`:
  - Locate the source file. If absent (generated, missing, or non-Java), score 0 → drop unless budget can't be filled otherwise.
  - **+** length / complexity, I/O signals (DB calls, HTTP clients, MQ sends), public/exported visibility, presence on loadgen primary path.
  - **−** trivial getters/setters, constructors with no logic, methods named `toString`/`equals`/`hashCode`.
- For **HTTP/Network** candidates: locate loadgen scripts in the same repo (locust/k6/wrk/jmeter — usually `loadgen/`, `bench/`, or `test/perf/`). Prefer routes the loadgen actually exercises.
- For **Pod / Container / DNS** candidates: roughly equal valuation, **+** apps that loadgen targets directly.
- For **Database** candidates: **+** tables referenced by handlers exercised by loadgen.

Diversity guards:
- No more than ⌈K/3⌉ from same `app` across the round.
- Reserve 1–2 slots for low-history exploration once most of the pool has been touched.

### 2. Submit each candidate

Default to `submit_dual.py` (handles `--auto --allow-bootstrap` plus the dual-fault pairing). Always pass `--reset-config --no-save-config --non-interactive --output json` if invoking `aegisctl` directly. Other flags depend on chaos_type — see "Chaos-type traps" below.

If submit returns 500: read the backend log immediately, don't retry blindly. The error message names the exact missing field 90% of the time (network pair not found, container not found, app not found in ns, etc.). Capture the response body in the candidate's `_failure` field — the failure mode is itself a finding.

### 3. Watch for terminal event

Each trace runs ~10–15 minutes (`pre_duration + duration + datapack-build + algo + collect`). Use `aegisctl trace get <trace-id>` and read `last_event`. Terminal events:

- `algorithm.result.collection` or `datapack.result.collection` — full pipeline ran. **+1 reward.** Treat both as success.
- `datapack.no_anomaly` — fault injected successfully but didn't perturb metrics enough. **-1 reward.** Common with resilient services and fast-recovering chaos like PodKill (vs PodFailure, which holds the pod down).
- `fault.injection.failed` / `datapack.build.failed` / `restart.pedestal.failed` — environment/contract bug, not a candidate property. **0 reward** (don't penalize). Read the runtime-worker log for the trace.
- `submit.error.*` (script-side bucket: 400 schema, 500 backend, dedup) — **0 reward** with the cause noted. Recurrent 500s often surface real backend bugs worth filing.

Also watch for `state=Running` traces stuck in `fault.injection.started` long after the chaos duration should have elapsed. Symptom: chaos-mesh `PodChaos` resource still present in the target ns past its `duration` field. Workaround: `kubectl delete podchaos -n <ns> --all`. Root cause is usually a duration-unit confusion you ate (set 20 thinking seconds, got 20 minutes).

### 4. Score / update

Append `{trace_id, ns, terminal, reward}` directly to the candidate row in `candidates_round<N>.json`. Add `_outcome` for one-line interpretation when the result is interesting (NEW WINNER / regression / off-path). Recompute per-chaos_type success rate at round end; recompute coarse shares for the next round. Don't snapshot — chaos_type budget is recomputed each round, only candidate posterior carries forward via the round-N → round-N+1 candidate selection.

### 5. Stop when…

- User asks to stop, OR
- Every "interesting" candidate has 3+ green runs, OR
- Hit a budget (rounds, wall-clock, ns-pool size).

Then summarize: top 5 candidates by posterior, top 5 by adversarial mismatch rate, candidates that consistently produce `no_anomaly` (those are the boring ones — flag for removal).

## Detector responsiveness varies wildly across systems

Empirically observed across 60+ rounds:

- **TT (TrainTicket)** — high signal. ~50–80% +1 rate on PodFailure / JVMException targeting on-path methods. Stable winners reproduce across rounds. Loop produces useful per-method posterior in 5–10 rounds.
- **otel-demo** — noise-floor signal. ~10–20% +1 rate regardless of chaos type / magnitude / target. R1 winners do not reproduce on exact-replay. Param-sweep is futile; needs detector-side investigation before loop yields more than coin-flips. **Pause the loop** with a `PAUSED.md` note and surface to the user when this pattern is clear.
- **sockshop** — ~zero signal. Detector is keyed off something other than chaos magnitude (likely loadgen-operation-timing-specific). Same diagnosis as otel-demo, even fewer wins.

When a system shows persistent ~5–15% +1 over 5+ rounds with no parameter pattern, it's not a candidate-quality problem; it's a detector-implementation problem. Stop iterating, write `<system>-loop/PAUSED.md` summarizing what was tried and the conclusion, and tell the user.

## "Magic dur=7" effect on TT JVMException

A repeated finding on TrainTicket: methods that -1 with `duration_override: 5` or `6` flip to +1 at `7`+. Confirmed on `route.getRouteByIds`, `travel.retrieve`, `payment.addMoney`, `cancel.cancelOrder`, `food.getAllFood`. Hypothesis: 5–6 minutes is below the detector's anomaly-detection sliding-window length, so the perturbation gets averaged out. When you see a TT JVMException -1 at dur=5, retry at dur=7 before retiring.

## Pairing helps, but only sometimes

Pairing two candidates into one ns (`paired_with` field, `submit_dual.py --pair-prob 0.3`) sometimes triggers a +1 that neither would solo (otel-demo's R7 fraud-detection × image-provider was such a case). But pairing also dilutes signal on a known solo winner — running otel-demo currency PodFailure (a 5/8 solo winner) paired with anything else regularly returned -1.

Use pairing as a probe, not a default: 30% rate is reasonable for exploration rounds, drop to 0 when stability-testing a known winner.

## Chaos-type traps (ask the chart, not your assumptions)

- **App labels vary by chart variant.** Sockshop coherence vs weaveworks have entirely different service names. otel-demo upstream uses kebab-case (`product-catalog`, not `productcatalog`). After install, run `kubectl -n <ns> get pods -L app` to see actual labels — never hardcode names from memory.
- **CPUStress / MemoryStress / ContainerKill need `container` set.** The error names the right value: `container "" not found under app X; available containers: …`. `submit_dual.py` defaults to `app` name when `defaults.container` is unset.
- **JVMChaos requires `/bin/sh` inside the target pod's filesystem.** Distroless or minimal images that put busybox at `/busybox/sh` don't satisfy this — chaos-daemon's `nsexec sh -c …` step fails with `exit status 101`. Switch to `eclipse-temurin`, `openjdk-slim`, or alpine-based bases.
- **NetworkDelay/Loss/Partition between two services** — the (source, target) pair must already be observed in the cluster's traffic snapshot. Freshly installed namespaces won't have any pairs. Either warm with traffic for 60s before submit, OR use chaos types that don't need pair pre-check (Pod*, JVM*, CPU*, Memory*, DNS-FQDN).
- **DNSError needs a cataloged domain.** Short service names ("checkout") and arbitrary external hosts ("www.example.com") both fail. Use FQDN inside the cluster (`checkout.<ns>.svc.cluster.local`) or a domain the resolver actually has.
- **TimeSkew `time_offset` is integer seconds.** `"+1h"` 400s.
- **HTTPRequest/Response chaos** needs a route that's in the observed-routes catalog. Random gateway paths fail.
- **HTTPChaos is unusable on byte-cluster (and any IPVLAN-based CNI cluster).** chaos-mesh's tproxy implementation tries to attach the pod's `eth0` to a Linux bridge in `chaos-tproxy-controller/src/proxy/net/bridge.rs::setenv_bridge`. The kernel rejects this on IPVLAN child interfaces with `RTNETLINK answers: Invalid argument`. byte-cluster's CNI is Cello in `eni_shared` mode (default), which makes pod `eth0` an IPVLAN child of a host ENI. Symptom: HTTPChaos stays at `Phase: Not Injected/Wait`, `injectedCount: 0`; tproxy ends up running inside the pod (chaos-daemon spawned it before bridge setup), but rules are never applied. Skip HTTPChaos / HTTPDelay / HTTPAbort entirely on this class of cluster — exclude them from candidate generation. Tracked at `OperationsPAI/chaos-mesh#7`.
- **PodFailure vs PodKill.** PodKill restarts fast (tens of seconds); for detectors that need sustained service degradation, PodFailure is the canonical chaos. On TT we saw config-service +1 with PodFailure ×6, but -1 with PodKill — kill-then-restart is too quick to perturb the metric window.
- **PodFailure needs no extra flags** — easiest first-round chaos type.

## Pool / namespace allocation

`submit_dual.py` always uses `--auto --allow-bootstrap` (allocator picks a free deployed slot, bootstrapping if needed). The system's namespace pool max defaults to 20. When you see `bump count for <sys> to register <sys>20: invalid count: value 21 exceeds maximum 20`, you've hit the cap; older traces in earlier ns slots haven't yet released them. Either wait, run a smaller K, or bump the pool max via the systems API.

When the worker is busy and a pre-allocated ns0 hasn't actually been bootstrapped (helm install incomplete), submits to that ns return `no containers found for system "<sys>" in namespace "<sys>0"`. The auto-allocator's hole-fill picked a slot that the bootstrap controller hasn't finished provisioning. Retry usually picks a different slot.

## Cluster hygiene between campaign phases

When pausing a system after concluding its detector is stuck on noise (sockshop, otel-demo), **`helm uninstall` the per-namespace releases** to free cluster resources rather than `kubectl delete ns` (which can leave stuck finalizers). Loop:

```
for i in 0 1 2 3 4 5 6 7 8 9; do
  helm uninstall <sys>$i -n <sys>$i --wait=false 2>&1 &
done
wait
```

Fast and reliable. Aegis's allocator state survives — when you resume the loop, the auto-allocator will re-bootstrap fresh ns from index 0.

## What to put in the round-end summary for the user

- Submitted N candidates, M reached `*.result.collection`, P were `no_anomaly`, Q failed environmentally, R deduped silently.
- Top candidates by posterior so far (not just this round).
- Surprises: did any "obvious" service produce no_anomaly? Did any obscure target produce a clean detection? Either is worth surfacing.
- Pool health: which candidates haven't run yet, which are saturated.
- New winners (+1 first time), and stability of prior winners (re-confirmation count).

## Pause points (ask the user; don't just keep looping)

- After round 1, before pivoting to scoring-driven selection — show the user the first batch's terminals and confirm the reward labels look right.
- When a system shows persistent low-signal across 5+ rounds — propose pausing with a `PAUSED.md` note instead of grinding more rounds.
- Before adding new candidates from external sources (loadgen analysis, JVM source) — describe what you found and let the user pick.

## Autonomous-loop mode

If the user has explicitly authorized autonomous iteration ("keep iterating without per-round approval"), use `<<autonomous-loop-dynamic>>` via `ScheduleWakeup` with `delaySeconds` matched to the round's expected terminal time (typically 18–25 minutes for K=10 with 5–10min chaos). Each wakeup: reap → write rewards → plan + submit next round → re-arm wakeup. Continue until `PAUSED.md` written, user terminates, or pool saturates.

## byte-cluster infrastructure pitfalls (learned the hard way 2026-04-26→27)

These are recurring footguns specific to the byte-cluster deployment — separate from any candidate-quality issue. If a loop "suddenly stops working" on byte-cluster, walk this list before re-tuning candidates.

### `system_type` does NOT always equal the short code

The seed in `manifests/byte-cluster/initial-data/data.yaml` uses **`system: teastore`**, but the staging seed uses **`system: tea`**. If a candidate JSON's `system_type` doesn't match the live cluster's seed, every submit returns `mismatched system type X for pedestal Y at index 0`. **Always check the byte-cluster `data.yaml` before writing the first candidate file** — copy the system code verbatim from there.

### DSB charts (hs / sn / mm) — Go SDK double-prefix bug

`hs/sn/mm` services are Go-based and their OTel SDK **prepends `http://` even when `OTEL_EXPORTER_OTLP_ENDPOINT` already starts with `http://`** → `parse "http://http://..." invalid URL escape`. Spans never reach the collector. The chart's helper template auto-prepends `http://` if your seed value lacks it, so both paths produce the bug.

Fix: set `parameter_configs.config_key='otel.collectorEndpoint'` value to the host:port WITHOUT scheme (e.g. `opentelemetry-kube-stack-deployment-collector.monitoring.svc.cluster.local:4317`). The chart still prepends, the SDK no longer double-prepends, the result is correct. Fresh ns deployed after the seed update inherits the fix; existing ns need `kubectl set env deploy --all -n <ns> OTEL_EXPORTER_OTLP_ENDPOINT=<host:port>` (note: chart helm-managed reconciliation may revert this on next chart op — kill+recreate the ns is more durable).

Symptom check: `kubectl exec -n <ns> <frontend-pod> -- /bin/true && kubectl logs -n <ns> <frontend-pod> | grep "http://http"`.

### DSB load-generator data-init container

`sn` and `mm` chart 0.1.3 load-generator has TWO init containers. The first uses the new `loader-datasets` pre-baked image (`global.dockerRegistry`/loader-datasets), but the second (`data-init`) **hardcodes `python:3.9-slim` from docker.io** — bypasses the global registry. byte-cluster's docker.io egress is flaky → `ImagePullBackOff` → `restart.pedestal.failed` after 15 min.

Workaround: push `python:3.9-slim` to `docker.io/opspai/python:3.9-slim` (volces auto-mirrors), then add `parameter_configs.config_key='load-generator.initContainer.image'` value `pair-cn-shanghai.cr.volces.com/opspai/python` linked to the `social-network` and `media-microservices` helm_configs. Newly deployed ns uses the override.

### teastore jmeter loadgen would firehose ClickHouse

teastore chart 0.1.3 default jmeter is `numUsers: 10 / duration: 300` → ~9k spans/sec/ns (~22× any other benchmark). Just 4 tea ns simultaneously can saturate CH `max_concurrent_queries=2000`, cascading every system's BuildDatapack to fail. Chart 0.1.4 has throttled defaults (numUsers=2, duration=120) — pin to **0.1.4 minimum** in seeds. If stuck on older chart, set `parameter_configs` overrides for `jmeter.numUsers=2`, `jmeter.duration=120`.

### teastore-db missing from seeded available_apps (backend bug)

The teastore chart deploys 7 components including `teastore-db`, but the backend's seeded `available_apps` for system `teastore` lists only 6 (auth/image/persistence/recommender/registry/webui). Submitting `app: teastore-db` returns 500 with `app "teastore-db" not found in namespace; available apps: ...`. **Don't include `teastore-db` in candidates** until the seed gap is fixed.

### Allocator namespace pool max defaults to 20

Per-system `injection.system.<sys>.count` has `max_value=20` in `dynamic_configs`. After 20 namespaces are registered, every submit returns 500 `value 21 exceeds maximum 20`. The deleted-but-not-counted-down problem is real: `helm uninstall <ns>` doesn't decrement the count. To recover capacity:
- `kubectl -n exp exec rcabench-mysql-0 -- mysql -uroot -p<pass> rcabench -e "UPDATE dynamic_configs SET max_value=50 WHERE config_key LIKE 'injection.system.%.count'"`
- restart api-gateway to refresh the limit cache
- delete actual stale ns (`kubectl get ns | awk '/^<sys>/...'`) AND reset the etcd count: `kubectl -n exp exec rcabench-etcd-0 -- etcdctl put /rcabench/config/global/injection.system.<sys>.count 0`

### ClickHouse capacity & OTel collector capacity

The byte-cluster ClickStack runs operator-managed (chart 2.1.1+) with these caps after PRs #206/#211/#213/#218/#279:
- `max_concurrent_queries: 5000` (was 100 default → 500 hot-fix → 2000 → 5000)
- CH replicas: 2 with replication via Keeper (3 nodes)
- CH resource limits: cpu 8/mem 16Gi per replica
- OTel deployment-collector HPA: minReplicas=6 **maxReplicas=120**, target 60% memory / 55% CPU (was 40 → 120 in #279)
- OTel collector pod: cpu 4 / **mem 8Gi** (was 4Gi → 8Gi in #279)

If you see `Code 202: Too many simultaneous queries` in build-pod logs, `max_concurrent_queries` was hit — likely because someone deployed teastore at chart < 0.1.4 (jmeter firehose). If you see `data refused due to high memory usage` in collector logs OR a pod log, OTel is OOM-throttling — bump `maxReplicas` and/or per-pod `memory` limit in `AegisLab/manifests/byte-cluster/otel-kube-stack.values.yaml`.

**Pre-flight before TT-scale campaigns** (TT = ~50 services × 16 ns; teastore = jmeter firehose). Run `kubectl -n monitoring get hpa opentelemetry-kube-stack-deployment-collector` BEFORE kicking off the round. If `MAXPODS` is at the deployed ceiling AND `memory` exceeds 100% of target, raise the ceiling first. Otherwise the **silent ts/teastore drop pattern** triggers: Java agent's OkHttp gRPC exporter does not retry on `UNAVAILABLE`, so TT spans are dropped on the floor while otel-demo / DSB Go-SDK / Node-SDK keep flowing (BatchSpanProcessor + retry). One-system-zero-traces-others-fine is the diagnostic signature.

### Best timing for aegis redeploy / namespace lifecycle

**Redeploy aegis only between rounds.** `runtime-worker` / `api-gateway` rollout silently orphans in-flight tasks (no error log, redis queues empty, the trace just stops progressing at whatever stage the worker was handling). Wait until `terminals_round<N>.tsv` has been fully reaped before any `kubectl rollout restart` or helm upgrade against `exp/`.

**Don't `helm install <sys>N` to pre-create benchmark namespaces.** Raw helm bypasses aegis's allocator + pool counter, and they desync (the pool registry doesn't know about the ns; later you hit `value 21 exceeds maximum 20` once aegis catches up). Two aegis-native paths:

1. **Default** — `submit_dual.py --auto --allow-bootstrap` lets the allocator bootstrap a slot at submit time (already the default; accept the first-round latency).
2. **Pre-warm to remove first-round bootstrap latency** — `aegisctl inject guided --install --namespace <sys>N` deploys the workload through the aegis path so allocator state stays consistent.

If neither covers your case, **flag it as an aegisctl gap** (per `CLAUDE.md`'s "aegisctl ownership" rule) — don't reach for raw helm/kubectl/mysql to work around. The CLI is the supported surface.

### BuildDatapack races OTel ingestion lag (issue #210)

`prepare_inputs.py` queries CH within ~2 min of fault end. If OTel→CH ingestion lag > 2 min (during CH overload, typically 10-30 min), `abnormal_traces.parquet` is empty → `ValueError: Output path validation failed.` → trace ends in `datapack.build.failed`. This is **environment failure, not candidate signal** — don't penalize the candidate, retry next round (varied dur to avoid dedup).

PR for permanent fix is at `OperationsPAI/aegis#210` proposed: pre-flight CH freshness check at executor entry. Until merged, mitigate by (a) keeping CH/OTel sized for the load (above), (b) BuildDatapack token bucket via `rate_limiting.max_concurrent_build_datapack=8` (#209 merged), (c) waiting an extra round before retrying recently-failed candidates.

### Fresh chart pickup requires more than seed file edit

When you bump the chart version in `manifests/byte-cluster/initial-data/data.yaml` and helm-upgrade rcabench, the `init-etcd-producer-config` initContainer **skips re-seeding if `/initialized_producer` etcd key already exists**. Three options:

1. `kubectl -n exp exec rcabench-etcd-0 -- etcdctl del /initialized_producer` then `kubectl rollout restart deploy rcabench-api-gateway -n exp` — full re-seed from configmap.
2. Direct mysql update: `UPDATE helm_configs SET version='X.Y.Z' WHERE id=<helm_config_id>` then `kubectl rollout restart deploy/rcabench-runtime-worker-service -n exp` — surgical, doesn't disturb other rows.
3. `aegisctl pedestal helm set --container-version-id <N> --version X.Y.Z` — preferred when authenticated.

After bump: also delete the existing `<sys>*` namespaces and reset `injection.system.<sys>.count` so allocator builds fresh ns from the new chart.

## What this skill is NOT

- Not a one-shot inject helper — for that, just call `aegisctl inject guided --apply` directly.
- Not a runbook for debugging individual fault failures — for that, use the regression-e2e skill.
- Not a replacement for an algorithm-evaluation harness — this skill stops at "did the pipeline run and produce a result", not at "is the result correct".
