# Reasoning: Fault-Driven Feature Taxonomy

Status: methodology — locked, governing all future state and adapter additions
Owner: reasoning pipeline
Scope: `src/rcabench_platform/v3/internal/reasoning/`

This document defines the methodology by which all decisions about canonical
states, adapters, and inferred edges in the IR are made. Every change to the
state lattice or adapter set is required to argue from this framework.

It is the companion to [reasoning-state-abstraction-proposal.md](./reasoning-state-abstraction-proposal.md):
the abstraction proposal said "decouple states from per-stack vocabulary";
this document says "and here is how to decide *which* canonical states the
lattice needs".

---

## 1. Motivation

The IR has accumulated states (`HEALTHY / SLOW / ERRORING / DEGRADED /
UNAVAILABLE / MISSING / UNKNOWN`), adapters (k8s metrics, traces, JVM,
structural inheritance, inferred edges, log-evidence), and rules (11 core
+ augmentation tier). Each addition was justified case-by-case. Over time
this becomes hard to defend and hard to extend:

- when reviewers ask "why these states and not others?", we have a
  list of historical reasons but no closed answer;
- when a new fault type arrives, it is unclear whether to extend an
  adapter, add a label, add a state, or invent a new mechanism;
- when a new benchmark stack arrives, the adapter writer has no
  scaffold for what they need to detect.

This document fixes a single methodology that answers all three. Every
fault is mapped to one **observation class** by its *primary physical
mechanism*. Each class has exactly one canonical representation in the
IR, and exactly one adapter family observing it. Cascades are not
written; they emerge from the same adapters running at neighbouring
entities.

---

## 2. The three-layer mapping

```
[1] Fault Type                 chaos-mesh / JVM agent / HTTP proxy
        │                           (what the operator injects)
        ▼
[2] Primary Feature(s)         the *physical* change
        │                          (kernel qdisc / JVM / TCP / pod)
        ▼
[3] Observation Surface        an adapter watching a sensor stream
        │                          (k8s metrics / OTel traces / logs / JVM mbean)
        ▼
[4] Canonical Representation   a state, label, or edge in the IR
```

A fault has **exactly one primary feature class** (§3). It may also have
secondary features observable at the same or other entities, but those
are coincidental — they are independently classified as primary features
of *something else* observed at a different place, and the IR does not
need to reason about a "primary→secondary" implication.

Cascade across the dependency graph is **not a fifth layer**. When fault
F at entity E causes downstream entity E' to behave abnormally, the
abnormality at E' is itself a primary feature observed independently by
the relevant adapter at E'. The IR connects these independent
observations through the dependency graph in `algorithms/propagator.py`;
it does not derive E' from E.

This is the core architectural commitment: **observation, not
derivation**.

---

## 3. The six observation classes

Classes partition faults by the layer at which the primary feature is
physically introduced. Two faults share a class iff their primary feature
is observed by the same adapter on the same sensor stream with the same
canonical representation.

### 3.A — Infrastructure death

