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
| **Observation surface** | `TraceVolumeAdapter` (planned) — per-service span rate. Trigger and threshold are derived per §12.2 (one-class baseline-quantile calibration at policy α); no hand-set ratio constants |
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
any class, that is the signal to add a new class** (§8).

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

### 6.1 Critical-path Class E and "global silence" — cascade, not confounder

A natural temptation when many services go SILENT in the abnormal
window is to subtract the "global drop ratio" from each per-service
ratio and flag only the residuals. **This is wrong** for the
methodology's core claim.

When the entity hit by a Class E fault sits on the system's critical
path (auth, verification, gateway, DNS), the propagation pattern is
*by design* a cascade of independent silence observations:

```
   user → ts-ui-dashboard → ts-verification-code → (partition) ✕
            ↓ (login fails)
   ts-ui-dashboard returns error → user retries less or backs off
            ↓
   loadgen → all subsequent flows (booking, payment, …) → never run
            ↓
   ts-order, ts-travel, ts-payment, … → drop to ~0 spans
```

Every drop in the chain is a real, locally-observable Class E signal.
The fact that "everyone is silent" is not noise to subtract; it is the
multi-observation footprint of one critical-path fault. Subtracting
global drop would suppress *exactly* the highest-impact Class E cases.

The propagator handles ranking among co-silent entities through path
length from the injection point — no adapter-level disambiguation
needed. If multiple entities are flagged silent, the one closest to
the alarm (by graph distance) ranks highest; the propagator's existing
score machinery handles this without methodology change.

This restates §6's "observation, not derivation" commitment for the
specific case of multi-entity silent cascades.

A Class E cascade is, however, **causally time-ordered**. The partition
target / DNS source goes silent first; immediate callers within seconds;
deeper downstream over tens of seconds. Inferred-edges adapters that
bridge silent services to alarm spans must therefore rank silent services
by their first-silent transition time and only treat the earliest cohort
(top-K, K=5 default) as root-cause candidates. Later silent services
remain observable in `timelines` and the propagator can traverse them via
their own state evidence; they just aren't used as inferred-edge
*sources*. This refines (does not contradict) the "silence is cascade,
not confounder" commitment above — the cascade is still observed
faithfully, but only the temporal head of the cascade enters the
candidate-source set, which is methodologically correct (later silent
services are cascade consequences, not causes) and operationally
necessary (without temporal ranking, treating all N silent services as
equal candidate sources fans out the propagator's path enumeration to
N × |alarm spans|, which on real partition cases blows up RSS in the
tens of GB).

---

## 7. State machine operational semantics

§3–§6 fix **what** is observed and **why** cascade is observation rather
than derivation. This section fixes **how** the inference engine
traverses observations to produce a causal path from `do(fault)` to a
user-perceptible SLO alarm — or to confirm a *silent injection* (no
such path exists). The state machine is constrained by five operational
rules; weakening any of them re-introduces the failure modes §6 was
meant to prevent.

The core commitment of the methodology, restated for this section:

> A single `do(fault, target T)` action enters the system. The state
> machine transitions are the **only** mechanism that derives further
> facts from that action. Independent observations from §3 detectors
> are the *inputs* to the machine; rules in `builtin_rules.json` are
> the *transitions*; everything else (cascade aggregators, inferred
> edges, threshold calibration) is plumbing that prepares inputs for
> these transitions.

### 7.1 Single-state-per-(entity, time) with precedence

For state-machine transitions to be well-defined, each (entity,
time-bucket) must carry **exactly one** canonical state. Multiple
adapters observing the same entity may emit different states from
their own sensor surfaces; these are reconciled by a precedence
ordering, the highest-priority signal wins, lower-priority signals
are recorded as `shadowed_evidence` for explainability but **do not
enter the active lattice**.

#### State definitions (sensor / discriminator / threshold direction)

