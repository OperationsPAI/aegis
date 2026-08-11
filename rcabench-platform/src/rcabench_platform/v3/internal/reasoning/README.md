# reasoning

Fault propagation analysis for cloud-native systems. Given a datapack — the
parquet bundle aegis captures around one fault injection — this package decides
whether the fault actually did anything, whether it reached the SLO surface, and
if so which causal chain carried it there.

It is the ground-truth labeller behind aegis's `attributed` / `absorbed` /
`ineffective` verdicts, not an RCA algorithm. It knows what was injected; its job
is to argue from evidence that the injection explains the observed damage.

## Design philosophy

### Observation, not derivation

The central commitment. When fault F at entity E makes downstream entity E'
misbehave, the engine does **not** derive E' from E. E' is abnormal in its own
right, observed independently by whichever adapter watches E'. Cascades emerge
because the same adapters fire at neighbouring entities; the dependency graph
only connects observations that were made separately.

The alternative — writing cascade rules that propagate "if upstream is down then
downstream is down" — collapses as soon as reality disagrees with the rule. Here
a missing downstream observation simply means no path, which is itself a
reportable outcome (`unexplained_impact`) rather than a fabricated chain.

### Every fault has exactly one primary observation class

Faults are partitioned by the layer at which their primary feature is
*physically* introduced, not by what they are named or what they incidentally
disturb. Six classes, each with one adapter family and one canonical
representation:

| Class | Mechanism | Adapter | Canonical state |
| --- | --- | --- | --- |
| A — infrastructure death | SIGKILL, OOMKill, pod terminating | `K8sMetricsAdapter` | `container`/`pod` → `unavailable` |
| B — resource pressure | CPU throttle, memory near limit | `K8sMetricsAdapter` | `container` → `degraded` |
| C — logical error | 5xx, thrown exception, corrupted payload | `TraceStateAdapter` | `span` → `erroring` |
| D — latency injection | thread sleep, netem delay, held response | `TraceStateAdapter` | `span` → `slow` |
| E — traffic isolation | partition, blackhole, DNS failure | `TraceVolumeAdapter` | `service`/`span` → `silent` |
| F — runtime internal | GC pause, heap pressure | `JvmAugmenterAdapter` | augmentation labels |

Two faults share a class **iff** the same adapter observes the same signal with
the same representation. Collisions *within* a class are fine — telling
`network_delay` from `jvm_latency` is not this layer's problem. Distinctness
*across* classes is required, and is what makes the state lattice defensible
when someone asks why these states and not others.

This matters operationally: when a new fault type arrives, the question "extend
an adapter, add a label, or add a state?" has one answer, derived from where the
fault physically bites.

### Thresholds are calibrated, not chosen

No hand-set ratio constants. Adaptive thresholds come from per-(entity, case)
baseline quantiles at a policy-level α. A fault that manifests differently on a
quiet service and a busy one is still detected on both, and nobody has to retune
a magic number when a new benchmark lands.

### Manifests carry per-fault knowledge; code stays generic

What a given fault should look like — entry signature, expected features per
propagation layer, decay, fan-out limits, hand-offs to related fault types —
lives in a declarative YAML manifest under `manifests/fault_types/`, one per
fault type (32 today). The engine itself has no `if fault_type == ...` branches.
A missing manifest is a documented, supported state: the engine falls back to
the generic rule path.

### Gates decide; builders only produce

`PathBuilder` is a pure data producer. Every admit/reject decision runs through
a defense-in-depth gate chain (`algorithms/gates/`): topology → drift →
temporal → inject-time, plus manifest entry/layer gates when a manifest is
active. Rejections are retained on the result with their reason, so a
`no_paths` outcome can be explained rather than merely reported.

Depth on all of this: [`docs/reasoning-feature-taxonomy.md`](../../../../../docs/reasoning-feature-taxonomy.md)
is the governing methodology — every change to the state lattice or adapter set
is expected to argue from it. The manifest schema spec is
[`docs/forge_rework/SCHEMA.md`](../../../../../docs/forge_rework/SCHEMA.md).

## How a case flows through

```
datapack/                          ← {normal,abnormal}_{traces,logs,metrics,metrics_sum}.parquet
   │                                 conclusion.parquet, injection.json
   ▼
ParquetDataLoader                  loaders/      build HyperGraph, identify alarm nodes
   ▼
IR adapters                        ir/adapters/  emit Transitions from sensor streams
   ▼
synth_timelines → run_fixpoint     ir/           per-node StateTimeline, inferred edges
   ▼
FaultPropagator                    algorithms/   explore topology, match rules, run gates
   ▼
classify(L, E, has_path)           algorithms/label_classifier.py
   ▼
result.json + causal_graph.json    export/       written next to the datapack
```

The IR pass runs in two phases: observation adapters emit first, then
`StructuralInheritanceAdapter` sees those timelines and fills in derived nodes
(pod/service/span) that had no observation of their own. Structural containment
is expressed *only* there — rule matching downstream never learns about it.

### The five labels

`classify` is a pure function of a three-part decomposition: local effect `L`
at the injection point, SLO impact `E` at the surface, mechanism `M` as an
admitted path.

