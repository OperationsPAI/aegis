# Reasoning Output Quality: Bad Cases and Optimization Goals

Status: proposal / audit notes
Owner: reasoning pipeline
Scope: `src/rcabench_platform/v3/internal/reasoning/`, `cli/reason.py`, exported `result.json` and `causal_graph.json`

## Motivation

Manual audits of several TrainTicket RCA datapacks show that the reasoning
pipeline usually finds a credible service-level root cause, but the exported
explanation graph is hard to trust as-is. The recurring failures are not random:
they cluster around root-state export, alarm accounting, missing strong alarms,
and weak alarm/path noise.

This document turns those audit findings into concrete optimization targets. The
goal is not to change the underlying RCA philosophy; it is to make the exported
artifacts (`result.json`, `causal_graph.json`) internally consistent and easier
to evaluate automatically.

## Audited bad cases

The examples below are local datapacks from:

`/home/ddq/AoyangSpace/dataset/rca/`

| Case | Fault | Main verdict | Relevant output weakness |
|---|---|---|---|
| `ts4-ts-config-service-container-kill-f6w2x4` | `ContainerKill` on `ts-config-service` | Root service is correct; container restart evidence is strong | `root_causes` says `unknown`, alarm superset is wider than graph, weak/no-issue alarms are included |
| `ts0-ts-station-service-partition-7z428n` | `NetworkPartition`, `ts-station-service <-> mysql` | Root symptom on station is correct; edge/root granularity is coarse | `root_causes` says `unknown`, only 3 UI alarms are graphed while 6 strong UI issues exist, weak alarms are mixed in |
| `ts5-ts-seat-service-loss-5kglkt` | `NetworkLoss`, `ts-seat-service <-> ts-order-other-service` | Main seat/order-other network-loss chain is correct | Weak route/mysql branch is admitted despite `routes` not being a conclusion issue |
| `ts2-ts-verification-code-service-container-kill-2lmcml` | `ContainerKill` on `ts-verification-code-service` | Root service is correct; pod metadata is stale | `root_causes` says `unknown`; one alarm id is not explained by paths/graph |

## Problem 1: root state is exported as `unknown`

### Symptom

`causal_graph.json.root_causes` often contains:

```json
{
  "timestamp": null,
  "component": "service|...",
  "state": ["unknown"]
}
```

while the same component appears in `causal_graph.json.nodes` or
`result.json.visualization_paths` with a concrete timestamp and state.

### Bad cases

| Case | `root_causes` export | Concrete state already available elsewhere |
|---|---|---|
| `ts4-ts-config-service-container-kill-f6w2x4` | `service|ts-config-service`, `timestamp=null`, `state=[unknown]` | `service|ts-config-service`, `timestamp=1754978417`, `state=[degraded, unavailable]` |
| `ts0-ts-station-service-partition-7z428n` | `service|ts-station-service`, `timestamp=null`, `state=[unknown]` | `service|ts-station-service`, `timestamp=1752748144`, `state=[degraded, silent]` |
| `ts2-ts-verification-code-service-container-kill-2lmcml` | `service|ts-verification-code-service`, `timestamp=null`, `state=[unknown]` | path root is `degraded, unavailable` |
| `ts5-ts-seat-service-loss-5kglkt` | `service|ts-seat-service` / `service|ts-order-other-service`, `state=[unknown]` | paths show `ts-seat-service` as `degraded, silent` and `ts-order-other-service` as `silent` |

### Diagnosis

This is an exporter/bookkeeping bug, not a detector failure. State inference has
already produced a concrete root state; the `root_causes` serializer is not
joining it back to the graph node.

### Optimization target

Root cause exports must be state-complete when the component exists in the graph.

Acceptance criteria:

- For every `root_causes[i].component`, if an equivalent node exists in
  `causal_graph.nodes`, copy the node's earliest `timestamp` and unioned `state`.
- `state=["unknown"]` is allowed only when no stateful node exists and the reason
  is explicitly exported as `state_resolution_reason`.
- Add a regression check: no audited case above may export `timestamp=null` for a
  root component that exists in `causal_graph.nodes`.

## Problem 2: `alarm_nodes` is a candidate superset, but paths/graph only explain a subset

### Symptom

`result.json.alarm_nodes` contains many ids, while `visualization_paths` and
`causal_graph.json.alarm_nodes` contain only some of them. The current schema does
not say which alarms are explained, unexplained, or intentionally dropped.

### Bad cases