Each state is uniquely defined by a four-tuple — **(PlaceKind, sensor
stream, discriminator shape, threshold direction)**. Two states whose
four-tuples differ on any axis cannot fire on the same observation,
which is what eliminates overlap **by construction**. The
*discriminator shape* is abstract (e.g. "duration ratio over
baseline"), not a specific formula; concrete extraction is per-system
(§9.2). *Thresholds* are not in this table — continuous-discriminator
states are calibrated at runtime per §12.2; binary and cascade-driven
states have no threshold.

| State | PlaceKind | Sensor stream | Discriminator shape | Threshold direction |
|---|---|---|---|---|
| UNKNOWN | any | none | no detector emitted | n/a |
| HEALTHY | any | any | every emitting detector below threshold | derived |
| SLOW | `span` | trace | `latency_ratio = duration / p_baseline(svc, endpoint)` | upper-tail |
| DEGRADED | `container` | k8s metrics | `utilization(metric)` ∈ {CPU, mem, thread, GC, …} | upper-tail |
| RESTARTING | `pod` | k8s | `restart_event(pod)` — count delta or phase ∈ {Pending, CrashLoopBackOff} | binary |
| ERRORING | `span` | trace | `is_failure(span)` — status / exception / log-error | binary (or upper-tail on aggregate error rate) |
| SILENT | `service` / `span` | trace | `rate(svc, window) = count / window_seconds` | lower-tail |
| MISSING | `span` / `service` | (no direct sensor) | cascade-inherited from `container.unavailable` along ownership edges; OR fallback when no baseline data exists | structural |
| UNAVAILABLE | `container` / `pod` | k8s | `not_ready(pod)` — `ready=false` or phase ∈ {Terminated, Failed} | binary |

Two state pairs commonly confused, separated by the four-tuple:

- **MISSING vs SILENT** — SILENT *requires* a working observation
  channel: a rate is measurable, even when rate = 0 in abnormal as
  long as baseline rate > 0. MISSING is the case where observation
  cannot proceed (cascaded from UNAVAILABLE, or no baseline data to
  calibrate against). When a partition simultaneously kills the pod
  and silences its traffic, MISSING (tier 5) wins over SILENT (tier 4)
  by §7.1's tier ordering — UNAVAILABLE-derived MISSING is the more
  precise causal attribution.
- **DEGRADED vs SLOW** — DEGRADED lives on `container` PlaceKind,
  observed at the **resource-input side** (k8s metrics: CPU/mem/thread).
  SLOW lives on `span`, observed at the **request-output side** (OTel:
  duration). Different PlaceKinds, so cannot fire on the same node;
  the cascade `container.degraded → span.slow` is a *cross-entity*
  transition (a propagation rule), not a within-entity state collision.

#### Intra-tier precedence

Tiers 5, 4, and 3 each contain two states. When both states in a tier
fire on the same (entity, time-bucket), the higher-precedence one
wins; the lower is demoted to `shadowed_evidence`. Precedence within
each tier follows a single unifying principle:

> **Direct observation of failure mode** > **inference of failure
> from absence or pressure**.

| Tier | Higher (direct observation) | Lower (inferred from absence/pressure) |
|---|---|---|
| 5 | **UNAVAILABLE** — k8s reports `ready=false` / phase Terminated | MISSING — observation absent; reason unspecified |
| 4 | **ERRORING** — span carries 5xx / exception / log-error | SILENT — rate dropped (could be partition, could be caller give-up) |
| 3 | **RESTARTING** — pod cycling (event-typed) | DEGRADED — capacity-layer pressure (steady-state) |

The asymmetry holds because higher-precedence states carry strictly
more information: they pin down *what* the failure mode is.
Lower-precedence states are consistent with multiple causes (SILENT
could be partition or caller give-up; MISSING could be dead pod or
sensor outage; DEGRADED is consistent with both pre-failure pressure
and steady-state load). When both fire, retain the more-specific one
and record the less-specific in shadowed evidence.

The Class C regression that motivated this section: a Class C
injection service whose callers retreat under exceptions accumulates
a real SILENT signal alongside its real ERRORING signal. Without
ERRORING > SILENT precedence, SILENT can overwrite ERRORING during
merge and the Class C verification chain
(`service.erroring → span.erroring → caller.erroring`) cannot fire
because its source state was lost.

Enforced in `synth.merge_evidence`: per (node, time-bucket), select
the highest-tier evidence; within tier, select the
higher-precedence state by the table above; demote the rest to
`shadowed_evidence`. Precedence is **lattice-internal**, not a free
dial — read directly from this table.

> **Class F caveat** — specialization labels (`frequent_gc`,
> `oom_killed`, …) per §10 and #186 / #188 are **not** canonical
> states. They live on a separate axis and **do not participate in
> this precedence ordering**; an entity can carry one canonical state
> from the table above plus zero or more specialization labels
> concurrently.

#### Alarm vs deviation — three sets used by §7.4 / §7.5 / §7.6

The terms below are precise and used uniformly throughout §7:

```
alarm_set       ≔ {n : n is a root-Server span ∧ state(n) ∉ {HEALTHY, UNKNOWN}}
deviating_set   ≔ {n : state(n) ∉ {HEALTHY, UNKNOWN}}
injection_set   ≔ the do(fault) target nodes for this case
```

`alarm_set ⊆ deviating_set`. The two are not interchangeable:

- An **alarm** is a *user-perceptible* failure: the user's request hit
  the system at some entry point and that entry point exhibited a
  deviation. In trace terms: the topmost Server span that is *not* in a
  loadgen service. A path search ends at an alarm.
- A **deviating node** is anything that emitted a non-HEALTHY
  canonical state. Mid-chain entities (cascading errors / silence /
  resource pressure) are deviating but **not** alarms — they are
  *propagation evidence*, used to explain how the fault reached the
  alarm, not as endpoints of the search.

The alarm definition is **fault-class agnostic**: every class produces
its own deviation flavor at the user-perceptible boundary, and §7.1's
state machine maps each flavor to the appropriate canonical state at
that boundary (Class C → ERRORING; Class D → SLOW; Class E → SILENT
or ERRORING-by-timeout; Class A → ERRORING). No per-class alarm
definition is needed.

**Root-Server-span predicate** (system-agnostic, structural):

```
is_root_server(span) ≡
    span.kind == Server
    ∧ (span.parent_span_id == ""             # no parent in trace
       ∨ owner_service(parent_span) ∈ LOADGEN_SERVICES)
    ∧ owner_service(span) ∉ LOADGEN_SERVICES
```

`LOADGEN_SERVICES` is a per-system constant (e.g. `{"loadgenerator",
"locust", "wrk2", "ts-loadgen"}` for the current dataset) declared in
the system registry alongside per-system adapter selection (§9.2).
Heuristic alternatives (e.g. "service has no in-edge in the system
graph") are inferior because loadgen services *are* deployed entities
and would otherwise pollute alarm_set with self-injected loadgen
errors.

**Silent-injection short-circuit**: if `alarm_set == ∅`, the case is a
*silent injection* by definition (no user-perceptible deviation
exists), and §7.6's path search is skipped. This is consistent with
`fault_seed.has_alarm == false` ground truth and lets the pipeline
return early without any DFS work.

#### 7.1.1 Structural truncation alarms — multi-signal detector

The §7.1 alarm definition (`is_root_server` + state ≠ HEALTHY) catches
the cases where the user-perceptible boundary deviates *visibly* in the
abnormal trace: the topmost non-loadgen Server span is slow, errors
out, or fails. Empirically a non-trivial slice of real injections
deviates *invisibly* at the boundary while the request *body* is
truncated: the boundary span returns 200 with normal latency, but the
fault has cut the call subtree short — downstream services that should
have been invoked never were, or were collapsed into an error stub. A
status-code check on the loadgen child cannot see this; an
endpoint-level latency/error detector cannot either.

We extend §7.1's alarm extraction with a **trace-shape detector** that
operates on the same per-endpoint granularity (`service::span_name`)
and writes its hits into `alarm_set` alongside the canonical detector
output. The detector is *purely structural* — it makes no claim about
which downstream service caused the truncation, only that the
boundary's request body is shaped wrong relative to baseline.

**Per-endpoint baseline profile** (computed once from baseline traces):

```
for each (service, span_name) seen as a root alarm candidate:
    canonical_shapes        ≔ top-K most frequent (sorted) tuples of
                              descendant (service, span_name) edges,
                              covering ≥ COVERAGE of baseline traces
    span_count_distribution ≔ baseline (median, p10, p90) of
                              descendant span count per trace
    ubiquitous_services     ≔ services that appear in ≥ UBIQUITY of
                              baseline traces for this endpoint
```

`canonical_shapes` is bounded (`K ≤ 20`, `COVERAGE = 0.95`) so the
profile stays small. `ubiquitous_services` is the set of services
whose absence is structurally meaningful (a service present in 95% of
baseline traces is not optional — it is part of the request's normal
body).

**Per-trace flagging rule** (during abnormal period). For each trace
hitting endpoint *e*:

```
S1 ≔ |services(trace) ∩ ubiquitous_services(e)|
     < |ubiquitous_services(e)|         (a normally-ubiquitous service is missing)
S2 ≔ span_count(trace) < p10_baseline(e) × 0.5
                                        (request body is unusually small)
S3 ≔ jaccard(edges(trace), nearest canonical_shape(e)) < J_THRESHOLD
                                        (edge structure diverged)
S4 ≔ trace_terminates_at_error(trace) ∧ S3
                                        (truncated AND errored, evidence
                                         the truncation is fault-driven
                                         rather than a benign cache hit)

trace_truncated(trace, e) ≡ Σ Sᵢ ≥ SCORE_GATE
```

Multi-signal — no single S is sufficient. A trace can be small (S2)
because of a benign cache; it can be missing a service (S1) because
the request was a different operation; it can have a different shape
(S3) because of A/B traffic. Two of the four signals firing together
indicates the request *body* (not just its outcome) deviates from
how the endpoint normally serves traffic. (`SCORE_GATE = 3`,
`J_THRESHOLD = 0.5`, `UBIQUITY = 0.95` in the current calibration.)

**Per-endpoint promotion rule**. An endpoint is added to `alarm_set`
only if a *fraction* of its abnormal traces were truncated:

```
truncation_rate(e) ≔ |{t ∈ abnormal_traces(e) : trace_truncated(t, e)}|
                     / |abnormal_traces(e)|

e ∈ alarm_set  ⟸  truncation_rate(e) ≥ R_THRESHOLD
              ∧   |truncated traces| ≥ MIN_FAILED_TRACES
```

(`R_THRESHOLD = 0.05`, `MIN_FAILED_TRACES = 3`.) The rate gate keeps
out endpoints with one-off odd traces; the absolute floor keeps out
low-traffic endpoints where two unlucky traces would crest the rate.
An endpoint with no baseline coverage (`< MIN_BASELINE_TRACES = 5`)
is skipped — we cannot distinguish "different from usual" from
"never seen before".

**Sidecar output for downstream ranking**. The detector also emits,
per flagged endpoint, the set of `missing_services` — the
`ubiquitous_services` that vanished. Downstream root-cause ranking
(§7.6) prefers candidates whose place is in or causally upstream of
this set; the truncation alarm thus carries its own *witness* of
which sub-tree was cut, narrowing the search even though the alarm
itself is endpoint-local.

**Composition with the §7.1 alarm definition**:

```
alarm_set ≔ {n : is_root_alarm_candidate(n) ∧ state(n) ≠ HEALTHY}
          ∪ {e : truncation_rate(e) ≥ R_THRESHOLD
                 ∧ |truncated traces(e)| ≥ MIN_FAILED_TRACES}
```

The two branches are complementary: state-machine alarms catch
*outcome* deviations (latency/error) at the boundary; truncation
alarms catch *body* deviations when the boundary still returns 200.
Cases are no longer mis-classified as silent-injection just because
their boundary outcome looks normal — the truncation pass converts
five v4-baseline `no_alarms`/`no_paths` failures (silent-injection
mis-routes) into solved cases on the TrainTicket fixture (498/500 ⇒
99.6% success, +5 over the v4 baseline of 493/500).

**Why kind-agnostic**. The §7.1 root-alarm predicate started as
`is_root_server` (Server-kind + non-loadgen). Real instrumentation
mis-labels span kinds inconsistently (the same Spring filter chain
is Server in one service and Internal in another), so the predicate
was relaxed to `is_root_alarm_candidate`: *non-loadgen owner whose
parent is loadgen-or-missing*, with no kind constraint. The
truncation detector inherits this predicate — it scans the same set
of endpoints the canonical alarm extractor sees. This keeps the two
branches aligned and prevents the truncation pass from silently
expanding scope into mid-tier spans.

### 7.2 Rule firing scoped by fault class

§3 partitions faults by primary class. Rules in `builtin_rules.json`
describe how a state propagates *assuming the system is in the
corresponding class regime*. A rule introduced for Class E (e.g.
`service_silent_to_span`) holds only when the underlying event is a
Class E partition; letting it fire on Class C cases — where
SILENT-looking signals are secondary cascades of caller-give-up rather
than primary partition events — generates spurious paths that compete
during DFS and degrade ranking.

Two mechanisms, in increasing leniency:

1. **Soft gate via §7.1 precedence (preferred)**: if state precedence
   is correctly enforced, the Class C injected service is never labeled
   SILENT in the first place, so silent rules find no source state to
   fire on. No fault-class metadata is needed at inference; degrades
   gracefully on unknown faults.
2. **Hard gate via metadata**: add
   `PropagationRule.applicable_fault_classes: set[FaultClass]`, filter
   by the case's `injection.json` at rule-load time. Reserved for the
   rare cases where two classes legitimately co-fire on the same
   entity (Class A + E on infra death — the dead pod simultaneously
   triggers UNAVAILABLE and SILENT, both of which are real).

Default to the soft gate. Add the hard gate only when a documented
co-fire case demonstrates the soft gate is insufficient.

### 7.3 Inferred edges are anchored at the injection point

§6 commits to "cascade emerges from observation, not derivation".
Inferred edges (`enrich_with_inferred_edges`) are a special case: when
physical request-flow observation is structurally absent, an inferred
edge patches over the gap so the propagator can still reach an alarm.

> An inferred edge is a *patch on missing observation*. It is not a new
> transition rule, and it is not part of the canonical state-machine
> transition set.

The earlier "co-anomaly bridge" formulation generated inferred edges
between **any** silent service and **any** alarm span. This produces
O(silent_services × alarm_spans) edges — typically tens of thousands —
each of which competes with rule-based transitions during DFS,
inflates path counts, and worst, lets correlated-but-not-causal
anomalies form spurious paths. We replace it with a narrow,
**injection-anchored** formulation: inferred edges are generated only
in two scenarios, and **the source of every inferred edge is the
injection point** (or its owning container/pod). Targets are derived
from concrete physical relationships, not from anomaly correlation.

#### Scenario A — Class A: dead pod has no spans

```
Trigger:    injection_point.container or pod is in state UNAVAILABLE
            (Class A — PodKill, PodFailure, ContainerKill, OOMKill).
Source:     the dead container/pod node.
Targets:    services that physically depend on this pod, derived from
            k8s ownership: services with `routes_to` (Service→Pod) or
            `runs` (Pod→Container) edges to/from the dead pod.
Edge kind:  inferred:depends_on_dead_infra
```

The dead pod emits no spans, so OTel-derived `calls` edges from
callers to it are absent. We can't observe "caller called dead pod and
got an error" from traces, but we can structurally infer that any
service mapped to this pod via k8s manifests will see Class C–like
ERRORING behavior — the inferred edge connects the structural fact
(dead pod) to the consuming services so the propagator can reach
alarms via them.

#### Scenario E — Class E: gating-failure silences downstream operations

```
Trigger:    injection_point.service is in state ERRORING (or SILENT)
            AND it is a *gating* service — its successful response is
            a precondition for downstream operations to ever fire
            (auth, gateway, verification, DNS, …).
Source:     the gating service node.
Targets:    services that
              · went SILENT or MISSING during the abnormal window, AND
              · have no other ERRORING ancestor reachable in the graph
                (i.e. their silence is unexplained by a closer cause),
            AND are downstream of the gating service in usual flows
            (calculated from baseline traces — services historically
            invoked along chains that pass through the gating service).
Edge kind:  inferred:gated_silenced
```

When auth/verification fails, downstream operations are never
*invoked* — there's no caller-side timeout to observe, the calls
simply don't happen. The OTel graph for this case has no edges at all
between the gating service and the silenced downstream services. The
inferred edge patches this: it connects the gating service's failure
to the silenced services it would have triggered, so a propagator
walking from the injection point reaches the alarm.

#### Why this is conservative enough

- **Source is always the injection point** (or its owning infra
  node). Inferred edges *cannot* originate from arbitrary anomalies —
  no co-anomaly bridges between unrelated services.
- **Edge cardinality is O(consumers of injection)**, typically a small
  constant per case. The DFS subgraph stays sparse.
- **Targets are derived from physical/structural relationships**
  (k8s ownership for Scenario A; baseline-trace flow lineage for
  Scenario E). This keeps inferred edges grounded in real observed
  structure.
- **Both scenarios still respect the three gates from earlier**
  (topology — physical graph cannot reach target; class — Class A or E
  trigger condition; temporal — onset ordering per §7.5). The gates
  are necessary but not sufficient; the injection-anchored source is
  the additional structural commitment that prevents scope leak.

Without the injection anchor, inferred edges become a parallel
propagation surface that competes with rule-based transitions and
inflates DFS path counts without improving recall — exactly the
failure mode observed when the L6d silent inferred edges fired
unconditionally on Class C cases.

### 7.4 Topology pre-pruning: source–target intersection

The DFS search space is the set of nodes potentially on an
injection→alarm path. The naïve approximation —
`Reach_forward(injection)` alone — over-includes services reachable
from injection but unable to reach any alarm, which during DFS produces
paths that wander into unreachable territory before backtracking. The
correct subgraph is the **bidirectional intersection**:

Using the three sets defined at the end of §7.1 (`alarm_set`,
`deviating_set`, `injection_set`):

```
corridor       ≔ Reach_forward(injection_set, max_hops_fwd)
              ∩ Reach_backward(alarm_set, max_hops_bwd)
relevant_nodes ≔ corridor ∩ (deviating_set ∪ injection_set)
```

Three filters compose:

1. **Forward reach** — only nodes the injection could causally affect.
2. **Backward reach** — only nodes that can reach a *user-perceptible*
   alarm (root-Server-span deviation), not arbitrary deviating nodes.
   This is the key tightening introduced by the alarm/deviation split:
   mid-chain deviating services (loadgen-side ERRORING, mid-tier
   silence) are **not** backward-reach targets; they only get to
   participate in the path *as middlemen* if they sit on a corridor
   ending at a true alarm.
3. **Activity filter** — within the corridor, drop nodes that are
   themselves HEALTHY. A HEALTHY node cannot be a load-bearing link in
   the propagation chain. Keep `injection_set` regardless because the
   injection target may be HEALTHY at the §7.1 level for silent
   injections that are about to be short-circuited.

Properties:

- A node not in `relevant_nodes` cannot lie on any injection→alarm
  path that participates in propagation; pruning is exact.
- Typical subgraph shrink 5–20× on real microservice topologies from
  the bidirectional intersection alone, plus a further 2–5× from the
  activity filter (most services in a system are HEALTHY during any
  given fault).
- `max_hops_fwd + max_hops_bwd ≥ graph_diameter` must hold, otherwise
  the intersection misses legitimately reachable nodes whose path
  passes through the "middle" of the graph.

The reduced subgraph is what BFS-then-DFS (`extract_paths`) operates
on. Path-cap heuristics (`max_paths`, svc-dedup) become rare-trigger
safety nets, not load-bearing components — the geometry + state mask
are doing the work, not the cap.

#### Graph-completeness invariants assumed by this step

The activity filter (`corridor ∩ deviating_set`) is exact only because
the underlying graph is constructed to contain *every* node and edge
that could host a propagation chain. Two invariants must hold at graph
build time:

1. **Node completeness** — service / pod / container nodes are
   extracted from the **union** of (a) k8s metrics, (b) baseline-window
   traces, (c) abnormal-window traces. A service that was idle during
   the abnormal window but is k8s-deployed (and therefore visible via
   metrics) is still in the graph as a node.
2. **Edge completeness** — `calls` / `includes` edges are extracted
   from the **union** of baseline-window and abnormal-window
   trace-derived edges. A `calls` edge that fired in baseline but not
   abnormal (because the upstream went silent) is still in the graph.

Together these guarantee that "service idle during abnormal window"
does not erase the service or its dependency edges from the search
space. The node will appear in the corridor; whether it survives the
activity filter then depends on whether *any* §7.1 detector emitted a
non-HEALTHY state for it during the abnormal window.

The activity filter dropping HEALTHY corridor nodes is also exact:
propagation rules never have `HEALTHY` as a `src_state` (a HEALTHY
node carries no state to propagate), so a HEALTHY node cannot be a
load-bearing link in any admissible chain. The injection point is the
single exception (silent injection's target may be HEALTHY-by-§7.1
because no §7.1 detector fired) and is rescued by the
`∪ injection_set` clause.

`build_graph_from_parquet` in `loaders/parquet_loader.py` realizes
these invariants — `_extract_k8s_resources_from_all_sources` unions
the three node sources, and `_build_edges_from_traces` unions baseline
and abnormal edges. New benchmark adapters must preserve this
contract; a per-system loader that only consumes abnormal-window data
would silently regress recall on idle-relay services.

### 7.5 Temporal causality: monotonic transitions

Causality has a temporal direction: if A.s₁ → B.s₂ is causal in this
case, the onset of A.s₁ must precede the onset of B.s₂. The state
machine currently checks rule legality and edge existence but **not**
temporal ordering — a transition is admitted whether B's anomaly began
before, after, or independently of A's.

Per (edge), the temporal admission:

```
onset(A.s₁) ≤ onset(B.s₂) + ε_eff(s₁, s₂, edge_kind)
onset(A.s₁), onset(B.s₂) ∈ [t_inject − δ_pre, t_alarm + δ_post]

ε_eff(s₁, s₂, edge_kind)
    = ε(edge_kind)                  # propagation-delay budget for the channel
    + onset_resolution(s₁)          # measurement noise on src onset
    + onset_resolution(s₂)          # measurement noise on dst onset
```

- **ε(edge_kind)** — propagation-delay budget appropriate to the
  channel: synchronous (`calls`, `includes`) ≤ 5s; infrastructure
  (`runs`, `schedules`) ≤ 60s; network routing (`routes_to`) ≤ 10s.
  Events beyond ε *plus* onset measurement noise are unrelated
  coincidences.
- **onset_resolution(state)** — per-state measurement precision (§12.4
  policy table). SILENT's onset is determined by the first below-threshold
  30 s subwindow, so its onset precision is 30 s; ERRORING / SLOW are
  measured at the `TraceStateAdapter` window granularity (3 s);
  UNAVAILABLE / RESTARTING come from k8s events and are sub-second.
  Without this term, a SILENT-SILENT chain across a `calls` edge faces
  ε = 5 s while either onset has ±15 s aliasing noise from subwindow
  alignment — perfectly causal chains get rejected by the temporal gate
  due to bucket boundaries, not because they violate causality.
- **δ_pre / δ_post** — pre-injection and post-alarm tolerance for
  clock skew and metric aggregation lag. δ_pre ≈ 30s (state may have
  onset slightly before t_inject due to reporting latency); δ_post ≈
  subwindow_seconds (state may have onset slightly after t_alarm if
  catching the trailing edge).
- States with onset outside `[t_inject − δ_pre, t_alarm + δ_post]` are
  pruned from the inference subgraph entirely — they are pre-existing
  or post-resolution conditions, not effects of `do(fault)`.

#### Onset for rule firing — earliest matching state in the trajectory

An entity's state is not constant over the abnormal window; it can
follow a trajectory like `HEALTHY → ERRORING → SILENT` (e.g. a Class C
injection target throws exceptions for the first minute, then its
callers retreat and rate drops). The trajectory is the time-ordered
sequence of `(state, onset)` transitions emitted by §7.1's merge step
across consecutive time-buckets.

When a propagation rule R with `src_states = S` fires on entity E,
the onset used by §7.5 is **the earliest transition in E's trajectory
into a state ∈ S**:

```
onset_for_rule(E, R) = min { onset_i : trajectory(E)[i].state ∈ R.src_states }
```

Why earliest, not most-recent: in `HEALTHY → ERRORING → SILENT`, the
ERRORING was the *primary* failure mode caused by `do(fault)`; the
later SILENT is a consequence of callers giving up on the failing
service. Causally, the ERRORING is the cause that downstream effects
should be timed against. Using the most-recent state (SILENT here)
would attribute the cascade to the wrong onset and let the temporal
gate reject otherwise-valid chains because the source onset has
slipped 30+ seconds past the downstream onset.

This rule has a useful side-effect: the silent rules for Class E
(introduced in §3.E) still match a Class C entity that drifted into
SILENT, but they fire with the SILENT onset (later in the
trajectory). The downstream alarm's onset is typically anchored on
the earlier ERRORING (the user-perceptible failure happened then,
not when callers eventually went silent), so the silent rule's chain
fails the temporal check — resolved by §7.5 without needing §7.2's
hard gate.

#### Why per-edge tolerance is set generously

`ε_eff` is deliberately generous (e.g. SILENT→SILENT on `calls` is
5 + 30 + 30 = 65 s). The methodological justification: noise survival
**compounds multiplicatively** along a path.

Let `q < 1` be the per-edge probability of a noise transition randomly
satisfying ε_eff. A noise path of length N has joint survival
probability `q^N` — exponential decay. Real causal paths pass every
edge regardless of how loose ε_eff is set, because real causality
respects time order; only noise paths see the q factor.

So per-edge strictness trades off:

- **Strict ε_eff** (low ε): some real paths rejected by measurement
  aliasing (recall drops); noise paths also rejected.
- **Generous ε_eff** (high ε): real paths recovered (recall protected);
  per-edge noise admission q rises, but path-length itself filters
  composite noise paths exponentially.

Since recall on real causal chains is the load-bearing metric and
alarm-set narrowing (§7.4) plus path-length-based scoring (§12.3)
already filter long noise paths, **generous ε_eff dominates**. We
choose to admit some per-edge noise rather than miss real chains.

Temporal pruning is a strong natural filter for failures §7.1–§7.3
might miss:

- A Class C service mis-labeled SILENT *despite* §7.1 only enters the
  path if its SILENT onset is post-injection; baseline-pre-existing
  silence is dropped.
- A spurious cross-class rule firing per §7.2's leak is caught when the
  secondary state's onset is far from the upstream onset (the temporal
  budget for `calls` is 5s; correlated-but-not-causal anomalies often
  miss this).
- An inferred edge whose two endpoints have incompatible onset times
  is dropped — only edges connecting causally-ordered events survive.

Each Evidence/Transition already carries `at`. Implementation is a
single linear pass after §7.4's intersection: drop edges where
`onset(src) > onset(dst) + ε(edge_kind)` or where either endpoint
falls outside the global window.

### 7.6 The inference pipeline

The five rules above compose into a fixed pipeline. Each step depends
only on prior steps' outputs:

```
Step 1 — Per-detector evidence emission (§3, §12.2)
         each adapter emits Evidence(node, state, at, ...) independently.
Step 2 — State precedence reconciliation (§7.1)
         per (node, time_bucket): pick highest-tier state, shadow others.
Step 3 — Compute alarm_set / deviating_set / injection_set (§7.1 end)
         alarm_set     = root-Server-spans with state ≠ HEALTHY
         deviating_set = all nodes with state ≠ HEALTHY
         If alarm_set == ∅ → return SILENT_INJECTION (skip rest).
Step 4 — Rule applicability filter (§7.2)
         keep rules whose src_states match reconciled state.
         Soft gate via §7.1; hard gate via fault_class only when needed.
Step 5 — Inferred edge generation (§7.3)
         injection-anchored only — Class A "depends_on_dead_infra" + Class E
         "gated_silenced". O(consumers of injection) edges, not O(silent × alarm).
Step 6 — Topology + activity pruning (§7.4)
         corridor       = Reach_forward(injection) ∩ Reach_backward(alarm_set)
         relevant_nodes = corridor ∩ (deviating_set ∪ injection_set)
Step 7 — Temporal pruning (§7.5)
         drop edges and nodes whose onsets violate causal ordering or
         fall outside [t_inject − δ_pre, t_alarm + δ_post].
Step 8 — Path search
         BFS/DFS on the triply-pruned subgraph; max_paths cap is a
         safety net only.
Step 9 — Path verification + output
         each transition matches a candidate rule (Step 4) and uses the
         rule's structurally-valid onset (§7.5 trajectory rule);
         output = the SET of all admissible paths (no ranking — see below).
         path(s) found → causal chains (positive case).
         no path       → silent injection candidate
                         (verify against fault_seed.has_alarm == false).
```

**Output is a set, not a ranked list.** Path-level confidence scoring
is not part of the methodology: composing rule confidences
(§12.3 `P_struct × P_causal`) into a chain score is unreliable
(unfounded independence assumptions, no calibration data) and would
displace the natural noise filter §7.5 already provides
(`q^N` compounding). When downstream consumers need a ranking — top-K
service evaluation, e.g. — derive it from structural signals over the
admissible-path set:

- frequency a service appears across admissible paths,
- earliest deviation onset along a service's trajectory,
- graph distance from a service to the injection point.

These are observable from the output without committing to a chain
score. The pipeline itself returns the set untouched.

The architectural commitment: Steps 2–7 are **constraint propagation**,
executed once per case in linear time. Step 8 searches inside an
already-tight space. Earlier iterations collapsed Steps 2–7 into Step
1's output and put all constraint work inside Step 8's DFS — which is
why path enumeration explodes on dense subgraphs after §3.E's silent
edges land: the DFS was being asked to discover constraints that
should have been pre-computed. Step 3's silent-injection short-circuit
also turns the trivially-negative cases (`alarm_set == ∅`) into O(graph
state) rather than letting them reach the DFS at all.

This pipeline is the operational definition of the state machine. Any
optimization or new feature must specify which step it modifies; a
proposal that does not fit a step is a proposal to extend the pipeline
itself, which requires updating this section first.

---

## 8. Procedure: adding a new fault type

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

## 9. Procedure: adding a new benchmark / stack

### 9.1 Steps

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

### 9.2 Adapter contract per state

Each canonical state's discriminator (§7.1) has a corresponding
**Protocol** that per-system adapters implement. The adapter family
(Layer 2) consumes the protocol; the per-system implementation
(Layer 3) provides it. Threshold calibration runs separately (§12.2)
and is not the per-system layer's concern.

| State | Protocol method | Returns | Default fallback |
|---|---|---|---|
| ERRORING | `FailureDetector.is_failure(span) -> bool` | True iff span represents a failed call | OR-of: `HTTPFailureDetector` (`status_code ≥ 500`), `GRPCFailureDetector` (`grpc.status_code != 0`), `ExceptionEventFailureDetector` (`events[*].name == "exception"`) |
| SLOW | `LatencyExtractor.duration_seconds(span) -> float` | Span end-to-start duration | OTel default: `(end_time_ns - start_time_ns) / 1e9` |
| SILENT | `RateExtractor.service_label(span) -> str` | Owning-service tag for rate aggregation | OTel default: `service_name` column |
| DEGRADED | `UtilizationProvider.discriminators() -> dict[str, MetricSeries]` | Per-pod time series of named utilization signals (`cpu_util`, `mem_util`, …) | k8s/OTel default: standard metric names listed in `K8sMetricsAdapter` |
| UNAVAILABLE | `LivenessProbe.is_unavailable(pod) -> bool` | True iff pod is observed not-ready | k8s default: `phase ∈ {Terminated, Failed}` ∨ `not container.ready` |
| RESTARTING | `RestartProbe.is_restarting(pod) -> bool` | True iff pod is in a restart cycle | k8s default: `restart_count_delta > 0` ∨ `phase ∈ {Pending, CrashLoopBackOff}` |
| MISSING / HEALTHY / UNKNOWN | n/a | (derived; no per-system protocol) | n/a |

**Registration**:

```python
@register_failure_detector("trainticket")
class TrainTicketFailureDetector(FailureDetector):
    def is_failure(self, span: SpanRow) -> bool:
        # TT-specific: any 5xx HTTP, JVM exception event, or app-level
        # error attribute set by Spring exception handlers.
        return (
            HTTPFailureDetector().is_failure(span)
            or ExceptionEventFailureDetector().is_failure(span)
            or span.attrs.get("app.error.code") is not None
        )
```

Selection: at IR pipeline startup, `get_active_<protocol>(case)` reads
the case's system tag (e.g. `injection.json::system_code`) and selects
the registered subclass; an unrecognized system falls back to the
default OR-of-detectors.

Per-system adapters **must not**:

- Bake threshold values into their logic — use §12.2 calibration.
- Introduce new canonical states — use §10 procedure.
- Override precedence merging — lives in `synth.merge_evidence`.

Per-system adapters **may**:

- Add stack-specific patterns (log message regexes, attribute names,
  metric prefixes).
- Combine multiple protocol implementations via OR-of-detectors.
- Opt out of detection on specific entities by returning a sentinel
  (e.g. a JVM-only detector returning `None` on non-JVM spans).

The current code has these per-system seams partially mixed into the
adapter families: `TraceStateAdapter` hard-codes
`attr.http.response.status_code >= 500` (HTTP-only `FailureDetector`)
and `error_rate_floor = 0.1` (a §12.2 threshold short-circuited by a
constant). Migrating these out of the adapter family and into
registered per-system detectors is the first concrete refactor that
materializes §9.2.

---

## 10. Procedure: adding a new canonical state

A new state is added when, and only when, §8 step 4 is reached — a
genuinely new observation class is being added.

Process:

1. Add the state name to `ir/states.py` (`PerKindState` enums for the
   `PlaceKind`s where it can appear). Severity rank is assigned by the
   tier admission table in §12.1 — this is procedural, not a free choice.
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
- per-stack idioms (use per-system adapter subclasses per §9).

---

## 11. Current IR audit (2026-04-25)

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
audit, or (b) introduce a new class via §8.

---

## 12. Decision methodology

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

### 12.1 Severity tier methodology

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

### 12.2 Adapter threshold methodology

**One-class baseline calibration.** Each adapter detects deviations from
healthy operation by treating the case's own baseline window as the
ground-truth healthy distribution.

Inputs (per service, per case):

- A discriminator `Q` with an exact formula on observed data.
- The case's baseline window of healthy traffic.
- A policy parameter `α` (global; see §12.4).

Procedure:

1. Slice **both** the baseline window and the abnormal window into
   sliding sub-windows of fixed length `subwindow_seconds` (default 30s,
   stride = `_BUCKET_SECONDS`). The subwindow length is **decoupled from
   the abnormal-window length**: real datasets often have baseline ≈
   abnormal length (chaos-mesh's standard pre-duration = duration
   layout), and tying subwindow length to abnormal length would yield
   only one baseline subwindow → calibrator opt-out → no detection.
   Fixed 30s gives ~baseline_length/5 samples (≈ 40+ on a 4-min baseline,
   stable enough for q_0.01 estimation; see §12.4 for the choice of 30s).
   The slice range is **per-service**: anchored to that service's own
   active baseline range `[svc_ts_min, svc_ts_max]` clamped within the
   global baseline window. A service that was deployed mid-baseline (or
   only fires sporadically) is calibrated against its active range, not
   the global window — otherwise zero-count subwindows from "before the
   service existed" would dominate the distribution and pin the threshold
   at zero.
2. Compute Q on each baseline sub-window. **Q is a per-second rate**
   (`count_in_subwindow / subwindow_seconds`), not a count or count-ratio,
   so the distribution stays comparable across services with different
   baseline rates. The set of values is the service's empirical healthy
   distribution of Q.
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
5. At inference: slice the abnormal window into the same fixed-length
   subwindows, compute Q (per-second rate) per subwindow, take
   `Q_abnormal = mean(abnormal_subwindow_rates)`. Emit the adapter's
   state iff `Q_abnormal` crosses `T(svc, case)` in the tail direction.
   The mean preserves the per-(svc, case) FP semantics: under the null
   hypothesis (service is healthy) `Q_abnormal` is one sample drawn from
   the same Q distribution; with the threshold at the α-quantile the
   per-(svc, case) FP rate stays bounded by α.
   When aggregate emission fires, the transition `at` is taken from the
   first abnormal sub-window whose rate falls below `T(svc, case)` —
   *whether* uses the aggregate test (FP bound holds), *when* uses the
   first-crossing sub-window so downstream consumers (e.g. inferred-edges
   silent gate) can rank silent services by causal order.

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

### 12.3 Rule-confidence methodology

> **Scope** — `confidence(firing of R, on case C)` is per-rule per-case.
> It is *not* composed into a chain-level score; per §7.6 the pipeline
> output is the **set** of admissible paths, ranked (if needed) by
> structural signals over the set rather than by composite confidence.
> The values defined here are used as admission gates for individual
> rule firings (e.g. dropping rules with `P_causal < 0.1` from
> consideration), not as path weights.

A rule's confidence is decomposed:

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

### 12.4 Policy parameters

The only free dials in the system; everything else flows from data.

| Parameter | Value | Why this value |
|---|---|---|
| α — quantile FP budget per (svc, case) | `0.01` | A typical case has ~10 services. At α=0.01 case-level FP rate ≈ `1 − 0.99^10 ≈ 9.5%`. At α=0.05 case-level FP rate ≈ 40% — comparable to or larger than typical TP rates, dominating ranking. At α=0.001 case-level FP ≈ 1% but requires N ≳ 1000 baseline sub-windows for a stable `q_0.001` estimate, exceeding our typical ~120-sub-window baselines. |
| Beta prior for `P_causal` | `Beta(2, 2)` | Pseudo-count 4 → ~10 observations to move posterior mean by 0.1. `Beta(1, 1)` (uniform) over-trusts the first 1–2 fault cases; `Beta(10, 10)` hard-pins at 0.5 for too long. |
| Bootstrap stability bound | `std(quantile) / IQR(data) ≤ 0.10` | Bootstrap quantile-estimate std stays within 10% of the data's natural scale. IQR (not mean) is the denominator so the metric works for quantiles near zero — see §12.2 step 3. Below this services opt out: the quantile is not stable for the available baseline N. |
| Sub-window stride | `_BUCKET_SECONDS` (5 s) | Same temporal grid as the rest of the IR. Smaller stride is heavier compute with no recall gain at our cadences. |
| Sub-window length | `30s` fixed | Decoupled from abnormal-window length. Real datasets (chaos-mesh, RCABench) commonly use `pre_duration ≈ duration ≈ 4 min`; tying subwindow length to abnormal length collapses the baseline distribution to a single sample. 30s is short enough to give ~40+ samples on a typical 4-min baseline (≥ N for stable `q_0.01` per the bootstrap-stability check) and long enough that single-event noise (Poisson std ≈ √(λ·30s)) stays small relative to per-second rate signal at λ ≥ 0.2/s. The discriminator is per-second rate, so subwindow length doesn't affect comparability — only sample count and noise floor. |
| Calibration scope | per-case | Calibration runs once per reasoning invocation, sharing baseline already loaded. Per-corpus calibration loses case-specific load patterns and ties cases to a calibration cadence. |
| ε(`calls`, `includes`) — synchronous propagation | `5 s` | Synchronous request paths return on the millisecond–second scale; `5 s` covers TCP retransmits, GC pauses, and small queue buildup but rejects coincident events tens of seconds apart. |
| ε(`runs`, `schedules`) — infrastructure | `60 s` | k8s reconcile loops, kubelet probe cadence, container restart back-off all fall within ~minute. Below this misses real Class A → Class C cascades; above this admits unrelated failures separated by minutes. |
| ε(`routes_to`) — network routing | `10 s` | DNS TTL / iptables / service-mesh route refreshes. |
| onset_resolution — per-state measurement precision | ERRORING / SLOW = `3 s`; SILENT = `30 s`; DEGRADED = `5 s`; UNAVAILABLE / RESTARTING = `1 s`; MISSING = inherit src state | Each value is the temporal grid the corresponding adapter measures on (e.g. SILENT lives on §12.4's 30 s sub-window). Used by §7.5: `ε_eff = ε(edge) + onset_resolution(src) + onset_resolution(dst)`. Generous per-edge tolerance is intentional — noise paths' joint survival decays as `q^N` along chain length, so per-edge tolerance can favor recall without inflating accepted noise. |
| δ_pre / δ_post — temporal gate window padding | δ_pre = `30 s`; δ_post = `subwindow_seconds (30 s)` | δ_pre absorbs reporting latency that backdates onset; δ_post catches trailing-edge silences whose first below-threshold sub-window starts after t_alarm. |

Changing any of these is a documented policy decision (separate PR; the
PR must show measured FP / recall on a labeled subset before/after).

### 12.5 Procedure templates

When adding a new state, threshold, or rule, the PR description must
include the matching template, fully filled in:

**New state:**

1. Physical description (one sentence).
2. Tier admission match (which row of §12.1).
3. Severity = tier number.
4. If no row matches, attach a separate PR extending §12.1 first.

**New adapter threshold:**

1. Discriminator Q (formula).
2. Calibration code path (must implement §12.2).
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

## 13. Implementation strategy: two-phase pipeline

§7.6 specifies the inference pipeline as a 9-step **logical order** —
each step's correctness rests on the prior steps, the order proves
the methodology's soundness. The implementation does not have to
execute the steps in that order; it must only produce the same
output as if it had.

In practice, evaluating §7.1 detectors on the entire system before
pruning to the corridor is wasteful: detector evaluation
(per-(svc, case) baseline calibration; per-state observation passes)
is the single most expensive operation in the pipeline, and ~95% of
nodes are pruned away in §7.4 anyway. A two-phase reordering moves
detector evaluation **behind** topology pruning so it runs on the
corridor only.

### 13.1 Phase 1 — Corridor build (cheap, structural)

Phase 1 reduces the search space using only structural and
lightweight signals.

```
1.1  injection_set ← injection.json   (supports multi-injection)

1.2  cheap_alarm_set ← root-Server-spans where
     baseline-vs-abnormal diverges along ANY of:
       · error rate up
       · latency  up
       · volume   down
     evaluated with **loose thresholds** (see §13.3 invariant A).

1.3  Injection-anchored inferred edges (§7.3):
       Scenario A — injection's pod → its routes_to / runs consumers
                    (from k8s ownership; no traces needed)
       Scenario E — gating-service injection → downstream lineage
                    (from baseline traces' usual flows)

1.4  corridor = Reach_forward(injection_set,   physical ∪ inferred)
              ∩ Reach_backward(cheap_alarm_set, physical ∪ inferred)

1.5  if cheap_alarm_set == ∅ or corridor == ∅
       → return SILENT_INJECTION  (skip Phase 2 entirely)
```

Phase 1 cost is `O(|root spans| + |graph|)` — root-span comparison
plus two BFS passes. Detector machinery and baseline calibration
(§12.2) are not invoked here.

### 13.2 Phase 2 — State machine (expensive, on corridor only)

```
2.1  Run §7.1 detectors only on `corridor` nodes.
     Per-(svc, case) baseline calibration (§12.2) restricts to
     services that survived Phase 1.

2.2  Per-(node, time-bucket) precedence merge → reconciled state.

2.3  Compute state trajectory per corridor node.

2.4  deviating_set ← {n ∈ corridor : ∃ bucket where state ≠ HEALTHY}.

2.5  alarm_set ← cheap_alarm_set ∩ deviating_set
                 filtered by the precise §7.1 four-tuple
                 (tightens cheap_alarm_set down to true alarms;
                  see §13.3 invariant B).

2.6  relevant_nodes ← corridor ∩ (deviating_set ∪ injection_set).

2.7  Drop edges that violate §7.5 ε_eff temporal causality;
     drop nodes whose onset is outside
     [t_inject − δ_pre, t_alarm + δ_post].

2.8  DFS from injection_set to alarm_set in relevant_nodes;
     each transition must match a §7.2 rule with the onset
     given by §7.5's trajectory rule.

2.9  Output: SET of admissible paths.
```

Phase 2 cost scales with `|corridor|`, typically 5–20% of the total
graph. Detector baseline calibration runs on the same fraction.
Aggregate runtime falls 80–90% relative to a single-pass full-graph
implementation, almost entirely from the Phase-2 input shrinking.

### 13.3 Two correctness invariants

The reordering is sound iff two invariants hold.

**Invariant A — `cheap_alarm_set ⊇ true_alarm_set` (no missed alarms).**

`true_alarm_set` is what §7.1 + §7.4 would compute given full
state inference; `cheap_alarm_set` is the Phase-1 over-approximation.
If Phase 1 misses any true alarm, that alarm is absent from
`Reach_backward`, the corridor does not include the chain leading
to it, and Phase 2 cannot recover. Therefore Phase 1 thresholds must
admit any baseline-divergence that *could* be a real alarm, even at
the cost of false positives — Phase 2's precise §7.1 evaluation
filters the false positives at step 2.5 with no recall loss.

Concrete loose thresholds (subject to revision with measurement):

- error rate up: abnormal > 1% AND abnormal ≥ 2× baseline
- latency up: abnormal-p95 > 1.5× baseline-p95
- volume down: abnormal-rate < 0.5× baseline-rate

These are inclusive enough that any §12.2-quantile-flagged deviation
also passes. Verifying `P(cheap admits | §12.2 flags) = 1` on the
labeled corpus is part of integration testing.

**Invariant B — corridor includes every causally-relevant node.**

A node is causally relevant if it lies on at least one admissible
chain from injection to a true alarm. The corridor is built from
`cheap_alarm_set ⊇ true_alarm_set` plus injection-anchored inferred
edges (computed structurally without state). Both expansions are
over-approximations of what the full state machine would generate.
Therefore `corridor ⊇ relevant_nodes_full_pipeline`, and Phase 2's
activity filter (step 2.6) prunes back down to the same set the
single-pass pipeline would have produced.

### 13.4 What this does *not* change

- The methodology in §7 is the contract. Optimizations that do not
  satisfy invariants A and B are wrong by construction.
- Output format (admissible-path set) and the absence of path-level
  ranking (§7.6) are unchanged. Service-level ranking, when needed,
  derives from structural signals over the path set.
- Per-system adapter contracts (§9.2) are unchanged. The "cheap
  detector" reuses the same protocols (`FailureDetector`,
  `LatencyExtractor`, `RateExtractor`) with looser thresholds and no
  precedence merge.
- Change-log entries (§15) are still required for any state,
  threshold, rule, or pipeline-step change.

---

## 14. Glossary

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

## 15. Change log

| Date | Change | Driver |
|---|---|---|
| 2026-04-25 | Initial methodology fixed; six classes (A–F) defined; Class E identified as the only current gap | Need a defensible framework before extending the state lattice further |
| 2026-04-26 | §12 added — decision methodology fixed: severity by operational tier, adapter thresholds by per-case baseline quantile (α policy), rule confidence factored as `P_struct(case) × P_causal(corpus)` with Beta(2, 2) prior. §3.E's hand-set 0.2 / 10% / 2× constants replaced by reference to §12.2 | Reviewer-defensible derivation of every numeric value; remove ad-hoc thresholds |
| 2026-04-26 | §12.2 step 3 + §12.4 row revised — bootstrap-stability metric changed from `std/|mean|` to `std/IQR(data)`. Caught during L2 calibrator implementation: lower-tail quantiles legitimately sit near zero (SILENT's Q lower tail by construction), and `std/|mean|` structurally inflates `rel_std` for near-zero quantiles even when the bootstrap is stable. IQR-scaling is independent of where the quantile lands | Methodology must be self-consistent for the discriminators it advertises (TraceVolume's lower-tail Q is the motivating case) |
| 2026-04-26 | §12.2 step 1 clarified — sub-window slicing is per-service, anchored to each service's active baseline range, not the global baseline window | Caught during L3 TraceVolumeAdapter implementation: services deployed mid-baseline (or sparsely active) would otherwise have their Q distribution dominated by zero-count subwindows from "before they existed", pinning the threshold at zero |
| 2026-04-26 | §12.2 step 1 + step 2 + step 5 + §12.4 row "Sub-window length" revised — sub-window length decoupled from abnormal-window length (fixed 30s default), discriminator is per-second rate (not count or count-ratio), abnormal aggregate is mean of abnormal-window subwindow rates. §6.1 added: "global silence" in critical-path Class E faults is a multi-entity cascade observation, NOT a confounder to subtract | Caught during first end-to-end run on RCABench dataset: chaos-mesh layout uses pre_duration ≈ duration (both ~4 min), so tying subwindow length to abnormal length collapses the baseline distribution to a single sample → calibrator opt-out → no SILENT detection at all. Probe with 30s subwindows correctly flagged the partition ground-truth services; the resulting "many co-silent entities" are not noise but the natural footprint of a critical-path fault, ranked by propagator path length not by adapter-side global-ratio subtraction |
| 2026-04-26 | TraceVolumeAdapter emits transition.at at first below-threshold abnormal sub-window (was: abnormal_window_start). inferred_edges silent gate replaced with temporal top-K-earliest (K=5) per §6.1 — cap-by-count was suppressing high-impact cascades and exploding propagator path enumeration | partition case ts4-verification-code reached 60 GB RSS path-enum stall after L6d adopted cap=30; user clarification that cascade has causal time order surfaced the right gate |
| 2026-04-26 | §7 added — state-machine operational semantics: (§7.1) single-state-per-(entity,time) with tier precedence (ERRORING shadows SILENT); (§7.2) rule firing scoped by fault class via the soft gate of §7.1; (§7.3) inferred edges as gated correlation patches (topology + class + temporal gates), not transitions; (§7.4) topology pre-pruning by `Reach_forward(injection) ∩ Reach_backward(alarm)`; (§7.5) temporal causality with per-edge-kind ε; (§7.6) the inference pipeline as a 9-step constraint-propagation-then-search composition. Renumber §7→§8…§13→§14 to make room | Class C regression on `*-exception/*-response/*-request` cases (494/500 → 433/500) traced to three concurrent methodology gaps: SILENT being assigned to Class C services whose callers gave up (no precedence rule), L5 silent rules firing on those mis-labeled services (no fault-class scope), and L6d silent inferred edges adding parallel propagation paths that crowded out true erroring chains (no gating). Path enumeration explosion (60 GB RSS) was a downstream symptom; the root cause was that constraint propagation had not been factored out of DFS. Section §7 fixes the methodology before any code change |
| 2026-04-26 | §7.1 expanded — four-tuple state definition table (PlaceKind / sensor / discriminator shape / threshold direction) makes overlap impossible by construction; intra-tier precedence rule "direct observation > absence/pressure inference" pinned with explicit tier 5/4/3 ordering (UNAVAILABLE>MISSING, ERRORING>SILENT, RESTARTING>DEGRADED); MISSING vs SILENT and DEGRADED vs SLOW disambiguated by the four-tuple. §9.2 added — per-state Protocol contracts (`FailureDetector`, `LatencyExtractor`, `RateExtractor`, `UtilizationProvider`, `LivenessProbe`, `RestartProbe`) with default fallbacks; per-system adapters provide implementations, never thresholds | Reviewer asked "what's the difference between MISSING/SILENT, DEGRADED/SLOW; each state should have a precise judgment criterion and not overlap" + "thresholds shouldn't be hard-coded; per-system adapters should define how to translate to canonical state". Current `TraceStateAdapter` bakes in `attr.http.response.status_code >= 500` and `error_rate_floor = 0.1` — both anti-patterns the new contract eliminates by hoisting to per-system protocol + §12.2 calibration |
| 2026-04-26 | §7.1 end + §7.4 + §7.6 — alarm vs deviation split. `alarm_set` is now reserved for **user-perceptible** root-Server-span deviations (`is_root_server` predicate, system-agnostic structural definition with per-system `LOADGEN_SERVICES` constant); the broader set of all non-HEALTHY nodes is `deviating_set` (propagation evidence, not search endpoints). §7.4 corridor formula uses both: `corridor = Reach_fwd(injection) ∩ Reach_bwd(alarm_set)`, then `relevant_nodes = corridor ∩ (deviating_set ∪ injection_set)` adds the activity filter. §7.6 pipeline gains Step 3 — compute the three sets and short-circuit `alarm_set == ∅` to SILENT_INJECTION before any DFS work; pipeline grows from 9 steps to 9 steps with renumbered Steps 4–9 | Reviewer clarification "alarm 指的是用户可感知的，即最 root 的 span" — earlier framing treated all baseline-deviating nodes as alarms, which (a) made `Reach_backward(alarm_set)` essentially the whole graph for cascading faults, defeating bidirectional pruning; (b) conflated *what to look for* (the user-perceptible failure) with *what counts as evidence* (every non-HEALTHY observation). Splitting the two unlocks both the activity filter and the silent-injection short-circuit. Side effect: alarm definition is now fault-class-agnostic (root-Server-span + state ≠ HEALTHY), eliminating issue #4 from the methodology vulnerability list |
| 2026-04-26 | §7.5 + §12.4 — ε generalized to `ε_eff = ε(edge_kind) + onset_resolution(src_state) + onset_resolution(dst_state)`. `onset_resolution` per state added to §12.4 policy table (ERRORING/SLOW=3 s; SILENT=30 s; DEGRADED=5 s; UNAVAILABLE/RESTARTING=1 s). Concrete examples: SILENT→SILENT on `calls` ε_eff = 65 s; ERRORING→ERRORING on `calls` ε_eff = 11 s. New §7.5 sub-section "Why per-edge tolerance is set generously" formalizes the noise-compounding argument — joint survival of an N-edge noise path is `q^N`, exponentially decaying, so per-edge ε can favor recall without inflating accepted noise (the path-length filter and §7.4's alarm-set narrowing absorb the rest) | Originally raised as vulnerability #3: SILENT onset has 30 s sub-window aliasing noise but ε(`calls`)=5 s — bucket-boundary alignment can flip apparent onset order on legitimate causal silent→silent chains, getting them rejected by §7.5. Reviewer's principled answer: "我们允许有一些噪声，因为这些噪声可以随着传播链路的增强，概率会逐渐降低" — accept per-edge noise, rely on chain-length compounding. ε_eff makes this concrete |
| 2026-04-26 | Three coupled methodology decisions: (a) §7.3 rewritten — inferred edges are **injection-anchored**, generated only in two named scenarios (Scenario A "depends_on_dead_infra" + Scenario E "gated_silenced"); the earlier co-anomaly bridge (any silent service ⇨ any alarm span) is dropped — too many spurious paths and competes with rule-based transitions. Cardinality drops from O(silent × alarm) to O(consumers of injection). (b) §7.5 adds "Onset for rule firing — earliest matching state in the trajectory" — for entities with HEALTHY→ERRORING→SILENT trajectories, rule R uses the *earliest* transition into a state ∈ R.src_states; this naturally lets silent rules fire on Class C trajectories but with an onset that the temporal gate then rejects, removing the need for §7.2's hard fault-class gate. (c) §7.6 + §12.3 — pipeline output is the SET of admissible paths, not a ranked list. Path-level confidence scoring is removed: §12.3's `P_struct × P_causal` stays as a per-rule admission gate but does not compose into chain scores. Service-level ranking, when needed, derives from structural signals (frequency / earliest onset / graph distance), not composite confidence | Reviewer locked these three together: (a) "inferred edge其实没那么多场景，如果是pod整个都不行了，那只会是我们故障注入的点就是那个pod ... 而对于前置的请求，比如说认证没过，导致后续的操作都消失了，那也只会有这种场景" — narrowing to injection-anchored scenarios; (b) "State时序的话，我们可以取之前的所有的state，比如说他之前有A，那我们就可以拿error来作为因" — earliest-state trajectory rule; (c) "路径打分其实不需要吧，它其实也没那么可靠，反而会干扰" — drop path scoring. Net effect: methodology is simpler (no fault-class hard gate, no chain composition), inference is faster (smaller inferred-edge cardinality, no scoring layer), and recall is preserved (trajectory rule keeps real chains, ε_eff keeps real onsets) |
| 2026-04-26 | §7.4 — added "Graph-completeness invariants assumed by this step" sub-section: (1) node completeness via union of k8s metrics + baseline traces + abnormal traces; (2) edge completeness via union of baseline and abnormal trace-derived edges. Together they ensure idle-in-abnormal services / edges still appear in the search space. Activity-filter dropping HEALTHY corridor nodes is exact because propagation rules never have HEALTHY as src_state; the injection point exception is rescued by `∪ injection_set` | Originally raised as vulnerability #8 — concern that idle services could be erased from the graph and silently lower recall. Reviewer confirmed the graph build (parquet_loader.build_graph_from_parquet) already unions all three sources for nodes and both windows for edges, so the invariant holds. Documenting it now keeps future per-system loaders from regressing the contract |
| 2026-04-27 | §7.1.1 added — "Structural truncation alarms — multi-signal detector". Extends `alarm_set` with a per-endpoint baseline-shape detector that catches *body-deviation* alarms (request truncated, ubiquitous downstream service missing, edge-set Jaccard collapsed) where outcome-deviation at the boundary is silent (200, normal latency). Detector is purely structural — multi-signal score gate (S1 ubiquitous-service absence, S2 span-count below baseline p10×0.5, S3 canonical-shape Jaccard < 0.5, S4 truncated-and-errored), per-endpoint promotion only when ≥5% of abnormal traces flag and ≥3 absolute, baseline ≥5 traces. Sidecar `missing_services` per flagged endpoint feeds downstream root-cause ranking. Composes by union with the canonical §7.1 `is_root_alarm_candidate` extractor. Companion change: §7.1's root-alarm predicate relaxed from `is_root_server` to `is_root_alarm_candidate` (drops Server-kind requirement; non-loadgen owner ∧ parent loadgen-or-missing) — span-kind labels are unreliable across instrumentation, so the predicate is now structural | E2E v6 vs v4 baseline on TrainTicket 500-case fixture: 493/500 (98.6%) → 498/500 (99.6%), +5 cases recovered, 0 regressions. The 5 recovered cases were all status-code-200 silent-injection mis-routes (`ts5-ts-contacts-service-pod-kill-srvmct`, `ts5-ts-travel-plan-service-container-kill-4s877v`, `ts5-ts-verification-code-service-container-kill-g22rpg`, `ts5-ts-preserve-service-container-kill-lft86s`, `ts4-ts-verification-code-service-partition-nlbl25`) where `is_root_server` returned `state(boundary) = HEALTHY` despite the call subtree being truncated; the canonical extractor was producing `alarm_set = ∅` and short-circuiting to SILENT_INJECTION. Truncation pass converts these into legitimate alarms by detecting the body-shape divergence directly, without claiming knowledge of which downstream service caused it (that's left to §7.4 corridor + §7.6 ranking) |
| 2026-04-26 | §13 added — "Implementation strategy: two-phase pipeline". Phase 1 (cheap, structural) extracts SLO-violation `cheap_alarm_set` on root Server spans only (loose thresholds: error>1% AND ≥2× baseline, p95 latency >1.5×, rate <0.5×), pre-computes injection-anchored inferred edges from k8s + baseline lineage, and builds the corridor with two BFS passes — no detector machinery, no §12.2 calibration. Phase 2 runs full §7.1 detectors only on corridor nodes (~5–20% of graph), with precedence merge / trajectory / alarm tightening / temporal pruning / DFS / verification on the same set. Output unchanged. Two correctness invariants: (A) `cheap_alarm_set ⊇ true_alarm_set` so backward-reach loses nothing recoverable; (B) `corridor ⊇ relevant_nodes_full_pipeline` because both Phase 1 expansions are over-approximations. Renumber Glossary §13 → §14 and Change log §14 → §15 to make room | Reviewer raised performance: "实现的时候怎么去快速地检测和剪枝 ... 先把 SLO violation 的 nodes 抽取出来 ... 我们只需要关心这些有 violation 的 notes 和故障注入点 ... 我们只考虑这两批 node 之间的可能的可达路径，这是第一批剪枝，第 2 批剪枝的话，我们再去剪对应的状态". The §7.6 9-step linear order is the **methodology contract**; §13 is a soundness-preserving reordering for the implementation. Detector evaluation is the dominant cost (per-(svc, case) baseline calibration + per-state passes); shifting it behind topology pruning cuts wall-clock by 80–90% on real corridors |
