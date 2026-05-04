# Reasoning Export Consistency: Bad Cases and Optimization Requirements

Status: proposal / audit-derived requirements
Owner: reasoning pipeline
Scope: `src/rcabench_platform/v3/internal/reasoning/`, `cli/reason.py`, exported `result.json` and `causal_graph.json`

## Motivation

A follow-up audit of the new causal graph export directory found that the export
is cleaner than the older graph in several cases, but three schema/state issues
remain recurring and can mislead downstream evaluators:

1. `result.json.alarm_nodes` and `causal_graph.json.alarm_nodes` use different
   effective scopes.
2. The root component can be credible in `causal_graph.json`, while
   `result.json.propagation_result.injection_states` still says `unknown`.
3. Caller-side spans that are clearly erroring in raw traces can be exported as
   `healthy, missing`.

This document records concrete bad cases and defines optimization targets for
these three issues.

## Audited export set

New graph export directory:

`/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/causal_graph_export_new_reason_20260504_084535`

Raw case directory:

`/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases`

## Problem 1: `result.json` alarm count and graph alarm count disagree

### Symptom

The raw `result.json` contains a broader `alarm_nodes` set, while the exported
`causal_graph.json` contains only the explained/path-terminal alarm subset. The
new graph is visually cleaner, but the field name `alarm_nodes` is ambiguous:
consumers cannot tell whether it means all detected alarms or only alarms that
survived path explanation.

### Bad cases

| Case | `result.json.alarm_nodes` | `causal_graph.json.alarm_nodes` | Interpretation |
|---|---:|---:|---|
| `otel-demo15-currency-pod-kill-xvmpqx` | 16 | 2 | 14 result alarms are hidden from the exported graph |
| `hs0-user-pod-failure-cnf9cr` | 12 | 6 | graph keeps the explained frontend HTTP alarms only |
| `hs1-recommendation-pod-failure-frllxg` | 6 | 2 | weak reservation/attractions candidates are hidden |
| `hs31-recommendation-pod-failure-dn57db` | 3 | 2 | weak `NearbyRest` candidate is hidden |
| `ts0-ts-preserve-service-response-replace-body-644lf4` | 7 | 1 | only the strong `/preserve` terminal alarm is exported |

### Why this is a problem

The graph may be intentionally exporting an explained subgraph, but the current
schema does not make that contract explicit. A downstream evaluator reading only
`causal_graph.json` may conclude that the case had only 1-2 alarms, while
`result.json` shows a broader candidate set.

This also blocks quality metrics such as alarm recall, strong-alarm coverage,
and weak-noise filtering, because the reason for dropping each candidate is not
carried into the graph export.

### Optimization target

Make alarm scope explicit and lossless across exports.

Required fields or equivalent schema:

```json
{
  "candidate_alarm_nodes": [],
  "explained_alarm_nodes": [],
  "unexplained_alarm_nodes": [
    {
      "node_id": 0,
      "component": "span|...",
      "issue_strength": "strong|weak|none|unknown",
      "drop_reason": "no_path|weak_noise|filtered_by_rank|max_hops|schema_unmatched|unknown"
    }
  ],
  "path_terminal_alarm_nodes": []
}
```

Acceptance criteria:

- `causal_graph.json` must not expose an ambiguous `alarm_nodes` field without a
  documented scope. Prefer `path_terminal_alarm_nodes` for the current graph
  subset.
- Every `result.json.alarm_nodes` entry must be recoverable from either
  `candidate_alarm_nodes`, `explained_alarm_nodes`, or `unexplained_alarm_nodes`.
- If a node is hidden from the default graph because it is weak/noisy, export the
  reason rather than silently dropping it.
- Add a regression assertion for the five bad cases above: the exported graph
  must report both the original candidate count and the explained count.

## Problem 2: root is credible in the graph, but `injection_states` remains `unknown`

### Symptom

For PodFailure/PodKill cases, the new `causal_graph.json.root_causes` often has a
concrete and correct state such as `unavailable`, while `result.json` still
reports the corresponding injection state as `unknown`.

### Bad cases

| Case | Root in new graph | `result.json.propagation_result.injection_states` | Evidence-backed state |
|---|---|---|---|
| `hs0-user-pod-failure-cnf9cr` | `service|user`, `state=[unavailable]` | `[unknown]` | deployment available `1 -> 0`, container ready `1 -> 0`, service span disappears |
| `hs1-recommendation-pod-failure-frllxg` | `service|recommendation`, `state=[unavailable]` | `[unknown]` | recommendation server span `725 -> 0`, container ready `1 -> 0` |
| `hs31-recommendation-pod-failure-dn57db` | `service|recommendation`, `state=[unavailable]` | `[unknown]` | recommendation server span `601 -> 0`, frontend `/recommendations` 500 |
| `otel-demo15-currency-pod-kill-xvmpqx` | `service|currency`, `state=[degraded, unavailable]` | `[unknown]` | pod kill event and ReplicaSet available transiently `1 -> 0` |

Counterexample still failing even in the graph:

| Case | Root in new graph | Problem |
|---|---|---|
| `ts0-ts-preserve-service-response-replace-body-644lf4` | `service|ts-preserve-service`, `state=[unknown]` | `state_resolution_reason=root_component_not_in_causal_graph`; root component is not included as a graph node |

### Why this is a problem

The same run exports two contradictory root-state stories:

- `result.json`: injection state is unknown.
- `causal_graph.json`: root is unavailable/degraded.

The graph state is often correct, but the disagreement makes automated
regression checks brittle and makes it unclear which artifact is authoritative.

### Optimization target