| Label | Condition | Meaning |
| --- | --- | --- |
| `ineffective` | `L=0` | Nothing observable happened at the injection point |
| `absorbed` | `L=1, E=0` | Fault was real but never reached the SLO surface |
| `attributed` | `L=1, E=1, M=path` | Full causal chain — the usable case |
| `unexplained_impact` | `L=1, E=1, M=∅` | SLO broke, no gate-admitted path explains it |
| `contaminated` | — | Reserved for multi-fault analysis; unreachable today |

`unexplained_impact` is a feature. It marks cases where the evidence does not
support the story, instead of manufacturing one.

## Usage

### CLI

Mounted as a sub-app on the platform CLI by `cli/reason.py`:

```bash
# one case
./cli/reason.py reason run --data-dir path/to/datapack --max-hops 15

# a directory of cases, in parallel; skips already-processed ones
./cli/reason.py reason batch \
    --data-base-dir data/jfs/rcabench_dataset \
    --max-workers 12
./cli/reason.py reason batch --data-base-dir ... --retry-no-paths   # only redo no_paths
./cli/reason.py reason batch --data-base-dir ... --force            # ignore prior results

# quality-filter datapacks before spending time reasoning over them
./cli/reason.py reason filter-clean --data-base-dir ... > clean.txt
```

`--manifest-dir` overrides the shipped manifests; pointing it at an empty
directory forces the generic-rule fallback for every fault type, which is the
way to measure what the manifests are buying you.

`filter-clean` rejects datapacks that cannot support a verdict — too few
distinct services in the abnormal window, or too large a gap between the normal
and abnormal windows — and prints a breakdown of rejection reasons to stderr.
Run it first on any new dataset.

### Python

```python
from rcabench_platform.v3.internal.reasoning.cli import run_single_case

result = run_single_case(
    data_dir=Path("path/to/datapack"),
    max_hops=15,
    injection_data=json.loads((dp / "injection.json").read_text()),
)
result["label"]            # the five-class verdict — read this one
result["paths"]            # admitted path count
result["status"]           # success | no_alarms | no_paths | error
```

`label` is the verdict. `status` is a lossy back-compat bucket derived from it
by `_label_to_legacy_status` — `attributed` becomes `success`, everything else
collapses into `no_paths` or (when no SLO impact was detected) `no_alarms`. It
exists so batch skip-logic and the marker files keep working; new code should
read `label`. `error` is the one status not derived from a label: it means the
case threw.

Batch drivers such as `scripts/e2e_validate.py` import `run_single_case`
directly rather than going through typer. Doing so skips
`_init_manifest_registry`, so the manifest registry lazily self-initialises from
the packaged directory — direct callers get manifest-aware behaviour by default.
Pass an explicit registry via `ReasoningContext` if you need a sandboxed one.

### Outputs

Written beside the datapack, with any previous run's artifacts cleared first:

- `result.json` — verdict, paths, rejected paths with reasons, alarm accounting
- `causal_graph.json` — the admitted chain, for visualisation
- `no_alarms.marker` / `no_paths.marker` — cheap markers for batch resumption

## Extending it

**A new fault type.** Write `manifests/fault_types/<name>.yaml`. Decide its
observation class first (taxonomy §3) — that determines whether you also need
adapter work, or whether existing adapters already surface the signal. Lint
before committing:

```bash
python -m rcabench_platform.v3.internal.reasoning.manifests.lint \
    src/rcabench_platform/v3/internal/reasoning/manifests/fault_types
```

The linter enforces per-file schema plus cross-document rules: `target_kind`
must agree with the closed fault catalog in `models/injection.py`, and every
`hand_offs.to` must name a fault type that has its own manifest.

**A new benchmark stack.** Taxonomy §9. Usually adapter-level: the six classes
are stack-independent, but the sensor streams that expose them are not.

**A new canonical state.** Taxonomy §10, and expect to argue for it. A state
earns its place by being the canonical representation of an observation class
that no existing state covers — not by being convenient for one fault. Add it
together with the adapter that emits it, never pre-emptively.

Tests live in `ir_tests/` and are on the default pytest path — `just test` runs
them. They are unit-level over fixtures in `ir_tests/fixtures/`, not
cluster-dependent.

## Layout

| Path | Role |
| --- | --- |
| `runner.py` | Single-case orchestration — the spine worth reading first |
| `cli.py` | Typer facade: `run`, `batch`, `filter-clean` |
| `batch.py` | Parallel driver, case classification, resumption |
| `ir/` | Canonical-state IR: adapters, transitions, timelines, fixpoint inference |
| `algorithms/` | Propagator, topology explorer, rule matcher, gates, calibrators |
| `algorithms/gates/` | The admission chain — all validity decisions live here |
| `manifests/` | Per-fault YAML schema, loader, registry, linter, feature extractors |
| `models/` | HyperGraph, injection resolution, propagation result types |
| `loaders/` | Parquet reading, trace truncation detection, timing |
| `rules/` | Built-in and structural propagation rules (generic fallback path) |
| `export/`, `exporters/` | Result writing, causal graph, pattern export |
| `ir_tests/` | Test suite and fixtures |
