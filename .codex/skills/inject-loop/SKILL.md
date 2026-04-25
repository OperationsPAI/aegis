---
name: inject-loop
description: Drive a closed-loop fault-injection campaign against an aegis-deployed benchmark system. Use when the user asks to "run an injection loop", "iterate on fault candidates", "pick the next batch", "score candidates and submit", or wants you to repeatedly choose-submit-watch-update over a candidate pool of (app, chaos_type) tuples on systems like sockshop / otel-demo / ts. Trigger words: inject loop, candidate pool, batch injection, fault campaign, posterior, reward, adversarial inject, anti-detector inject.
---

# Fault-Injection Loop

Drive a closed-loop fault-injection campaign: a stable candidate pool, K parallel submissions per round, and reward updates that bias the next round toward "interesting" faults.

## When to use

- User wants you to run multiple rounds of injections, each round informed by the previous.
- The campaign has a *candidate pool* (app × chaos_type × params), not a single fault.
- Reward signal exists in task terminal events (the platform emits one per task).

## Inputs you need from the user before the first round

1. **Target system** — short code (sockshop, otel-demo, ts, …) and which pedestal/benchmark version pair.
2. **Pool size and shape** — how many candidates, which services to cover, which chaos types are in scope.
3. **Batch size K** — typical 5–10 per round; cluster-bound.
4. **Reward signal preferences** — at minimum what counts as +1, what counts as -1. Defaults below if user doesn't specify.

## Loop state

Maintain three files under `experiments/<system>-loop/`:

- `candidates.json` — the pool: `{system, pedestal, benchmark, defaults, candidates: [{id, app, chaos_type, params, prior, history:[{trace_id, ns, terminal, reward}]}]}`. Defaults carry shared values like `interval`, `pre_duration`, `container` — values that the chart variant (e.g. coherence-based sockshop) may force. **`duration` defaults to 5 minutes** in aegisctl; only pass it explicitly when you want a non-default window (e.g. longer to combat persistent `no_anomaly` on a resilient service).
- `runs.jsonl` — append-only, one line per submission: `{ts, candidate_id, group_id, trace_id, task_id, ns}`.
- `logs/<candidate_id>.err` — captured stderr per submit attempt. Always inspect when a submit fails.

State files persist between rounds and across sessions. Do not regenerate them; update in place.

## Enumerate the candidate space

Use `aegisctl inject candidates ls --system <code> --namespace <ns> -o json` to dump the full `(app, chaos_type, params)` tree for a system+namespace in one call. For sockshop1 this returns ~2900 candidates instantly; the data is whatever the platform's guided resolver knows about — JVM class+method targets (from chart annotations), HTTP routes (from observed traffic), Network pairs (from observed traffic), and the static chaos types like PodFailure / DNSError / TimeSkew.

Don't walk `aegisctl inject guided` step-by-step to enumerate — that's per-(app, chaos_type) round-trip and was only the path before the bulk endpoint shipped.

## One round = pick → submit → watch → score

### 1. Pick K candidates — coarse-then-fine

**Coarse: chaos_type budget** — proportional to that chaos_type's candidate-space size, but with floor and ceiling so no single type dominates.
- `coarse_share[c] = clamp(space[c] / Σ space, floor=1/(2·n_types·K), ceiling=0.5)`
- Renormalize so shares sum to 1.
- Apply success boost: `share[c] *= 1 + α · (success_rate[c] − mean_success_rate)`, with `α ≈ 0.3`. Small, so success doesn't snowball into bias.
- A chaos_type that hit zero successes drops to its floor, not to zero — keeps exploration alive.
- Round budgets to integers; spend leftover slots on highest-`coarse_share` type.

**Fine: candidate valuation within each chaos_type budget** — driven by the system's source repo, not by ClickHouse.
- Get `repo_url` from seed data (each system has one). Clone once, cache locally.
- For **JVM** candidates `(app, class, method)`:
  - Locate the source file. If absent (generated, missing, or non-Java), score 0 → drop unless budget can't be filled otherwise.
  - **+** length / complexity (LoC, branch count, try/catch, loops).
  - **+** I/O signals (DB calls, HTTP clients, message-queue sends). Methods that fan out to other services have higher blast radius.
  - **+** public/exported visibility. Internal helpers score lower.
  - **−** trivial getters/setters, constructors with no logic, methods named `toString`/`equals`/`hashCode`.
