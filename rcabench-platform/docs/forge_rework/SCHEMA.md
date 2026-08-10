# FaultManifest Schema

Authoritative spec for fault-type-conditioned manifests. Phase 1 plumbing agent implements the Pydantic model + YAML loader; Phase 2 manifest agents author one manifest YAML per fault type.

## File layout

```
src/rcabench_platform/v3/internal/reasoning/manifests/
├── schema.py                        # Pydantic FaultManifest model (Phase 1)
├── loader.py                        # YAML → FaultManifest (Phase 1)
├── registry.py                      # ManifestRegistry, fault_type_name → Mτ (Phase 1)
└── fault_types/                     # Phase 2 agents write here
    ├── cpu_stress.yaml              # one file per fault type, snake_case name
    ├── memory_stress.yaml
    ├── pod_kill.yaml
    └── ...
```

## YAML schema

```yaml
# Required
fault_type_name: CPUStress           # MUST match key in FAULT_TYPE_TO_SEED_TIER
target_kind: container               # one of: container | pod | service | span
seed_tier: degraded                  # MUST match fault_seed.py mapping
description: |                       # human-readable, 1-3 sentences
  Container-level CPU contention via cgroup throttle.
  Causes thread queue buildup and visible latency on inbound spans.

# Required: features that MUST appear at v_root within entry_window_sec of t0
entry_signature:
  entry_window_sec: 30               # observation window after t0; default 30
  required_features:                 # ALL must match (AND)
    - kind: container
      feature: cpu_throttle_ratio    # see "Feature vocabulary" below
      band: [3.0, .inf]              # ratio vs baseline; semi-open [low, high]
      magnitude_source: theoretical  # theoretical | empirical
  optional_features:                 # at least N must match (default N=0)
    - kind: container
      feature: thread_queue_depth
      band: [2.0, .inf]
      magnitude_source: empirical
    - kind: span
      feature: latency_p99_ratio
      band: [2.0, .inf]
      magnitude_source: empirical
  optional_min_match: 1              # how many of optional_features must match

# Required: forward-derivation tree, layer by layer
derivation_layers:
  - layer: 1                          # 1 = immediate downstream
    edge_kinds: [includes, routes_to]
    edge_directions: [forward, backward]   # parallel arrays with edge_kinds
    expected_features:                # downstream node MUST match ≥1 of these
      - kind: span
        feature: latency_p99_ratio
        band: [1.5, .inf]
        magnitude_decay: 0.7          # band[0] of layer N+1 = band[0] of layer N × decay
    max_fanout: 32                    # safety cap on per-layer expansion
  - layer: 2                          # transitive callers
    edge_kinds: [calls]
    edge_directions: [backward]
    expected_features:
      - kind: span
        feature: latency_p99_ratio
        band: [1.2, .inf]
        magnitude_decay: 0.5
      - kind: span
        feature: error_rate
        band: [0.05, 1.0]             # absolute, not ratio
        magnitude_decay: null          # no decay (absolute metric)

# Optional: cascade transitions to other fault types
hand_offs:
  - to: HTTPResponseAbort             # MUST be a known fault_type_name
    trigger:                          # downstream node satisfies this → hand off
      kind: span
      feature: error_rate
      threshold: 0.2
    on_layer: 2                       # which layer of THIS manifest can hand off
    rationale: |
      Sustained high error rate downstream of CPU stress is read by
      far-downstream callers as response-abort symptoms.

# Optional: terminal conditions; verification stops descending here
terminals:
  - kind: span                        # node-kind-based terminal
    feature: latency_p99_ratio
    band: [.inf, .inf]                # never satisfied = no early termination
```

## Feature vocabulary

The Phase 1 plumbing agent defines the canonical feature names in `manifests/features.py`. Manifest agents reference these names; new features must be added to that file with extraction logic in the IR adapters.

**Bootstrap set** (Phase 1 ships these; Phase 2 may request additions):

| Feature name | Kind(s) | Type | Description |
|---|---|---|---|
| `cpu_throttle_ratio` | container, pod | ratio | CPU throttle counter delta / baseline |
| `memory_usage_ratio` | container, pod | ratio | Memory bytes / limit |
| `thread_queue_depth` | container | ratio | Queue depth / baseline |
| `gc_pause_ratio` | container | ratio | GC pause time / window time |
| `restart_count` | container, pod | absolute | Restart events in window |
| `latency_p99_ratio` | span | ratio | P99 latency / baseline P99 |
| `latency_p50_ratio` | span | ratio | P50 latency / baseline P50 |
| `error_rate` | span | absolute | Error span count / total span count, ∈ [0, 1] |
| `request_count_ratio` | span | ratio | Span count / baseline count |
| `silent` | span, service | boolean | No spans observed when expected |
| `unavailable` | pod, service | boolean | Endpoint unreachable |
| `dns_failure_rate` | span | absolute | DNS error spans / total ∈ [0, 1] |
| `connection_refused_rate` | span | absolute | Refused conn rate ∈ [0, 1] |
| `timeout_rate` | span | absolute | Timeout rate ∈ [0, 1] |