| Case | Candidate alarms | Explained by paths/graph | What is confusing |
|---|---:|---:|---|
| `ts4-ts-config-service-container-kill-f6w2x4` | 15 | 6 terminal UI alarms | 9 alarm ids are listed but not reachable in exported paths |
| `ts0-ts-station-service-partition-7z428n` | 14 | 3 graph alarm nodes | 11 alarm ids are not represented in the causal graph |
| `ts2-ts-verification-code-service-container-kill-2lmcml` | 2 | 1 strong verifycode path | One extra alarm id is present but not explained |
| `ts5-ts-seat-service-loss-5kglkt` | 9 | 5 unique terminal alarms | Some candidate alarms are absent from graph while a weak route path is included |

### Diagnosis

The pipeline appears to have at least two stages with different semantics:

- E-stage / alarm surface: broad candidate impacted nodes.
- M-stage / path finding: subset that can be connected to the root by accepted
  propagation paths.

This split is reasonable, but the export flattens both into one field named
`alarm_nodes`, which makes the output look inconsistent.

### Optimization target

Make alarm accounting explicit and lossless.

Recommended schema:

```json
{
  "candidate_alarm_nodes": [],
  "explained_alarm_nodes": [],
  "unexplained_alarm_nodes": [
    {
      "node_id": 0,
      "component": "span|...",
      "issue_strength": "strong|weak|none|unknown",
      "reason": "no_path_found|filtered_weak_issue|missing_topology_edge|max_hops|unknown"
    }
  ],
  "path_terminal_alarm_nodes": []
}
```

Acceptance criteria:

- `candidate_alarm_nodes = explained_alarm_nodes + unexplained_alarm_nodes` by
  id set, unless a node is explicitly marked as `dropped_before_path_search`.
- `causal_graph.alarm_nodes` must be documented as path-terminal alarms or be
  renamed to `path_terminal_alarm_nodes`.
- Every alarm id in `result.json` must be mappable to a component name in the
  same export.

## Problem 3: strong alarms can be missed by the causal graph

### Symptom

Some alarms with strong conclusion evidence do not appear in paths or
`causal_graph.json`. This is different from harmless candidate-alarm noise.

### Primary bad case

`ts0-ts-station-service-partition-7z428n`

`conclusion.parquet` reports six strong UI issues:

| UI endpoint | Normal success | Abnormal success | Abnormal p99 |
|---|---:|---:|---:|
| `POST /api/v1/travelplanservice/travelPlan/quickest` | 1.0 | 0.0 | ~20.003s |
| `POST /api/v1/travelplanservice/travelPlan/minStation` | 1.0 | 0.0 | ~20.001s |
| `POST /api/v1/travelplanservice/travelPlan/cheapest` | 1.0 | 0.0 | ~20.001s |
| `POST /api/v1/travelservice/trips/left` | 1.0 | 0.2 | ~20.003s |
| `POST /api/v1/travel2service/trips/left` | 1.0 | 0.0 | ~20.004s |
| `POST /api/v1/preserveservice/preserve` | 1.0 | 0.0 | ~20.001s |

But `causal_graph.json.alarm_nodes` only contains:

- `POST /api/v1/preserveservice/preserve`
- `POST /api/v1/travelservice/trips/left`
- `POST /api/v1/travel2service/trips/left`

The three `travelPlan/*` endpoints are strong alarms and should not disappear
without explanation.

### Diagnosis

The graph has a coverage gap. Possible causes to investigate:

- missing or filtered topology edges from `station/basic` to `travel-plan`;
- max-hop or path ranking pruning before all strong alarms are covered;
- M-stage accepting only a subset of alarm clusters;
- endpoint/span name mismatch between alarm detection and topology graph.

### Optimization target

Strong alarms must be either explained by a path or explicitly reported as
strong-unexplained.

Acceptance criteria:

- Define `issue_strength=strong` from conclusion evidence. Initial rule:
  `Issues != {}` OR success-rate drop crosses the same threshold used by alarm
  detection OR p99/avg duration anomaly is above the alarm threshold.
- For each strong candidate alarm, export one of:
  - a path from a root to that alarm;
  - an `unexplained_alarm_nodes` entry with a concrete failure reason.
- Add a coverage metric:

```text
strong_alarm_coverage = explained_strong_alarm_count / candidate_strong_alarm_count
```

- Regression target for `ts0-ts-station-service-partition-7z428n`:
  `travelPlan/quickest`, `travelPlan/minStation`, and `travelPlan/cheapest`
  must be explained or explicitly marked `strong_unexplained`.

## Problem 4: weak alarm noise is mixed with real alarms and sometimes enters high-confidence paths

### Symptom

Some candidate alarms have weak or no conclusion evidence, but are still listed
as alarms. In one case, weak topology paths are admitted into the explanation
graph with high confidence.