Define one canonical root-state source and use it consistently in all exports.

Acceptance criteria:

- If `causal_graph.root_causes[i].component` has a concrete state, the matching
  `result.json.propagation_result.injection_states[i]` must be the same state or
  must reference the same canonical root-state object.
- `unknown` is allowed only with an explicit reason, for example:
  - `root_component_not_in_causal_graph`
  - `no_state_window_for_injection_node`
  - `root_resolved_from_metadata_only`
- For lifecycle faults (`PodKill`, `PodFailure`, `ContainerKill`), successful
  injection plus K8s evidence should seed `unavailable` at the resolved root even
  if no trace span exists.
- Add a regression check: no PodFailure/PodKill case with `k8s.container.ready=0`
  or deployment available `0` may export `injection_states=[unknown]` without a
  reason.

## Problem 3: caller-side span state is exported as `healthy, missing` despite raw errors

### Symptom

When a callee service is unavailable, the callee server span is truly missing.
However, the caller-side client span often still exists and records errors. The
new graph sometimes marks that caller span as `healthy, missing` instead of
`erroring`.

### Bad cases

| Case | Caller span | Raw abnormal evidence | Exported state | Expected state |
|---|---|---|---|---|
| `hs1-recommendation-pod-failure-frllxg` | `span|frontend::recommendation.Recommendation/GetRecommendations` | 841 rows, 839 `Error` | `[healthy, missing]` | `erroring` |
| `hs31-recommendation-pod-failure-dn57db` | `span|frontend::recommendation.Recommendation/GetRecommendations` | 671 rows, 671 `Error` | `[missing, healthy]` | `erroring` |
| `hs0-user-pod-failure-cnf9cr` | `span|frontend::user.User/CheckUser` | 376 rows, 376 `Error` | `[healthy, missing]` | `erroring` |

The callee-side server spans are correctly missing:

- `recommendation::recommendation.Recommendation/GetRecommendations`: normal
  725/601 rows in the two recommendation cases, abnormal 0 rows.
- `user::user.User/CheckUser`: normal 301 rows, abnormal 0 rows.

The caller-side spans are different: they are present and erroring.

### Why this is a problem

The state vocabulary conflates two distinct observations:

- **callee server span missing**: request never reaches or is not served by the
  failed service;
- **caller client span erroring**: caller attempted the RPC and recorded failure.

If both sides are marked `missing`, the graph loses the important fact that the
frontend observed an explicit RPC failure. If a caller span is also marked
`healthy`, the state set becomes contradictory and weakens path interpretation.

### Optimization target

State assignment must be side-aware for RPC edges.

Acceptance criteria:

- For caller/client spans, if abnormal rows exist and `attr.status_code=Error`
  or HTTP status is 5xx above threshold, emit `erroring`.
- Emit `missing` for a caller/client span only when the span is materially absent
  relative to baseline and there is no stronger erroring evidence.
- Do not co-export `healthy` with `erroring`, `unavailable`, or strong `missing`
  for the same timestamp unless the states are explicitly separated by time
  window.
- For callee/server spans, retain `missing` when baseline rows exist and abnormal
  rows drop to zero.
- Add regression assertions:
  - `hs1-recommendation-pod-failure-frllxg` frontend recommendation client span
    must include `erroring`.
  - `hs31-recommendation-pod-failure-dn57db` frontend recommendation client span
    must include `erroring`.
  - `hs0-user-pod-failure-cnf9cr` frontend user CheckUser client span must
    include `erroring`.

## Cross-cutting export requirements

### Requirement A: graph export must be auditable without `result.json`

`causal_graph.json` should either contain enough metadata for audit, or clearly
state that it is an explained-subgraph view and point to the required source
fields in `result.json`.

Minimum useful metadata:

- `case_name`
- `fault_type`
- `root_resolution_method`
- `candidate_alarm_count`
- `explained_alarm_count`
- `unexplained_alarm_count`
- `strong_alarm_coverage` with explicit zero-denominator handling

### Requirement B: zero-denominator coverage must not report success silently

If `candidate_strong_alarm_count=0`, export:

```json
{
  "strong_alarm_coverage": null,
  "strong_alarm_coverage_reason": "no_candidate_strong_alarms"
}
```

Do not export `1.0` for an empty strong-alarm set.

### Requirement C: confidence must distinguish rule admission from evidence quality

For these bad cases, the path itself is often topologically plausible, but the
state/alarm evidence is incomplete. Export either separate fields or an explicit
breakdown:

```json
{
  "rule_admission_confidence": 0.8,
  "evidence_confidence": 0.0,
  "alarm_coverage_confidence": 0.0,
  "final_confidence": 0.0
}
```

The exact scoring can be refined later; the immediate goal is to prevent a
single `confidence=0.8` or `1.0` from implying full evidential certainty.

## Minimal regression suite

Use these cases to gate the export changes:

```text
/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases/hs0-user-pod-failure-cnf9cr
/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases/hs1-recommendation-pod-failure-frllxg
/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases/hs31-recommendation-pod-failure-dn57db
/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases/otel-demo15-currency-pod-kill-xvmpqx
/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases/ts0-ts-preserve-service-response-replace-body-644lf4
```

The regression tests should validate exported JSON only; they should not rerun
fault injection.

## Non-goals

- This document does not require broad candidate alarm detection to be narrowed
  immediately. Broad recall is useful; the requirement is to carry the drop and
  explanation status into the export.
- This document does not require all weak/noisy alarms to be graphed by default.
  It requires them to be auditable.
- This document does not redefine the canonical state lattice. It only requires
  current states to be assigned to the correct side of an RPC edge.