## Magnitude band semantics

- Bands are `[low, high]` semi-open intervals; `low` inclusive, `high` exclusive.
- `.inf` means no upper bound.
- For `magnitude_source: theoretical`, the band reflects mechanism-level expectation (e.g., CPU stress at 80% load → throttle ratio ≥ 3×). Use chaos engineering literature + injection params.
- For `magnitude_source: empirical`, the band reflects measured 5th–95th percentile across ≥3 sample injection cases of the same fault type.
- A feature **matches** if any timeline window's measured value falls in `[low, high]`.
- `magnitude_decay` (per layer) shrinks the lower bound geometrically: layer N+1 lower = layer N lower × decay. The schema layer's own band is the AUTHORITATIVE one; decay is just a hint for verification when sampling deeper layers.

## Hand-off semantics

- A node admitted at `derivation_layers[k]` of Mτ may hand off to Mτ' if the node also satisfies Mτ'.entry_signature (using the on_layer trigger as quick prefilter).
- Hand-offs form a **finite** dependency graph; orchestrator detects cycles by tracking visited (node, fault_type) pairs.
- A path may chain at most 2 hand-offs (i.e., ≤ 3 fault types per path) — global orchestrator-enforced cap to avoid combinatorial blowup. Document any case where this cap kicks in.

## Validation rules (Pydantic)

The Phase 1 plumbing agent implements:

1. `fault_type_name` must exist in `FAULT_TYPE_TO_SEED_TIER`.
2. `seed_tier` MUST equal `FAULT_TYPE_TO_SEED_TIER[fault_type_name]` (consistency check).
3. `target_kind` matches the kind that fault_type physically targets (cross-check with `injection.py`).
4. Every `feature` referenced exists in `manifests/features.py` vocabulary.
5. Every `to:` in hand_offs references a manifest that also exists (resolved at registry-load time).
6. `band[0] <= band[1]` (with `.inf` handling).
7. `entry_window_sec` ≤ 60 (sanity).
8. `derivation_layers` non-empty; layers strictly increasing; max layer ≤ 5.

Invalid manifests fail loud at registry load time. Phase 1 ships a CLI: `python -m rcabench_platform.v3.internal.reasoning.manifests.lint manifests/fault_types/`.

## Worked example: CPUStress (full)

```yaml
fault_type_name: CPUStress
target_kind: container
seed_tier: degraded
description: |
  Container-level CPU contention injected via cgroup CPU throttle.
  Throttling causes thread queue buildup, GC pressure (on JVM), and
  visible P99 latency increase on inbound spans handled by the
  affected container.

entry_signature:
  entry_window_sec: 30
  required_features:
    - kind: container
      feature: cpu_throttle_ratio
      band: [3.0, .inf]
      magnitude_source: theoretical
  optional_features:
    - kind: container
      feature: thread_queue_depth
      band: [2.0, .inf]
      magnitude_source: empirical
    - kind: span
      feature: latency_p99_ratio
      band: [2.0, .inf]
      magnitude_source: empirical
  optional_min_match: 1

derivation_layers:
  - layer: 1
    edge_kinds: [runs, routes_to, includes]
    edge_directions: [backward, backward, forward]
    expected_features:
      - kind: span
        feature: latency_p99_ratio
        band: [1.5, .inf]
        magnitude_decay: 0.7
      - kind: pod
        feature: cpu_throttle_ratio
        band: [3.0, .inf]
        magnitude_decay: 1.0
    max_fanout: 32
  - layer: 2
    edge_kinds: [calls]
    edge_directions: [backward]
    expected_features:
      - kind: span
        feature: latency_p99_ratio
        band: [1.2, .inf]
        magnitude_decay: 0.5
      - kind: span
        feature: timeout_rate
        band: [0.05, 1.0]
    max_fanout: 32
  - layer: 3
    edge_kinds: [calls]
    edge_directions: [backward]
    expected_features:
      - kind: span
        feature: latency_p99_ratio
        band: [1.1, .inf]
        magnitude_decay: 0.3
      - kind: span
        feature: error_rate
        band: [0.05, 1.0]
    max_fanout: 16

hand_offs:
  - to: HTTPResponseAbort
    trigger:
      kind: span
      feature: timeout_rate
      threshold: 0.3
    on_layer: 2
    rationale: |
      Persistent upstream latency past timeout budget produces
      response-abort symptoms at far-downstream callers.

terminals: []
```
