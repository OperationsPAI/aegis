# Reasoning: cross-benchmark state abstraction

Status: proposal / open for discussion
Owner: reasoning pipeline
Scope: `src/rcabench_platform/v3/internal/reasoning/` — state model, rule engine, observation layer

## Motivation

The reasoning pipeline was migrated from `rca_label/src/reason/` where it was
originally built and tuned against **TrainTicket**. During sockshop integration
testing (`sockshop0-users-pod-failure-wnhnql` datapack) the pipeline:

- successfully built the topology graph (88 nodes / 108 edges) ✓
- correctly detected the alarm span `front-end::GET /login` via
  `identify_alarm_nodes_v2` (27155× latency blow-up vs baseline) ✓
- correctly resolved injection nodes (pod `users-0`, container `coherence`) ✓
- matched rules and reached the reachable subgraph (container → pod) ✓
- ✗ terminated with `no_paths`: `detect_state_timeline` returned **0 state
  windows on every node** — including the clearly-anomalous front-end span.

Root cause on inspection: the propagator's rule engine keys off **state
windows**, which in turn come from `detect_state_timeline` — a per-node
metrics-based anomaly scorer. The state vocabulary it emits
(`CRASH_LOOP_BACK_OFF`, `OOM_KILLED`, `FREQUENT_GC`, `SLOW_HTTP`, `SLOW_DB`, …)
is effectively **TrainTicket's observability vocabulary**: these labels only
materialize when Java-Spring auto-instrumentation + a specific K8s metric
pipeline supply the underlying signals. Go (sockshop), C++ (DSB SocialNetwork /
MediaMicroservices) and other stacks emit a different — and generally
coarser — set of signals, so `detect_state_timeline` comes up empty, and the
rule engine cannot propagate past the first hop.