### Bad cases

| Case | Weak/noisy node | Evidence weakness | Current bad behavior |
|---|---|---|---|
| `ts5-ts-seat-service-loss-5kglkt` | `ts-ui-dashboard::GET /api/v1/routeservice/routes` via `ts-order-other-service -> mysql -> ts-route-service` | `Issues={}`, success rate `1.0 -> 1.0`, `ts-route-service` has no errors and service p99 is not degraded | Weak route/mysql branch is admitted as a full-confidence path |
| `ts4-ts-config-service-container-kill-f6w2x4` | `orderservice/order/refresh` | `Issues={}`, success rate `1.0 -> 1.0`, latency change is small/outlier-like | Included in candidate alarm surface |
| `ts4-ts-config-service-container-kill-f6w2x4` | `routeservice/routes` | `Issues={}`, success rate `1.0 -> 1.0` | Included as weak/no-issue alarm candidate in the broad surface |
| `ts0-ts-station-service-partition-7z428n` | `auth`, `consign`, `cancel`, `trainservice/trains`, `inside_payment` candidates | not part of the station/basic/travel main propagation; several are not conclusion issues | Listed alongside strong travel/preserve alarms without strength labels |

### Diagnosis

The alarm surface is intentionally broad, but the graph does not consistently
separate weak candidates from strong user-facing issues. Shared dependencies
(e.g. MySQL) and global traffic drops can create plausible topology routes that
are not specific to the injected fault.

### Optimization target

Weak candidates should not be indistinguishable from strong alarms, and weak
paths should not receive maximum confidence without strong endpoint evidence.

Acceptance criteria:

- Add `issue_strength` to candidate alarms. Suggested values:
  - `strong`: conclusion issue exists or success rate materially drops;
  - `weak`: only small p99/avg shift, small sample, or heuristic-only signal;
  - `none`: `Issues={}` and no material success/latency regression;
  - `unknown`: conclusion row unavailable.
- Default graph export includes strong alarms and paths first. Weak/no-issue
  paths require `include_weak_paths=true` or must be separated under
  `weak_paths`.
- Path confidence must be penalized when the terminal alarm is weak/no-issue.
- A path whose terminal endpoint has `Issues={}` and success rate is unchanged
  cannot be exported with `confidence=1.0` unless another explicit evidence
  source is attached.
- Regression target for `ts5-ts-seat-service-loss-5kglkt`: the
  `order-other -> mysql -> route-service -> routes` branch should be marked
  `weak_path` or removed from the default causal graph.

## Cross-cutting optimization goals

### Goal A: Internal consistency of exports

`result.json` and `causal_graph.json` must agree on component identity, root
state, and alarm accounting.

Checks:

- every root component has a concrete state if it appears in graph nodes;
- every alarm id maps to a component name;
- every path-terminal alarm is included in `explained_alarm_nodes`;
- `causal_graph.alarm_nodes` is a documented subset, not an ambiguous duplicate.

### Goal B: Strong-alarm coverage before path count

The graph should optimize for explaining distinct strong user-facing alarms, not
for enumerating many similar span-level paths.

Checks:

- report `strong_alarm_coverage`;
- report `unexplained_strong_alarm_count`;
- fail or warn when strong coverage is below a threshold, initially 0.8 for
  TrainTicket audits.

### Goal C: Weak-signal isolation

Weak/no-issue candidates should be visible for debugging but not mixed into the
main RCA graph as if they were strong causal evidence.

Checks:

- no `Issues={}` terminal endpoint in default graph with `confidence=1.0`;
- weak paths are exported under a separate section or require an explicit flag;
- shared-dependency paths require endpoint issue evidence or client-side logs.

### Goal D: Regression dataset for output quality

Use these bad cases as a small output-quality regression suite.

Minimum suite:

```text
/home/ddq/AoyangSpace/dataset/rca/ts4-ts-config-service-container-kill-f6w2x4
/home/ddq/AoyangSpace/dataset/rca/ts0-ts-station-service-partition-7z428n
/home/ddq/AoyangSpace/dataset/rca/ts5-ts-seat-service-loss-5kglkt
/home/ddq/AoyangSpace/dataset/rca/ts2-ts-verification-code-service-container-kill-2lmcml
```

The suite should validate exported artifacts without rerunning injection.

## Non-goals

- Do not require MySQL/server-side traces to represent a DB/network-edge root.
  Client-side evidence is valid, but the root metadata must preserve the target.
- Do not remove the broad alarm detector. Broad detection is useful for recall;
  the fix is to label and account for candidate strength.
- Do not hide unexplained alarms. They are useful debugging signals, especially
  when they are strong.
