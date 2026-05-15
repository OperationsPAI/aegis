---
name: injection
description: Author and run fault-injection rounds against an aegis-deployed benchmark, framed as the adversarial puzzle setter against an RCA agent. Trigger on anything fault-injection-related — "故障注入 / inject fault / 出题给 RCA / 对抗性故障注入 / red team RCA / 持续注入 / 自动选难度高的故障 / pick what to inject next / run an injection round / injection loop / inject campaign". The skill embodies a single mindset (pick faults that maximize RCA inference difficulty — fine granularity × user-visible SLO breach × long causal chain × blast radius) and a state convention (per-system memory under `~/.aegisctl/injection-author/<system>/`) so judgments accumulate across runs. Use the `aegisctl` skill for CLI composition (NDJSON pipes, name-not-id filters); this skill decides *what* to inject and *why*.
---

# injection — be the RCA puzzle setter, not a fault firehose

You are the **adversarial author**. The RCA agent is your opponent. You win when **the RCA agent fails to identify the actual root cause** — not when the system crashes the loudest. A fault that breaks SLO but is trivially traceable is a *bad* puzzle. A fault that quietly degrades two endpoints whose only common dependency is a shared connection pool is a *great* puzzle.

This skill is **judgment, not configuration**. For command syntax, read `aegisctl <noun> --help` and follow the `aegisctl` skill — don't restate flags here, they change.

## What makes a fault "good"

A good puzzle scores high on all four axes simultaneously. Any one of them at zero produces a worthless puzzle:

| Axis | Bad (low) | Good (high) |
|---|---|---|
| **Granularity** | Whole pod, whole namespace, whole container | One JVM method, one HTTP route+verb, one DB table+op, one service-pair network |
| **User impact** | Detector silent (system absorbed it) | Detector fires on a user-facing entrance; SLO violated |
| **Inference distance** | The faulted service IS the failing service | Failure surfaces N hops away through shared resources, retries, cache cascades, timeout amplifiers |
| **Blast radius / "$"** | One demo endpoint slow | Critical-path endpoint (checkout, login, search) for noticeable wall-clock time |

**Rule of thumb:** if you can finish the sentence "the cause is obvious because the trace shows…", your puzzle is too easy. The cause should require correlating across services, time windows, or endpoint subsets.

### Granularity ladder (coarsest → finest)

1. PodFailure / whole-container kill — **avoid by default**. Sanity-check control only.
2. CPU / memory stress on a whole container — coarse; node metrics give it away.
3. Network delay / loss / partition between a **service pair** — better; not visible in the static service graph.
4. **JVM method-level** latency / exception / return-value rewrite — RCA must look at endpoint distributions, not service-level metrics.
5. **HTTP route+verb-level** abort / status-code rewrite / delay — RCA must filter by route to spot it.
6. **DB table+op-level** latency / abort — RCA must look at query-level traces.
7. **Multi-fault batch** of items at level 4–6 — top tier; see *Multi-fault* below.

Default to levels **4–7**. Levels 1–2 are control experiments, not puzzles.

## Reward signal: multi-source, no single oracle

There is no single ground-truth signal for "was this a hard puzzle." Triangulate from several sources, each of which can lie:

| Source | What it tells you | How it lies — what to do about it |
|---|---|---|
| **Detector verdict** | Did the detector flag SLO violation? | False positives (noise) and false negatives (graceful-degrade absorbed). Treat as **one** input, not as oracle. |
| **Direct injection-effect inspection** | Did the fault actually land and perturb traces/metrics? | Rarely lies — but takes work. Pull the abnormal-window data via aegisctl and look. If the chaos resource is "Injected" but no span / metric reflects perturbation, it was a no-op (record as `injection-noop`, don't grade as a puzzle). |
| **SLO impact at the entrance** | Did real loadgen / users see degradation? | Can be invisible at namespace average level when traffic is bursty — look at the entrance window specifically, not the system-level mean. |
| **RCA correctness** (the actual win condition) | Did RCA name the right root cause? | **Not live-queryable right now** — see below. |

The first three are available now, in-loop, via aegisctl. **Don't trust the detector alone** — many of its silences are real degrade and many of its fires are noise; cross-check with the data you can pull yourself.

### Right now: simulate the RCA inside this round

Until aegisctl exposes offline RCA-grading queries (planned — will surface whether the RCA pipeline named the right cause for each trace), you can't get a real RCA-was-wrong signal during the loop. So during each round, *after* you've confirmed the fault actually landed (sources 1+2+3), simulate an RCA reasoning pass yourself:

> "If I were an RCA agent looking only at the abnormal-window traces, the SLO violation, and the service graph — how many hops would I chase before converging on the actual fault? Which intermediate hypotheses look more plausible than the truth and would pull me off course?"

Score difficulty by **estimated inference-chain length** (hop count) plus **count of plausible decoy hypotheses on the way**. Record both in the round file. This is a proxy for the deferred RCA-correctness reward, but it's directionally aligned and forces you to think like the opponent.

Be honest in the simulation. If a competent agent could chain `entrance error → upstream service → its dependency → faulted method` in three hops with no plausible decoys, write down "3 hops, 0 decoys." Padding the estimate ruins the campaign.

### Later: offline RCA reward (deferred)

When aegisctl ships the offline RCA-grading query:
1. Round files already carry `pending_offline_rca: true` and `trace_ids: [...]` — the post-hoc grader uses those to re-score.
2. Re-grade past rounds in a single pass and patch `memory.md` with the corrections; your simulated difficulty may have been optimistic or pessimistic.
3. Bias future rounds toward whichever simulation patterns aligned with offline reality.

## Each round (you do, or `loop.sh` drives)

1. **Read state.** Open `~/.aegisctl/injection-author/<system>/metadata.json` and `memory.md`. They tell you what's been tried, what worked as a hard puzzle, and what the system's quirks are.
2. **Grade the previous round if its data has landed.** Look at the most recent file under `rounds/`; if its trace has terminal events available via aegisctl now, do the multi-source check (detector verdict + injection-effect inspection + SLO impact) and write the verdict back into that round file. Leave `pending_offline_rca` alone — that's for the future grader.
3. **Survey.** Pull the recent injection distribution for this system (last ~50–100 leaf injections; filter out batch parents). Tally by chaos_type *family* and target service.
4. **Read the system — code AND data, not priors.** Empirically the single biggest behavior regression in autonomous rounds: the agent already "knows" common targets (queryForTravel, checkout, etc.) from training and skips real reading. The judgment may still happen to be correct on familiar systems, but the methodology is brittle on any unfamiliar one, and even on familiar systems you miss recently-changed behavior. Two parallel evidence streams, both required when available — **before** writing the hypothesis:

   **a. Source code (when `--source-dir` is set).** Run **at least two `Read` or `Grep` calls** under `${SOURCE_DIR}` and cite the results in the round file's `code_evidence` block:
   - **Fan-in callers** of your candidate method / route / table — real codebases routinely have 5–10× the call sites you'd guess.
   - **Retry / timeout / circuit-breaker / cache-fallback** logic around the candidate. A fault on a path with a graceful fallback is a *bad* puzzle (detector silent, RCA never sees a symptom).
   - **Shared resources** (connection pools, Redis, auth, gateway) the candidate touches.

   **b. Observable data (always available via aegisctl).** Pull recent traces / metrics around your candidate. Look at:
   - The **actual outbound span graph right now** — production calls may differ from what the code suggests after recent rollouts or feature flags.
   - **Latency distributions per endpoint** — pick a candidate that's already a tail-latency contributor; extra latency on a fast endpoint may not move the entrance.
   - **Recent error / status-code distributions** — if the entrance already has noisy 5xx, your fault won't stand out.

   The wire (b) shows what the cluster actually does; the code (a) shows what it's supposed to do; **both can lie alone**, neither is replaceable by priors. With `--source-dir` provided and `code_evidence` empty, the next round's step-2 retro-grade will mark this round as "prior-only" anti-pattern in `memory.md`. Without `--source-dir`, do (b) only and set `code_evidence: []` with a `_note: "source unavailable"`.
5. **Propose.** Decide K_outer (parallel traces this round, see *Per-round shape*) and per-trace K_inner (leaves per trace's batch). For each of the K_outer puzzles, pick a fault (or K_inner-leaf interaction set) maximizing the four axes. Write the hypothesis as one sentence per trace: *"500ms delay on `<route>` of `<service>` should surface as failed checkouts because `<service>` is on the critical path with no fallback."* If you can't write that sentence with conviction, the idea isn't ready.
6. **Diversity check.** Compare to step 3's tally. If your family is overrepresented, swap to an underrepresented one. Also vary across the K_outer parallel traces — don't pose the same puzzle K_outer times. See *Diversity*.
7. **Apply.** Submit each of the K_outer puzzles as its own trace through aegisctl (use `--auto` namespace allocation so they spread across the deployed pool). For traces with K_inner ≥ 2, stage each spec then `--apply --batch` so the leaves land in the same trace; for K_inner=1, a single `--apply` per trace is enough.
8. **Verify injection landed.** Don't trust the submit response or the detector alone. Pull the trace / abnormal-window data via aegisctl and confirm the fault actually perturbed traces or metrics on the targeted scope. If it didn't, mark the round `injection-noop` and *do not* grade it as a puzzle — investigate why (image issue, HTTPChaos `direction: from` on byte-cluster, missing observed-pair for network chaos, DNS catalog miss, etc.).
9. **Simulate the RCA reasoning.** Walk through how an RCA agent would chase this from the entrance symptom to the actual fault. Record hop count and number of plausible decoy hypotheses on the path. This is your in-loop proxy for the deferred RCA-correctness reward.
10. **Persist.** Write the round record under `rounds/round-<N>-<unix-ts>.json` (full schema in *State conventions*), and append distilled cross-round lessons to `memory.md`.

The loop does **not** wait for the *next* round's results before exiting — step 2 picks up that grading on the next invocation, by which time aegisctl has the trace's terminal events. Don't sleep waiting on it inline.

## State conventions (per system)

Layout under `~/.aegisctl/injection-author/<system>/`:

```
metadata.json           # {system, first_started_at, last_started_at, total_rounds_run, family_tally, service_tally}
memory.md               # free-form running notes — keep tight; see below
aegisctl_gaps.md        # one-liner per friction point you hit using aegisctl; the user reviews this periodically to improve CLI ergonomics — see *Aegisctl gap log* below
rounds/
  round-<N>-<ts>.json   # round record; full schema below
loop.log                # appended by loop.sh
```

**Round-file schema** (write all available fields; leave deferred ones with the sentinels shown):

```jsonc
{
  "round": 17,
  "ts": "2026-04-28T09:30:00+08:00",
  "system": "ts",
  "picked_faults": [ /* the GuidedConfig(s) you submitted */ ],
  "hypothesis": "500ms delay on /orderother/queryOrders should surface as failed checkouts via ts-order-other-service → ts-cancel-service retry storm",
  "diversity_note": "previous 50 rounds: jvm-* 26%, network-* 14% — picking network-delay to rebalance",
  "submit": { "trace_ids": ["..."], "ns": "ts3", "submit_resp": { /* aegisctl response */ } },

  // Filled in step 4: code grounding (when --source-dir was set; empty list otherwise).
  // Each entry is a real (file, line-range, what-you-learned). Empty array when
  // --source-dir was provided is an anti-pattern — next round's retro-grade will
  // mark it as "prior-only" in memory.md.
  "code_evidence": [
    {"file": "ts-travel-service/.../TravelServiceImpl.java", "lines": "78-115",
     "what": "queryForTravel callers: TravelController.queryRoute, PreserveServiceImpl.fillBasicInfo, OrderServiceImpl.calcRefund (3 fan-in)"},
    {"file": "ts-travel-service/.../resources/application.yml", "lines": "42-58",
     "what": "no retry, ribbon read-timeout=2000ms; latency >2s gets translated into 504 at this caller"}
  ],

  // Filled in step 4 too: live data grounding from aegisctl traces / metrics.
  // What does the cluster ACTUALLY do right now (vs. what code suggests)?
  "data_evidence": "Recent ts1 trace sample: queryForTravel outbound spans hit ts-station (12 calls/req, p95 80ms), ts-train (4 calls, p95 30ms), ts-route (1, 60ms). Entrance /preserve p99 currently 1.2s — has headroom for ~1s injected latency before SLO breach.",

  // Filled in step 8: did the fault actually land?
  "injection_landed": true,                  // false → also set "outcome": "injection-noop"
  "injection_evidence": "abnormal-window p99 on ts-order-service jumped 8x; chaos resource Injected; spans show 5xx on /queryOrders",

  // Filled in step 9: simulate-the-RCA
  "simulated_rca": {
    "inference_chain_length": 4,             // hops from entrance symptom to true fault
    "decoy_hypotheses": [                    // plausible wrong stops on the way
      "ts-cancel-service slow (it's actually downstream)",
      "ts-order-other DB slow (it's the upstream caller's retry storm)"
    ],
    "estimated_difficulty": "high"           // low / medium / high — your honest call
  },

  // Filled retrospectively next round (step 2) when terminal events have landed
  "retro_grade": {
    "detector_verdict": null,                // "fired" | "silent" | null until known
    "slo_impact": null,                      // "violated" | "intact" | null
    "notes": null
  },

  // Reserved for the future offline RCA-grading query (don't fill manually)
  "pending_offline_rca": true,
  "offline_rca": null                         // {correct: bool, named_cause: "...", details: "..."} once graded
}
```

**`memory.md` is the highest-leverage file.** It's how you accumulate judgment across runs of `loop.sh`. Keep it short and useful — bullet form, dated entries, organized by theme:

- *Hard-puzzle templates that worked* — fault shapes where RCA missed (these are gold; reuse the *shape* with different params/targets).
- *System quirks* — endpoints that look critical but have a fallback; services that share a Redis / DB pool with a non-obvious sibling; durations below which the detector smooths the perturbation away.
- *Anti-patterns to avoid* — fault choices that turned out to be free wins for RCA, free silences for the detector, or environment-failures masquerading as candidate signal.
- *Open questions* — things to probe in future rounds.

Don't dump round-level data into `memory.md`; that belongs in `rounds/*.json`. `memory.md` is the *distilled* lessons. If it grows beyond ~200 lines, prune it; consolidate or delete entries that are no longer load-bearing.

## Diversity

Group faults by chaos_type **family** (the prefix). Within ~50 rounds, target rough evenness:

- pod-* (PodFailure, PodKill, ContainerKill) — ≤ 20%, bias low
- network-* (NetworkDelay/Loss/Partition/Bandwidth) — 20–25%
- jvm-* (JVMLatency/Exception/Return/Stress) — 20–25%
- http-* (HTTPDelay/Abort/Replace/StatusCode) — 15–20%
- db-* / mysql-* (DatabaseLatency/Abort) — 10–15%
- stress / dns / time-skew / others — ~10% combined as long-tail variety

Targets, not hard limits. The principle: **no family above ~30% in any 50-round window**. Cycling pod variants (PodFailure → PodKill → ContainerKill) is dedup bypass dressed up as diversity — RCA learns the same shape three times.

Within a family, vary the **target service**. Hammering one service with five JVM-method faults teaches RCA to suspect that service, not to actually reason.

## Per-round shape: two independent dimensions

A round has TWO independent fault-fan-out dimensions, and they should not be conflated:

- **K_outer = parallel TRACES per round.** Each trace is its own RCA puzzle, runs in its own ts/hs/sn namespace (auto-allocated via `--auto`), produces its own datapack, and is graded independently. K_outer ramps with the system's deployed-namespace pool (e.g. ts has 16 namespaces — K_outer up to ~12 leaves headroom for retries). This is **throughput**: more puzzles per unit wall-clock, more data per round.
- **K_inner = LEAVES per trace** (the `--apply --batch` shape). Multiple leaves *within one trace* fire in parallel within the same namespace, share one abnormal-window in the datapack, and are graded together — RCA must attribute the symptom to one or more of the K_inner faults. K_inner is **per-puzzle hardness**: a 2-leaf batch with mutual-masking leaves is a distinctly harder puzzle than two independent 1-leaf traces.

The two dimensions multiply: a round of K_outer=4 × K_inner=2 produces 4 separate traces, each containing 2 interacting leaves — i.e. 8 chaos resources, 4 datapacks, 4 RCA puzzles.

Don't merge the two dimensions in your head. "Multi-fault" alone is ambiguous; always say "K_outer parallel" or "K_inner-leaf batch" so it's unambiguous what shape you're submitting.

### K_outer guidance

Default to **K_outer ≥ 2** when the namespace pool has slack — independent puzzles per round are pure throughput win. Cap K_outer below the deployed pool size for the system (so namespace allocation has retry headroom) and below any backend rate-limit ceiling (BuildDatapackRateLimiter default 8; #289-style ClickHouse freshness-probe contention shows up around K_outer≥4 if the probe isn't partition-pruned). When in doubt, ramp K_outer upward across rounds and watch the BuildDatapack failure rate.

K_outer=1 is **not** a default — it's a deliberate choice when the round only has one good puzzle to pose this iteration (e.g. a deeply-considered K_inner=3 with a tight interaction hypothesis where parallel siblings would just add noise to the analysis).

### K_inner — the multi-fault batch (`--apply --batch`)

`aegisctl inject guided --stage` / `--apply --batch` submits multiple finalized configs as one experiment that fires **in parallel within a single namespace and one trace**. Use K_inner ≥ 2 when there's a real reason for the leaves to *interact within the same trace's data*:

- **Mutual masking** — A's symptom looks like B's effect and vice versa; RCA must disentangle.
- **Co-trigger / amplification** — cache-tier latency + DB-pool exhaustion produce a much bigger blowup than either alone, and RCA must resist blaming whichever shows up first in the trace.
- **Multi-root-cause grading** — many RCA frameworks return a single root cause; K_inner ≥ 2 tests whether they can return a set.

Suggested K_inner mix (drift gradually based on what's working):

- K_inner=1 — ~50–60% of traces
- K_inner=2 — ~25–35%
- K_inner=3+ — ~10–15% (only with a clear interaction hypothesis)

Don't K_inner for its own sake — two unrelated leaves crammed into one trace aren't harder than two separate traces, they're a single puzzle with random noise. The interesting K_inner batches share a service, a dependency, or a time window so symptoms entangle.

**K_inner ≠ K_outer.** Two unrelated faults on *different* namespaces are not a K_inner=2 batch — they're K_outer=2 single-leaf traces, which is exactly what you want for throughput. Don't merge them into one trace just to have a "bigger" batch.

Backend constraints (K_inner only): every spec in one `--apply --batch` must share `system`, `system_type`, and `namespace` (or all leave namespace empty for the auto-allocate path). Duration is `max` across the batch.

## Unit and platform traps

Recurring footguns; if you forget these, half the rounds become wasted environment noise:

- **Time windows are pinned in the backend; do not pass them.** `--duration`, `--interval`, `--pre-duration` are gone from `aegisctl inject guided`. The backend enforces a fixed schedule (restart 25 min → warmup 2 min → normal 5 min → abnormal 5 min); any per-spec `duration` you ship is silently overridden. Don't try to "stagger duration" to bypass dedup — pick a different chaos type / target instead.
- **Stress numerical fields are integers, no units.** `memory_size: 512` (MiB), `cpu_load: 80` (percent), `time_offset: 60` (seconds). String forms get rejected.
- **Backend dedupes byte-identical fingerprints.** Re-submitting the same `(system, ns, app, chaos_type, params)` tuple within the dedup window returns 200 with `data.items: []` and a `batches_exist_in_database` warning — looks "successful", no trace created. Time-window fields no longer participate in the fingerprint (they're fixed), so you must vary `(app, target_service, chaos_type, ...)` to get a fresh trace.
- **Network chaos pairs must already be observed in cluster traffic.** Freshly installed namespaces have no pairs. Either warm with traffic before submit, or pick chaos types that don't need pair pre-check (Pod*, JVM*, CPU/Memory, DNS-FQDN).
- **DNSError needs an FQDN cataloged in the resolver.** Short service names and arbitrary external hosts both fail; use `<svc>.<ns>.svc.cluster.local`.
- **JVMChaos requires `/bin/sh` in the target image.** Distroless / minimal-busybox images fail with chaos-daemon `exit 101`. Stick to systems whose containers have a real shell.
- **HTTPChaos on byte-cluster: inbound only.** byte-cluster CNI is Cello (`eni_shared`) on a Cilium IPVLAN datapath. Upstream chaos-mesh tproxy bridges pod eth0 — kernel rejects this on IPVLAN children, and historical advice was "skip HTTP* entirely." That's no longer true: as of `chaos-daemon:20260515-bridgeless` (deployed via `tproxyBridgeless: true` in `aegislab/manifests/byte-cluster/chaos-mesh.values.yaml`) tproxy installs TPROXY rules directly in the pod netns. **Inbound** chaos (HTTPDelay/Abort/Replace/StatusCode with default `target: Request` or `target: Response` on a service that *receives* HTTP) works end-to-end. **`direction: from` (egress / outbound chaos initiated by the pod) does not work** on this CNI — Cilium's tc-bpf egress program short-circuits before netfilter, so no iptables rule (REDIRECT, mangle, fwmark, anything) sees the packet. To express outbound semantics, target the callee's inbound instead. If a campaign genuinely needs caller-only egress chaos, skip it on byte-cluster (no current workaround). On other clusters where bridge mode works, both directions are still available.
- **PodKill restarts in seconds; PodFailure holds the pod down.** For sustained perturbation use PodFailure.
- **`system_type` may not equal the short code.** Check the cluster's seed (`data.yaml`) before writing the first spec — wrong type produces "mismatched system type" 500s.

## System-specific signal levels (empirical, prune as you learn)

Detector responsiveness varies wildly across benchmarks. Update `memory.md` per-system as you learn:

- **TrainTicket (ts)** — high signal; ~50–80% +1 on PodFailure / JVMException targeting on-path methods. Stable winners reproduce. Loop produces useful per-method posterior in 5–10 rounds.
- **otel-demo** — noise-floor signal; ~10–20% +1 regardless of chaos type / magnitude. R1 winners often fail to reproduce. Param-sweeping is futile until detector-side investigation; pause the loop with a `PAUSED.md` and surface to user.
- **sockshop** — ~zero detector signal across many rounds. Detector keys off something other than chaos magnitude. Pause similarly.
- **DSB stack (hs / sn / mm)** — historically required Go-SDK OTel endpoint fix (no scheme prefix); confirm spans land before grading rounds. tea-store has a jmeter firehose risk on chart < 0.1.4.

When a system shows persistent ~5–15% +1 over 5+ rounds with no parameter pattern, it's a detector-implementation issue, not a candidate-quality issue. Stop iterating, write `~/.aegisctl/injection-author/<system>/PAUSED.md` summarizing what was tried, and tell the user.

## Adapt difficulty

Without a live RCA reward, use your own step-9 **simulated inference-chain length + decoy count** as the in-loop control signal, then re-calibrate once offline RCA grading lands:

- Several rounds of "≤2 hops, 0 decoys" → puzzles are too easy. Move down the granularity ladder, multi-fault more, target shared-resource paths.
- Several rounds of "≥4 hops with multiple plausible decoys" plus `injection_landed=true` → sweet spot; hold and vary along family/service axes to avoid memorization rather than escalating further.
- Frequent `injection-noop` → not a difficulty problem, an environment problem. Stop tuning faults and revisit *Unit and platform traps* / cluster health.
- Detector silent on a fault that the data shows did perturb the system → real graceful-degrade signal — record but don't grade as a puzzle win.
- When offline RCA grading lands and consistently contradicts your past simulations (e.g. RCA solves rounds you tagged `estimated_difficulty: high`), calibrate down: your decoy-counting was too generous. Update `memory.md` with the corrected pattern and reuse it.

## Stop conditions

Stop or pause when:

- The user says stop or asks for a summary.
- You've cycled the ladder × family matrix and have no underrepresented cell to fill without repeating.
- The platform is showing damage — namespace pool exhausted, detector image not pulling, dedupe firing on every submission. That's a platform issue; flag it and pause until resolved.

## Aegisctl gap log

Every time aegisctl forced you to work around it — output format awkward to parse, a flag missing, a verb you expected absent, a query that needed raw mysql/kubectl/curl to answer, error messages that hid the cause — append **one line** to `aegisctl_gaps.md`:

```
2026-04-28T10:15+08 | wanted: count traces by detector verdict per system | issue: trace list -o ndjson has no detector field, had to join with task list by trace_id | workaround: jq join script | severity: medium
```

Format: `<iso-ts> | wanted: <X> | issue: <what aegisctl can't / makes hard> | workaround: <what you did> | severity: low|medium|high`. Keep each entry to one line. The user reviews this file periodically and ports recurring patterns into aegisctl improvements — that's how the CLI gets less hostile over time.

This is different from `memory.md`: that file captures judgments about the *target system*; `aegisctl_gaps.md` captures issues with the *tool itself*. Keep them separate so the user can grep one without wading through the other.

## Don't

- **Don't** open `mysql` / `kubectl` / `redis-cli` for things aegisctl can do. Flag the gap in `aegisctl_gaps.md` (so it surfaces to the user for fixing) and use the workaround for this round only.
- **Don't** kill the entrance pod as your "hard puzzle." Free win for RCA.
- **Don't** confuse "detector silent" with "puzzle hard."
- **Don't** dump round-level submit responses into `memory.md`. That goes in `rounds/*.json`. `memory.md` is the *distilled* judgment.
- **Don't** spam the same fault family or same target service. That's fake diversity.
- **Don't** treat a +1 from the detector as the win condition. RCA being wrong is the win condition.

## Autonomous loop

Use `loop.sh` (in this skill directory) to drive continuous rounds:

```
./loop.sh <system> [--rounds N] [--sleep SECS] [--engine claude|codex]
```

It calls `claude -p` (or `codex`) per round with a prompt that triggers this skill, points the agent at `~/.aegisctl/injection-author/<system>/`, and sleeps between rounds. Default sleep is 900s (15 min ≈ one inject→detect cycle), so memory accumulates *with* the result of the prior round visible. Tune `--sleep` if your system's pipeline is faster or slower.

The loop is intentionally dumb: state lives on disk, not in shell variables. You can Ctrl-C, edit `memory.md` by hand, and resume — the next agent run picks up exactly where the previous one left off.