| | |
|---|---|
| **Definition** | Container or pod stops servicing requests because the kernel-level resource (process / cgroup / pod sandbox) is gone or terminating |
| **Physical mechanism** | SIGKILL on container, pod terminating, OOMKill, kubelet eviction |
| **Observation surface** | `K8sMetricsAdapter` — `container.ready=false`, `restarts++`, pod phase transitions |
| **Canonical state** | `container` / `pod` → `unavailable` |
| **Cascade representation** | Inherited up to `service.unavailable` and `span.missing` by `StructuralInheritanceAdapter` (#197). Caller-side ERRORING is observed independently as Class C at the caller's spans. Inferred trace edges added by `enrich_with_inferred_edges` (#198) close trace-blind gaps when the dead infra has no spans. |

### 3.B — Resource pressure

| | |
|---|---|
| **Definition** | Container or process is alive but resource utilization is high enough to slow request handling |
| **Physical mechanism** | CPU throttling, memory near limit (no OOM), thread pool exhaustion |
| **Observation surface** | `K8sMetricsAdapter` — usage metrics exceed adaptive threshold |
| **Canonical state** | `container` → `degraded` |
| **Cascade representation** | Slowness at the spans is observed independently as Class D at trace level |

### 3.C — Logical error

| | |
|---|---|
| **Definition** | Application or protocol layer produces an error response or throws an exception. Process and infra are healthy. |
| **Physical mechanism** | JVM byte-code mutator throws, HTTP response replaced with 5xx / aborted, packet payload corrupted, auth token rejected |
| **Observation surface** | `TraceStateAdapter` — span `status_code ≥ 500`, exception attribute present, error rate over baseline |
| **Canonical state** | `span` → `erroring` |
| **Cascade representation** | Caller spans observe ERRORING independently when their downstream returns 5xx |

### 3.D — Latency injection

| | |
|---|---|
| **Definition** | Calls succeed but take significantly longer than baseline. No errors, no infra failures. |
| **Physical mechanism** | JVM thread sleep, HTTP response held, kernel netem delay, bandwidth throttle (small enough to enqueue rather than drop), packet loss with retry |
| **Observation surface** | `TraceStateAdapter` — span `duration / p_baseline ≥ adaptive_threshold` |
| **Canonical state** | `span` → `slow` |
| **Cascade representation** | Caller spans observe SLOW independently |

### 3.E — Traffic isolation *(currently unrepresented)*

| | |
|---|---|
| **Definition** | The entity is alive and configured normally, but **expected request flow has dropped to near zero**. Distinct from MISSING (no datapoint to interpret) and from UNAVAILABLE (infra failed). The fault is *between* caller and callee, not at either, but the observable symptom lives at one side. |
| **Physical mechanism** | NetworkPartition, NetworkLoss at high percentage, DNSError preventing resolution, NetworkBlackhole |
| **Observation surface** | `TraceVolumeAdapter` (planned) — per-service span rate. Trigger and threshold are derived per §11.2 (one-class baseline-quantile calibration at policy α); no hand-set ratio constants |
| **Canonical state** | `service` / `span` → **`silent`** *(planned new state)* |
| **Cascade representation** | The fault's downstream effect is itself another `silent` observation at the next entity, made independently by the same adapter — no "chain rule" needed |

### 3.F — Runtime internal state

| | |
|---|---|
| **Definition** | Application runtime (JVM, .NET CLR, Go runtime) reports an internal anomaly that an external observer cannot distinguish from healthy operation by traces alone |
| **Physical mechanism** | JVM stop-the-world GC, heap pressure, finalizer queue backlog, GC root scan stuck |
| **Observation surface** | `JVMAdapter` — JVM-specific metrics (GC pause time, heap usage, thread states) |
| **Canonical state** | `frequent_gc`, `high_heap_pressure`, `oom_killed` (specialization labels — class F is augmentation, not core) |
| **Cascade representation** | Class B / D usually emerge concurrently when Class F is severe enough to affect request handling |

---

## 4. Fault-to-class map (all 31 fault_types)

| Fault Type (chaos-mesh) | Primary Class | Notes |
|---|---|---|
| `PodKill`, `PodFailure`, `ContainerKill` | **A** | |
| `MemoryStress` (when triggers OOM kill) | **A** | upgraded from B at OOM threshold |
| `CPUStress`, `MemoryStress` (sub-OOM) | **B** | |
| `JVMCPUStress`, `JVMMemoryStress` | **B** + **F** | dual signature |
| `JVMGarbageCollector` | **F** | |
| `JVMException`, `JVMReturn`, `JVMRuntimeMutator` | **C** | |
| `JVMLatency` | **D** | |
| `HTTPRequestAbort`, `HTTPResponseAbort` | **C** | |
| `HTTPResponseReplaceCode`, `HTTPResponseReplaceBody`, `HTTPResponseReplaceMethod` | **C** | |
| `HTTPRequestDelay`, `HTTPResponseDelay` | **D** | |
| `NetworkCorrupt` | **C** | bytes garbled → receiver throws |
| `NetworkDelay` | **D** | |
| `NetworkBandwidth` | **D** | spans arrive late, no drops at low traffic |
| `NetworkLoss` (< ~50%) | **D** | retries make it look slow |
| `NetworkLoss` (high %), `NetworkPartition` | **E** | spans vanish on the affected side |
| `NetworkDuplicate` | **C** or **D** depending on protocol layer | edge case, document on first encounter |
| `DNSError`, `DNSRandom` | **E** (outgoing direction) | resolution fails → no calls go out |
| `TimeSkew` | **C** | auth/cert validation rejects |

Every fault has a unique primary class. **If a future fault does not fit
any class, that is the signal to add a new class** (§7).

---

## 5. Within-class collisions are acceptable; cross-class distinctness is required

**Within-class:** several faults map to the same primary signature with
no observable distinguisher in the IR's chosen sensor surface. Examples:

- Class **D** collisions: `JVMLatency`, `NetworkDelay`, `NetworkBandwidth`,
  `HTTPDelay`, `NetworkLoss(low%)` — all manifest as `span.slow`.
- Class **C** collisions: `JVMException`, `HTTPResponseAbort`,
  `NetworkCorrupt`, `TimeSkew` — all manifest as `span.erroring`.

This is a deliberate design choice. RCA evaluation scores top-k root
causes against ground truth. The propagator finds paths from injection
to alarm; injection is known from the fault config. Distinguishing
"why is this span slow" requires sensor sources we do not have or do
not want to maintain (kernel tracepoints, stack profilers, `tcpdump`),
and would not improve ranking.

If finer distinction becomes useful for a downstream task (e.g. fault
explanation rather than root-cause ranking), it goes through the
`#188` specialization-label / augmentation-rule mechanism — **not** by
expanding the core lattice.

**Cross-class:** distinct classes must have distinct canonical
representations. Two faults from different classes that produced the
same state would corrupt the propagator's ranking — for example,
treating a Class E "downstream is starved" the same as a Class A "infra
is dead" would let the propagator nominate a cascade victim as a
root-cause candidate.

This is why §3.E demands a new state (`silent`) rather than reusing
`unavailable` or `missing` — those are reserved for Class A and for
"no data to interpret" respectively, and conflating either with
Class E would violate cross-class distinctness.

---

## 6. Cascade emerges from observation, not derivation

The IR's cascade-handling components — `StructuralInheritanceAdapter`
(#197), `enrich_with_inferred_edges` (#198), `LogDependencyAdapter`
(per-system) — are **aggregators of independent observations**, not
derivers of new facts.

To illustrate, take Class E (NetworkPartition on `ts-verification-code`):

```
observation surface           entity                         canonical repr
───────────────────────────   ──────────────────────────     ──────────────
TraceVolumeAdapter            service|ts-verification-code    silent
TraceVolumeAdapter            service|<any caller of vc>      (per-edge silent — not implemented yet)
TraceVolumeAdapter            service|<downstream of vc>      silent
TraceStateAdapter             span|<caller>::<call_to_vc>     erroring (timeout) or slow
K8sMetricsAdapter             container|<vc>                  healthy (kernel-level partition not visible)
```

Each row is an independent observation. None depends on another being
correct. The propagator stitches them via dependency edges in the graph.

For Class A (PodKill on `mysql`):

```
TraceVolumeAdapter            service|mysql                   silent (also fires)
K8sMetricsAdapter             container|mysql                 unavailable
StructuralInheritance (#197)  service|mysql                   unavailable (inherited)
LogDependencyAdapter (TT)     service|mysql -[includes]→ ...  inferred edge from caller logs
TraceStateAdapter             span|<caller>::SELECT user      erroring
```

Multiple classes (A and E) both fire — A is primary, E is consequential
but observed in its own right. The propagator does not need to know
which is primary; it only needs paths.

This is the property that lets us add new fault types without growing
combinatorial cascade rules: each new fault type is matched against
§3, lands in some class, the corresponding adapter already produces
the canonical representation, and downstream cascade is handled by
existing aggregators.

---

## 7. Procedure: adding a new fault type

When a new chaos-mesh primitive (or other fault tooling) is added:

1. Identify the **physical layer** at which the fault is introduced
   (kernel / JVM / HTTP / DNS / time / process).
2. Compare to §3. Place the fault in the matching class.
3. If the matching class is **A / B / C / D / F**, no IR change is
   needed; ensure `models/fault_seed.py` maps `fault_type` to the
   canonical seed state (per #185), and confirm the adapter already
   detects the relevant signal on the relevant sensor.
4. If the fault does not fit any class, **stop and revise this
   document**. A genuinely new class requires:
   - new entry in §3 with definition, mechanism, surface, state
   - new adapter family with explicit "owns Class X" contract
   - new canonical state added to `ir/states.py` if Class X has no
     existing state
   - cross-class distinctness verified against §5
5. Add the fault to §4's table.
6. Add a fixture test that drives the fault end-to-end through the IR
   and asserts the expected canonical state appears.

A new class addition is therefore **explicit and observable in this
document**. The lattice does not grow silently.

---

## 8. Procedure: adding a new benchmark / stack

Per-system adapters are pattern carriers, not new classes. To onboard
benchmark B:

1. For each existing class, identify the stack-specific signature in
   B's logs / metrics / traces (e.g. JVM uses `HikariPool` for Class A
   DB death, Go uses `dial tcp ... refused`).
2. Subclass the class's adapter family (e.g. `LogDependencyAdapter` for
   log-evidence-driven Class A/E coverage). Subclass declares an
   `applies(observations)` gate and the stack-specific patterns.
3. Register the subclass with `@register_<adapter_family>(...)`.
4. The IR pipeline picks up the new adapter automatically; no rule or
   state change required.

Per-system subclasses live in the same module as the family and do
not modify the framework.

---

## 9. Procedure: adding a new canonical state

A new state is added when, and only when, §7 step 4 is reached — a
genuinely new observation class is being added.

Process:

1. Add the state name to `ir/states.py` (`PerKindState` enums for the
   `PlaceKind`s where it can appear). Severity rank is assigned by the
   tier admission table in §11.1 — this is procedural, not a free choice.
2. Update `test_states_invariants.py` — every new state must be
   covered by partial-order assertions and round-trip tests.
3. Update `propagator.py` and `rule_matcher.py` to handle the new
   state in transition matching.
4. Update `inferred_edges.py::_INFRA_FAULTY_STATES` /
   `structural_inheritance.py` if the new state should participate in
   cascade aggregation.
5. Add documentation here in §3 with the four-row table.

A state is **not** added for:

- finer distinctions within an existing class (use specialization
  labels per #188);
- coverage of edge cases (use stricter adapter thresholds);
- per-stack idioms (use per-system adapter subclasses per §8).

---

## 10. Current IR audit (2026-04-25)

| Class | Primary state | Owning adapter | Status |
|---|---|---|---|
| A — Infra death | `container.unavailable` → `service.unavailable` (via #197) → `span.missing` | `K8sMetricsAdapter` + `StructuralInheritanceAdapter` + `enrich_with_inferred_edges` + `LogDependencyAdapter` | ✅ |
| B — Resource pressure | `container.degraded` | `K8sMetricsAdapter` | ✅ |
| C — Logical error | `span.erroring` | `TraceStateAdapter` | ✅ |
| D — Latency | `span.slow` | `TraceStateAdapter` | ✅ |
| E — Traffic isolation | **`silent`** (state to be added) | **`TraceVolumeAdapter`** (to be added) | ❌ planned |
| F — Runtime internal | specialization labels (`frequent_gc`, etc.) | `JVMAdapter` | ✅ |

Class E is the single open gap. Any further design discussion that
proposes a new state must either (a) be Class E and align with this
audit, or (b) introduce a new class via §7.

---

## 11. Decision methodology

The framework so far has decided **what** to detect (classes A–F) and
**how** to combine evidence (cascade aggregation). It has not yet
specified **how the specific values** in the IR — severity ranks,
adapter thresholds, rule confidences — are chosen. This section defines
the procedures so that every value is the *output* of a procedure, not
the input.

Three procedures, each consuming a different evidence source:

| Decision | Source | Output rule |
|---|---|---|
| Severity rank of a state | Operational impact tier | One of 0–5 by tier admission |
| Adapter detection threshold | Per-case baseline distribution of a discriminator Q | `quantile(α, baseline_distribution)` per service |
| Rule confidence | Baseline structural premise + Beta prior calibrated on the labeled fault corpus | `P_struct(case) × P_causal(corpus)` |

A new state, threshold, or rule is admitted only after running the
matching procedure and recording inputs (so a reader can re-derive the
value).

### 11.1 Severity tier methodology

Severity controls the multi-adapter merge in `synth`: when two adapters
emit different states for the same node at the same `at`, the higher
severity wins.

Tier admission table — the lowest-numbered tier whose condition holds is
the assigned tier:

| Tier | Admission condition | Members |
|---|---|---|
| 0 | No observation, no signal | UNKNOWN |
| 1 | Node operating normally | HEALTHY |
| 2 | Latency above baseline; all requests still succeed | SLOW |
| 3 | Resource/capacity-layer indicator exceeded but request layer not yet failing | DEGRADED, RESTARTING |
| 4 | Sustained request-level failure observable; process still alive | ERRORING, **SILENT** |
| 5 | Node-level recovery required (process/sandbox dead, or observation lost) | UNAVAILABLE, MISSING |

Procedure for placing a new state:

1. Write a one-sentence physical description.
2. Find the lowest tier whose admission condition is satisfied.
3. severity = that tier number.
4. If no tier matches, the tier table itself must be extended (separate
   PR + review) before the state lands.

This pins **SILENT at tier 4** with no remaining freedom: requests at
the node have ceased flowing (sustained request-level failure observed)
but k8s health checks pass (process alive). Tier 5 does not apply
because no node-level recovery is required — a partition heals and
traffic resumes; the process stays the same.

### 11.2 Adapter threshold methodology

**One-class baseline calibration.** Each adapter detects deviations from
healthy operation by treating the case's own baseline window as the
ground-truth healthy distribution.

Inputs (per service, per case):

- A discriminator `Q` with an exact formula on observed data.
- The case's baseline window of healthy traffic.
- A policy parameter `α` (global; see §11.4).

Procedure:

1. Slice the baseline window into sliding sub-windows of the same length
   as the abnormal window for this case (stride = `_BUCKET_SECONDS`).
   The slice range is **per-service**: anchored to that service's own
   active baseline range `[svc_ts_min, svc_ts_max]` clamped within the
   global baseline window. A service that was deployed mid-baseline (or
   only fires sporadically) is calibrated against its active range, not
   the global window — otherwise zero-count subwindows from "before the
   service existed" would dominate the distribution and pin the threshold
   at zero.
2. Compute Q on each baseline sub-window. The set of values is the
   service's empirical healthy distribution of Q.
3. Bootstrap-stability check: resample the distribution `B = 200` times,
   compute `quantile(α, resample)` each time. Compute
   `rel_std = std(quantile estimates) / max(IQR(distribution), ε)` with
   `ε = 1e-9` to handle constant-baseline degeneracy. If `rel_std > 0.10`,
   the service opts out of detection for this case (insufficient baseline
   samples for a stable quantile). **IQR-scaling, not mean-scaling,
   because the quantile of interest may legitimately sit near zero
   (e.g. SILENT's Q has lower tail near 0 by construction). Scaling by
   `|mean|` would inflate `rel_std` for any near-zero quantile even when
   the bootstrap variance is small in absolute terms; IQR captures the
   data's natural scale and is independent of where the quantile lands.**
4. Set threshold:
   `T(svc, case) = quantile(α, distribution(svc, case))`
5. At inference: compute Q once on the abnormal window (case-level
   aggregate, single test). Emit the adapter's state iff Q crosses
   `T(svc, case)` in the tail direction.

Properties:

- **Per-(svc, case) false-positive rate is bounded by α by construction.**
  No labeled fault data is needed to set this. Recall on actual faults
  is not directly controlled — a fault that does not push Q outside the
  baseline distribution is missed by design.
- A service whose natural variation is wide gets a wide threshold; a
  spiky endpoint cannot drag healthy services into false alarms.
- Calibration is per-case and shares the baseline already loaded by the
  pipeline. No global stored state.

Adapters this applies to:

- **TraceVolumeAdapter** (planned, Class E): `Q = rate(abnormal) / mean(baseline_rate)`
- **TraceStateAdapter** (Class C/D): per-(span, trigger) Q for latency and error rate
- **K8sMetricsAdapter** (Class B): per-metric Q

Class A is binary (restart-counter delta, end-of-window blackout) — no Q
is needed; it stays as-is. The current `BaselineAwareDetector` uses a
fixed-`kσ` threshold; migration to quantile-based is incremental, one
adapter per PR.

### 11.3 Rule-confidence methodology

A rule's edge weight in the propagator is decomposed:

```
confidence(firing of R, on case C) = P_struct(R, C) × P_causal(R)
```

- **P_struct(R, C)** — probability that R's structural premise holds at
  the entities R fires on, in case C. Measured at runtime from the
  case's own baseline traces (e.g. for a `calls`-edge rule, the
  empirical frequency the parent–child span pair appears together in
  baseline). No fault data required.
- **P_causal(R)** — probability that R's downstream-state claim is
  correct *given* the structural premise. Calibrated globally across
  the labeled fault corpus.

`builtin_rules.json` stores **only `P_causal`**; the runtime multiplies
in case-specific `P_struct` at firing time.

Initial value (before any fault calibration):

```
P_causal(R) = posterior_mean(Beta(α=2, β=2)) = 0.5
```

Beta(2, 2) is weakly informative: pseudo-count 4, prior mean 0.5. After
observing the labeled corpus, posterior mean is
`(correct + 2) / (fired + 4)`. The pseudo-count of 4 means it takes
~10 observations to substantially move the estimate, preventing
small-N flapping.

Calibration procedure (re-run at each release):

1. For each rule R: track `(fired_count, correctly_used_count)` across
   the labeled corpus. *"Correctly used"* = R appears in a path
   connecting the ground-truth root cause to an alarm.
2. `P_causal(R) = posterior_mean(Beta(correct + 2, fired − correct + 2))`.
3. The JSON entry for R carries:
   - the numeric `P_causal`,
   - `calibration_n` — corpus size used,
   - `provisional` flag if `calibration_n < 50`.

Confidences are not hand-set after the first deploy. The table evolves
only via re-calibration commits. The pre-existing rules (which currently
have hand-set values 0.8 / 0.85 / 0.9) will be re-interpreted as
`P_causal` priors during the first calibration run.

### 11.4 Policy parameters

The only free dials in the system; everything else flows from data.

| Parameter | Value | Why this value |
|---|---|---|
| α — quantile FP budget per (svc, case) | `0.01` | A typical case has ~10 services. At α=0.01 case-level FP rate ≈ `1 − 0.99^10 ≈ 9.5%`. At α=0.05 case-level FP rate ≈ 40% — comparable to or larger than typical TP rates, dominating ranking. At α=0.001 case-level FP ≈ 1% but requires N ≳ 1000 baseline sub-windows for a stable `q_0.001` estimate, exceeding our typical ~120-sub-window baselines. |
| Beta prior for `P_causal` | `Beta(2, 2)` | Pseudo-count 4 → ~10 observations to move posterior mean by 0.1. `Beta(1, 1)` (uniform) over-trusts the first 1–2 fault cases; `Beta(10, 10)` hard-pins at 0.5 for too long. |
| Bootstrap stability bound | `std(quantile) / IQR(data) ≤ 0.10` | Bootstrap quantile-estimate std stays within 10% of the data's natural scale. IQR (not mean) is the denominator so the metric works for quantiles near zero — see §11.2 step 3. Below this services opt out: the quantile is not stable for the available baseline N. |
| Sub-window stride | `_BUCKET_SECONDS` (5 s) | Same temporal grid as the rest of the IR. Smaller stride is heavier compute with no recall gain at our cadences. |
| Sub-window length | abnormal-window length (per case) | Equal-length comparison removes window-size confounding. Per-case because abnormal-window length varies. |
| Calibration scope | per-case | Calibration runs once per reasoning invocation, sharing baseline already loaded. Per-corpus calibration loses case-specific load patterns and ties cases to a calibration cadence. |

Changing any of these is a documented policy decision (separate PR; the
PR must show measured FP / recall on a labeled subset before/after).

### 11.5 Procedure templates

When adding a new state, threshold, or rule, the PR description must
include the matching template, fully filled in:

**New state:**

1. Physical description (one sentence).
2. Tier admission match (which row of §11.1).
3. Severity = tier number.
4. If no row matches, attach a separate PR extending §11.1 first.

**New adapter threshold:**

1. Discriminator Q (formula).
2. Calibration code path (must implement §11.2).
3. α (default `0.01`; deviation requires justification).
4. First-run measurement: distribution shape, FP@α on a baseline-only
   re-run, opt-out rate.

**New rule:**

1. Structural premise (e.g. *"src has `includes` edge to dst"*).
2. `P_struct` measurement at runtime (formula in code).
3. Initial `P_causal = 0.5` (Beta(2, 2) prior) unless calibration data
   exists.
4. `calibration_n` after first labeled run.

A PR that adds any of the above without these fields is reverted.

---

## 12. Glossary

- **Primary feature**: the change a fault makes to the running system,
  located at the physical layer where injection occurs.
- **Observation surface**: a sensor stream + an adapter that detects a
  feature on it.
- **Canonical representation**: a state value (PerKindState), a
  specialization label, or a HyperGraph edge (with `EvidenceLevel`)
  that the rule engine consumes uniformly.
- **Cascade aggregator**: a graph-mutating component that connects
  independent observations across entities (not a deriver).
- **Class collision**: two faults producing the same primary canonical
  representation. Within-class collision is fine; cross-class is a
  design bug.
- **Augmentation tier**: rules that fire only when specialization
  labels are present, used to refine ranking without expanding the
  core lattice (per #186).

---

## 13. Change log

| Date | Change | Driver |
|---|---|---|
| 2026-04-25 | Initial methodology fixed; six classes (A–F) defined; Class E identified as the only current gap | Need a defensible framework before extending the state lattice further |
| 2026-04-26 | §11 added — decision methodology fixed: severity by operational tier, adapter thresholds by per-case baseline quantile (α policy), rule confidence factored as `P_struct(case) × P_causal(corpus)` with Beta(2, 2) prior. §3.E's hand-set 0.2 / 10% / 2× constants replaced by reference to §11.2 | Reviewer-defensible derivation of every numeric value; remove ad-hoc thresholds |
| 2026-04-26 | §11.2 step 3 + §11.4 row revised — bootstrap-stability metric changed from `std/|mean|` to `std/IQR(data)`. Caught during L2 calibrator implementation: lower-tail quantiles legitimately sit near zero (SILENT's Q lower tail by construction), and `std/|mean|` structurally inflates `rel_std` for near-zero quantiles even when the bootstrap is stable. IQR-scaling is independent of where the quantile lands | Methodology must be self-consistent for the discriminators it advertises (TraceVolume's lower-tail Q is the motivating case) |
| 2026-04-26 | §11.2 step 1 clarified — sub-window slicing is per-service, anchored to each service's active baseline range, not the global baseline window | Caught during L3 TraceVolumeAdapter implementation: services deployed mid-baseline (or sparsely active) would otherwise have their Q distribution dominated by zero-count subwindows from "before they existed", pinning the threshold at zero |