- For **HTTP/Network** candidates `(app, route, method, target_service)`:
  - Locate loadgen scripts in the same repo (locust/k6/wrk/jmeter — usually `loadgen/`, `bench/`, or `test/perf/`).
  - **+** route is exercised by loadgen.
  - **+** route handler fans out to multiple downstreams (parse handler source).
  - **+** route is on the path of a "core user journey" if the loadgen has weighted scenarios.
- For **Pod / container / DNS** candidates: roughly equal valuation, but **+** apps that loadgen targets directly (the hot services) over rarely-touched ones.
- For **Database** candidates `(table, op)`: **+** table is referenced by handlers exercised by loadgen.

Within the chaos_type's budget, sort by `prior × posterior × valuation`, pick top-N with diversity guards:
- No more than ⌈K/3⌉ from same `app` across the round.
- Reserve 1–2 slots for low-history exploration once most of the pool has been touched.

### 2. Submit each candidate
Default to explicit `--namespace sockshopN` (or equivalent) when you have a pre-installed pool — it's strictly more reliable than `--auto --allow-bootstrap` while #166 allocator quirks are open. Always pass `--reset-config --no-save-config --non-interactive --output json`. Other flags depend on chaos_type — see "Chaos-type traps" below.

If submit returns 500: **read the backend log immediately**, don't retry blindly. The error message names the exact missing field 90% of the time (network pair not found, container not found, latency-correlation-jitter-direction required, etc.).