The original design intuition ("fault → deterministic features → state
transitions → rules") is sound. The leak is that **the rules' state
vocabulary is entangled with a single stack's observation vocabulary**, so
rules that are logically universal cannot mechanically transfer.

## Design goal

Decouple the rule engine from per-stack observation. Introduce an **abstract
canonical state layer** that the rules operate on, plus per-stack **observation
adapters** that translate each benchmark's raw signals into canonical states.
New benchmarks onboard by writing an adapter, not by editing the rules.

```
┌──────────────────────────────────────────────────────────────────┐
│  RULE ENGINE  (canonical states + topology kinds, cross-stack)   │
└─────────────────────────────▲────────────────────────────────────┘
                              │  consumes: canonical state windows
                              │
  ┌───────────────────────────┴───────────────────────────────┐
  │   CANONICAL STATE LAYER  (small, closed vocabulary)       │
  │   e.g. HEALTHY / SLOW / ERRORING / UNAVAILABLE / MISSING  │
  └──▲─────────────▲────────────────────▲─────────────────────┘
     │             │                    │
  [seed]       [trace adapter]     [metrics adapter]      [stack-specific
     │             │                    │                  augmenter]
fault_type     parent_span_id,      k8s.pod.cpu.usage,    JVM GC, DB client
→ initial      duration,            memory.rss,           metrics, OOM events,
 state on      http.status_code     restart_count         container reason codes
 injection
 node
```

## Proposal

### 1. Canonical state lattice (small, closed, cross-stack)

One small set of states applies to every `PlaceKind`. Candidate vocabulary
(subject to refinement — see open questions):

| Canonical state | Meaning |
|---|---|
| `HEALTHY` | Behaving like baseline |
| `SLOW` | Latency materially above baseline (adaptive threshold) |
| `ERRORING` | Elevated error rate (5xx / OTel ERROR / exceptions) |
| `DEGRADED` | Resource pressure or restarts without outright failure |
| `UNAVAILABLE` | Not serving (pod terminated, container crashed, endpoint absent) |
| `MISSING` | Expected in baseline but absent in abnormal window |

Every current fine-grained state (`SLOW_HTTP`, `SLOW_DB`, `CRASH_LOOP_BACK_OFF`,
`OOM_KILLED`, `FREQUENT_GC`, `HIGH_CPU`, `HIGH_MEMORY`, …) becomes either:

- **a specialization label** carried alongside the canonical state (multi-label:
  `{SLOW, SLOW_DB}` implies `SLOW`), or
- **derivable only when the augmenter adapter supplies the evidence**; absence
  just means "we don't know the specialization", not "not slow".

### 2. Fault type → deterministic initial state (no observation needed)

`StateEnhancer` today applies injection-point enhancement only for the subset
in `INJECTION_POINT_ENHANCEMENT_CATEGORIES`. Make it **universal** and
**contract-driven**:

| Fault type | Target node kind | Canonical seed state |
|---|---|---|
| `PodKill`, `PodFailure` | pod | `UNAVAILABLE` |
| `ContainerKill` | container | `UNAVAILABLE` |
| `CPUStress` | container | `DEGRADED` (+ optional `HIGH_CPU`) |
| `MemoryStress` | container | `DEGRADED` (+ optional `HIGH_MEMORY`) |
| `HTTPResponseDelay`, `HTTPRequestDelay` | span | `SLOW` |
| `HTTPResponseAbort`, `HTTPRequestAbort` | span | `ERRORING` |
| `NetworkDelay`, `NetworkLoss`, `NetworkCorrupt`, … | service / edge | `SLOW` or `ERRORING` |
| `DNSError`, `DNSRandom` | service (caller) | `ERRORING` |
| `TimeSkew` | pod | `DEGRADED` |
| `JVMException`, `JVMReturn` | span | `ERRORING` |
| `JVMLatency`, `JVMGarbageCollector` | span | `SLOW` |
| `JVMMySQLException`, `JVMMySQLLatency` | db-span | `ERRORING` / `SLOW` |

This table is **chaos-tool contract**, not inference. The injection node
carries the seed state regardless of what the observation pipeline sees, so
propagation always has something to start from.

### 3. Observation layer: two adapters, clean separation

#### a) Trace adapter (universal — every OTel stack)

Drives state for **spans and services**. Inputs: trace parquets only.

- `span.SLOW` ← adaptive threshold on duration (already implemented inside
  `identify_alarm_nodes_v2`, extract as a shared primitive)
- `span.ERRORING` ← rate of HTTP 5xx / OTel ERROR / span.status=ERROR
- `span.MISSING` ← present in baseline, absent in abnormal window
- `service.DEGRADED` / `UNAVAILABLE` ← derived: root span of service is
  SLOW / ERRORING / MISSING
- `edge.SLOW` / `edge.ERRORING` ← child-span anomaly attributable to a specific
  call edge

#### b) K8s-metrics adapter (universal — any OTel K8s receiver)

Drives state for **pods and containers**. Restricted to the **common K8s
metrics** every cluster with `k8s-cluster` receiver emits:

- `k8s.pod.phase`, `k8s.container.restarts` → `UNAVAILABLE` / `DEGRADED`
- `k8s.pod.cpu.usage`, `k8s.pod.memory.rss` (with baseline-adaptive thresholds) → `DEGRADED`
- `k8s.pod.network.errors`, `k8s.pod.network.io` → `DEGRADED`

No Java/JVM/DB-client metrics required. (They may be layered on — see next.)

#### c) Stack-specific augmenters (optional, per benchmark)

Additional adapters that contribute **specialization labels** on top of the
canonical state. E.g.:

- JVM augmenter (TrainTicket, TeaStore): adds `FREQUENT_GC` / `OOM_KILLED`
- DB-client augmenter: adds `SLOW_DB` if span name matches a DB pattern and
  duration anomalous
- Python/Go runtime augmenters as needed

Augmenters must **not** be required for any rule to fire. Their absence only
degrades root-cause explanation quality, never correctness.

### 4. Rule classification

Audit `rules/builtin_rules.json` (13 rules today) and reclassify:

- **Core rules**: operate on canonical states, required for cross-stack
  operation. Expected ~10–15 rules covering `UNAVAILABLE → UNAVAILABLE`
  (container → pod → service → span), `SLOW → SLOW` (callee → caller),
  `ERRORING → ERRORING`, etc.
- **Augmentation rules**: operate on specialization labels, fire only when
  augmenter adapter produced them. These are the existing rules keyed on
  `HIGH_CPU`, `OOM_KILLED`, `SLOW_DB`, etc. Keep as-is; just acknowledge
  they're optional.

### 5. Onboarding a new benchmark (target UX)

```
1. Deploy the benchmark with OTel traces + k8s-cluster metrics (already the
   baseline requirement for any datapack).
2. Run detector to produce normal/abnormal parquets (unchanged).
3. Run reason. Works out of the box via trace adapter + k8s-metrics adapter.
4. Optional: implement a stack-specific augmenter if you want richer
   specialization labels in the output.
```

No rule edits. No state-enum edits.

## Migration plan

Phased — goal is zero regression on TrainTicket while unlocking the other
benchmarks.

| Phase | Work | Gate |
|---|---|---|
| 0 | Land this doc + issue-tracker ticket; collect corrections | design sign-off |
| 1 | Extract the `identify_alarm_nodes_v2` span-state derivation into a named `TraceStateAdapter`; same code paths, just refactored | no behavior change |
| 2 | Implement `CanonicalState` enum + per-PlaceKind canonical-state fields on graph nodes (alongside existing fine-grained fields) | green on TT |
| 3 | Wire trace-adapter + k8s-metrics-adapter to populate canonical states; populate specialization labels too when fine-grained state_detector output is available | green on TT + sockshop `no_paths` case now produces paths |
| 4 | Write JVM-augmenter as first example of specialization adapter. Prove TT's fine-grained rules still fire through the new pipeline | TT regression suite clean |
| 5 | Audit 13 built-in rules: reclassify core vs augmentation, add any missing core rules (expected: a couple canonical-`SLOW`/`ERRORING` propagation rules) | — |
| 6 | Enforce deterministic fault-type → seed-state table as a non-optional first step of `InjectionNodeResolver` / `StartingPointResolver` | sockshop green end-to-end |
| 7 | Integration sweep: sockshop, HotelReservation, SocialNetwork, MediaMicroservices, TeaStore datapacks should all produce non-trivial causal graphs without stack-specific code changes | — |

## Open questions

1. **State lattice size** — is 6 states enough, or do we need an intermediate
   level (e.g. split `DEGRADED` into `RESOURCE_PRESSURE` vs `RESTARTING`)? I'd
   start minimal and grow by demand.
2. **Multi-label representation** — currently node state is
   `frozenset[str]`. Keep that, with canonical-state members coexisting with
   specialization labels in the same set? Or split into two fields (more
   explicit, more work)?
3. **Backward compat of `causal_graph.json`** — downstream consumers read the
   existing `state` field. If we keep it as a set of string labels but now the
   set contains `{"slow", "slow_db"}` instead of just `{"slow_db"}`, is that
   acceptable, or does the schema need versioning?
4. **TrainTicket regression coverage** — do we have a stable TT datapack +
   expected `causal_graph.json` that we can gate phase-2/3/4 against?
5. **Rule authoring surface** — when core rules and augmentation rules
   coexist, do we want a single `builtin_rules.json` with a `tier` field, or
   separate files?
6. **Alarm → state bridge as a transitional shortcut** — while phases 1–3 are
   in flight, should we land the minimal "alarm-span → SLOW state window"
   bridge (Plan A from earlier discussion) as a stopgap so sockshop / dsb
   benchmarks aren't blocked for weeks? Lean yes if migration is slow.

## Non-goals

- Rewriting the rule engine itself (rule matcher / BFS / subgraph extraction).
  The rules stay, their semantics stay; we only change the state vocabulary
  they operate on and how that vocabulary is populated.
- Abandoning the metrics-based state detector. It becomes the JVM / Java-stack
  augmenter; it just stops being the mainline.
- Changing the datapack contract (parquet layout, injection.json schema, file
  locations).