### 3. Watch for terminal event
Each trace runs ~10–15 minutes (`pre_duration + duration + datapack-build + algo + collect`). Use `aegisctl trace get <trace-id>` and read the `last_event` field. Terminal events you'll see:
- `algorithm.result.collection` or `datapack.result.collection` — full pipeline ran, datapack and/or algorithm emitted output. **+1 reward.** Treat both as success; the difference is just how far the result-collection pipeline got, and from a "did this candidate produce useful data" perspective both are wins.
- `datapack.no_anomaly` — fault injected successfully but didn't perturb metrics enough for the detector to flag anything. **-1 reward.** Common with the default 5-minute fault on resilient services (statefulset pods that restart fast, async consumers, lightly-loaded code paths) and with sporadic chaos types (e.g. JVM exception with low call frequency). If a candidate keeps producing this, retry once with longer `--duration` before retiring it.
- `fault.injection.failed` / `datapack.build.failed` / RestartPedestal failure — environment/contract bug, not a candidate property. **0 reward** (don't penalize the candidate). When you see this, read the runtime-worker log for the trace ID — the cause is usually visible there (target image incompatibility, lock-state divergence, missing target service for chosen route, etc.).
- Any `*.failed` upstream of datapack — same: 0 reward, treat as flaky and surface to the user; don't silently retry without checking the cause.

### 4. Score / update
Append `{trace_id, ns, terminal, reward}` to that candidate's `history`. If you have access to the algorithm result artifact and want the adversarial bonus (#5/#6 below), add `+α` when detector's top service ≠ injected service.

After every round, recompute per-chaos_type `success_rate` (mean reward, clamped to [0,1] for the boost calc). Recompute the coarse shares for next round. Don't snapshot — chaos_type budget is recomputed each round, only candidate posterior carries forward.

### 5. Stop when…
- User asks to stop, OR
- Pool's posterior has converged (every "interesting" candidate has 3+ green runs), OR
- You hit a budget (rounds, wall-clock, ns-pool size).

Then summarize: top 5 candidates by posterior, top 5 by adversarial mismatch rate, and any candidate that consistently produces `no_anomaly` (those are the boring ones — flag for removal).

## Reward extensions

When the user asks for "anti-detector" or "adversarial" sampling:
- After algorithm.result.collection, fetch the detector's top-K services for that trace.
- `+α` if injected app ∉ top-3 (RCA missed).
- `-β` for repeat-without-new-insight: a candidate run for the Nth time with the same terminal class and same detector top-1 carries a small negative.
- Treat the adversarial term as a **bonus**, not the dominant signal — otherwise the pool collapses to the corner where the detector is permanently confused, losing diversity.

## Common round-shape patterns

- **Cold start (round 1)**: K equal priors, max diversity, no scoring yet.
- **Exploit round**: top-K by posterior, with 1–2 exploration slots.
- **Diagnostic round**: pin one app, sweep chaos_types — used when user wants to understand why one service keeps producing no_anomaly.
- **Scaling round**: add new candidates (e.g. JVM class+method targets discovered from the repo) to the pool mid-campaign — they enter with `prior=1` and equal weight.

## Chaos-type traps (ask the chart, not your assumptions)

- **Service names vary by chart variant.** Sockshop has multiple charts: oracle/coherence (services: front-end, carts, catalog, orders, payment, shipping, users) vs. weaveworks (catalogue, user, queue-master, …). After install, run `kubectl -n <ns> get pods -L app` to see actual labels — never hardcode names from memory.
- **CPUStress / MemoryStress need `--container <name>`** when pods run multiple containers (e.g. `coherence` for sockshop coherence chart). Symptom: `container "" not found under app X; available containers: …` — the error prints the right value.
- **JVMChaos requires `/bin/sh` inside the target pod's filesystem.** Distroless or minimal images that put busybox at `/busybox/sh` (e.g. `gcr.io/distroless/java*:debug`) don't satisfy this — chaos-daemon's `nsexec sh -c "mkdir -p /usr/local/byteman/lib/"` step fails with `exit status 101` before `bminstall` ever runs. Switch the target image to a base that ships `/bin/sh` natively (eclipse-temurin, openjdk-slim, alpine variants).
- **NetworkDelay needs all of `--latency --jitter --correlation --direction`**, plus `--target-service`. The target must form an *observed* network pair — freshly installed namespaces won't have any pairs yet. Either warm the cluster with traffic first, or skip NetworkDelay until later rounds.
- **PodFailure needs no extra flags** — the easiest first-round chaos type when you're not sure what the chart supports.
- **HTTPDelay/HTTPAbort need `--route` + `--http-method`** that an existing trace has already used; same warm-up caveat as NetworkDelay.

## Pool / namespace allocation

Two modes:

- **Pre-installed pool**: `aegisctl pedestal chart install <system> --namespace <sysN>` for each slot upfront, then submit with explicit `--namespace <sysN>` per candidate. Most predictable; you control which candidate runs in which ns and can trace state ns-by-ns.
- **Auto-allocate**: `aegisctl inject guided --apply --auto --allow-bootstrap …`. Server picks a free deployed slot, bootstrapping a new one (helm install + bump system count) if the pool is empty. Use this when you want to grow the pool on demand without managing slots yourself.

If `--auto --allow-bootstrap` returns `pool exhausted` and you're sure there should be free slots, the system count in etcd may have been reset to a smaller value than the pool actually has — bump it back up via the admin path (etcd or systems API) before retrying.

## What to put in the round-end summary for the user

- Submitted N candidates, M reached `algorithm.result.collection`, P were `no_anomaly`, Q failed environmentally.
- Top candidates by posterior so far (not just this round).
- Surprises: did any "obvious" service produce no_anomaly? Did any obscure target produce a clean detection? Either is worth surfacing.
- Pool health: which candidates haven't run yet, which are saturated.

## Pause points (ask the user, don't just keep looping)

- After round 1, before pivoting to scoring-driven selection — show the user the first batch's terminal events and confirm the reward labels look right.
- When you discover the candidate set has a structural problem (e.g. half the chaos types need data the cluster doesn't have yet) — propose a fix, don't silently delete those candidates.
- Before adding new candidates from external sources (loadgen analysis, JVM source) — describe what you found and let the user pick which to add.

## What this skill is NOT

- Not a one-shot inject helper — for that, just call `aegisctl inject guided --apply` directly.
- Not a runbook for debugging individual fault failures — for that, use the regression-e2e skill.
- Not a replacement for an algorithm-evaluation harness — this skill stops at "did the pipeline run and produce a result", not at "is the result correct".
