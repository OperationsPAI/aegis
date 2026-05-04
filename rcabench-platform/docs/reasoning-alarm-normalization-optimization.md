# Reasoning Alarm Normalization: Optimization Plan

Status: proposal / audit-derived requirements
Owner: reasoning pipeline
Scope: `src/rcabench_platform/v3/internal/reasoning/`, `cli/reason.py`, exported `result.json`, `causal_graph.json`, and `conclusion.parquet` consumers

## Motivation

Recent manual audits show two alarm-accounting defects that make otherwise
credible RCA outputs look inconsistent:

1. Strong user-facing alarms in `conclusion.parquet` can be missed because the
   conclusion row key does not match the graph component id.
2. Alarm counts differ across `candidate`, `explained`, and `causal_graph`
   fields because the fields use different scopes without an explicit schema
   contract.

These are primarily normalization/export problems, not evidence problems. The
raw data and conclusion rows often contain enough information, but the reasoning
export loses the mapping.

## Audited examples

### Example A: strong alarm missed by component-name mismatch

Case:

`/home/ddq/AoyangSpace/dataset/dataset_v3_beta_500_2026-05-04/cases/hs4-recommendation-pod-failure-lq9fmq`

Fault:

`PodFailure` on Hotel Reservation `recommendation`.

Evidence:

- `conclusion.parquet` contains a strong row for `HTTP /recommendations`.
- The endpoint success rate drops from `1.0` to `0.0011655011655011655`.
- Raw trace confirms `frontend::HTTP /recommendations` has 858 abnormal spans,
  857 of which return HTTP 500.
- The graph alarm component is `span|frontend::HTTP /recommendations`.

Bad output:

- `candidate_strong_alarm_count=0`.
- The alarm strength reason is `conclusion_row_unavailable`.
- The export treats the alarm as unknown even though the conclusion row is
  present and strong.

Root cause:

The conclusion key is a bare span/endpoint name:

```text
HTTP /recommendations
```

The graph/candidate key is a normalized component id:

```text
span|frontend::HTTP /recommendations
```

The current matching logic does not canonicalize these two names to the same
alarm identity.

### Example B: three alarm scopes are mixed together

Case:

`/home/ddq/AoyangSpace/dataset/dataset_v3_beta_500_2026-05-04/cases/ts5-ts-travel-service-exception-mzjt7x`

Fault:

`JVMException` in `ts-travel-service`, method
`TravelController.getTripAllDetailInfo`.

Evidence:

- The injected `trip_detail` path fails strongly: abnormal `136` calls, `134`
  errors.
- Two strong user-facing conclusion alarms are present:
  - `preserveservice/preserve`: success rate `1.0 -> 0.0188679`.
  - `travelplanservice/travelPlan/minStation`: success rate `1.0 -> 0.464286`.

Bad output:

- Candidate alarms: `13`.
- Explained alarms: `5`.
- `causal_graph.alarm_nodes`: `4`.

This makes three different alarm sets appear in one result without a clear
contract. A consumer cannot tell whether `causal_graph.alarm_nodes` means all
candidate alarms, all explained alarms, or only terminal alarms rendered in the
subgraph.

Root cause:

The pipeline has multiple legitimate scopes, but the export does not preserve
those scopes explicitly:

- candidate alarm surface: broad set of impacted nodes considered by reasoning;
- explained alarm set: candidates that have accepted root-to-alarm paths;
- path-terminal graph alarms: terminal nodes included in the visual causal graph.

Using the same name, or nearly the same name, for these sets creates an apparent
inconsistency.

## Problem 1: strong alarm lookup is not canonicalized

### Current behavior

The reasoning export attempts to attach conclusion evidence to candidate graph
components. If the exact key lookup fails, it emits:

```json
{
  "issue_strength": "unknown",
  "issue_strength_reason": "conclusion_row_unavailable",
  "has_issues": false
}
```

This is wrong when the conclusion row exists under an equivalent bare endpoint
name.

### Required behavior

A graph component and a conclusion row must be matched through a canonical alarm
identity, not by raw string equality.

Canonicalization should handle at least these forms:

| Source form | Example | Canonical fields |
|---|---|---|
| Graph component | `span|frontend::HTTP /recommendations` | kind=`span`, service=`frontend`, operation=`HTTP /recommendations` |
| Conclusion bare span | `HTTP /recommendations` | operation=`HTTP /recommendations` |
| Conclusion full URL | `HTTP GET http://ts-ui-dashboard:8080/api/v1/foodservice/foods/...` | method=`GET`, host/service hint=`ts-ui-dashboard`, path=`/api/v1/foodservice/foods/...` |
| Trace span name | `GET /api/v1/foodservice/foods/{date}/...` | method=`GET`, path template=`/api/v1/foodservice/foods/{date}/...` |
| Graph malformed endpoint | `span|ts-ui-dashboard:: /api/v1/foodservice/...` | service=`ts-ui-dashboard`, path=`/api/v1/foodservice/...`, method may be unknown |

### Matching order

Use deterministic, explainable matching instead of a single exact lookup:

1. Exact component id match, if conclusion already stores canonical ids.
2. Exact `(service, operation)` match after stripping `span|` and splitting
   `service::operation`.
3. Exact operation/span-name match for unique bare names in the case.
4. HTTP endpoint match by `(service_or_host, method, normalized_path)`.
5. HTTP endpoint match by `(method, normalized_path)` when unique.
6. Bare path match when unique and method is missing from one side.
7. If multiple rows match, mark `ambiguous_conclusion_match` and do not promote
   to strong unless one row is clearly selected by service/host.

Every match should export its method:

```json
{
  "conclusion_match": {
    "status": "matched",
    "method": "bare_operation_unique",
    "conclusion_span_name": "HTTP /recommendations"
  }
}
```

If no row matches, export the failed lookup keys:

```json
{
  "conclusion_match": {
    "status": "unmatched",
    "attempted_keys": [
      "span|frontend::HTTP /recommendations",
      "frontend::HTTP /recommendations",
      "HTTP /recommendations"
    ]
  }
}
```

### Strong alarm definition

A candidate alarm is strong when its matched conclusion row satisfies one of the
configured detector rules. Initial rule for export consistency:

- `Issues != {}`; or
- success-rate drop crosses the existing alarm threshold; or
- duration anomaly crosses the existing alarm threshold.

For the `hs4-recommendation-pod-failure-lq9fmq` regression case,
`span|frontend::HTTP /recommendations` must be matched to `HTTP /recommendations`
and counted as strong.

## Problem 2: alarm scope is ambiguous across result and graph exports

### Current behavior

Different fields expose different scopes:

- `result.json.alarm_nodes`: broad candidate alarms.
- `causal_graph.candidate_alarm_count`: candidate count.
- `causal_graph.explained_alarm_count`: accepted explained count.
- `causal_graph.alarm_nodes`: currently closer to path-terminal graph alarms.

Because the names are not explicit, outputs such as candidate `13`, explained
`5`, graph alarms `4` look like internal inconsistency rather than expected
stage-by-stage narrowing.

### Required schema contract

Export alarm scopes as separate, lossless sets:

```json
{
  "candidate_alarm_nodes": [
    {
      "node_id": 0,
      "component": "span|...",
      "issue_strength": "strong|weak|none|unknown",
      "conclusion_match": {
        "status": "matched|unmatched|ambiguous",
        "method": "exact_component|service_operation|bare_operation_unique|http_endpoint|none"
      }
    }
  ],
  "explained_alarm_nodes": [
    {
      "node_id": 0,
      "component": "span|...",
      "path_ids": ["path-0"]
    }
  ],
  "unexplained_alarm_nodes": [
    {
      "node_id": 0,
      "component": "span|...",
      "issue_strength": "strong|weak|none|unknown",
      "drop_reason": "no_path_found|weak_noise|filtered_by_rank|max_hops|schema_unmatched|ambiguous_conclusion_match|unknown"
    }
  ],
  "path_terminal_alarm_nodes": [
    {
      "node_id": 0,
      "component": "span|..."
    }
  ]
}
```

Keep `alarm_nodes` only as a backward-compatible alias if necessary, and add:

```json
{
  "alarm_nodes_scope": "path_terminal_alarm_nodes"
}
```

### Invariants

The export should satisfy these set invariants:

```text
candidate_alarm_nodes = explained_alarm_nodes union unexplained_alarm_nodes
explained_alarm_nodes = path_terminal_alarm_nodes union non_terminal_explained_alarm_nodes
path_terminal_alarm_nodes subset explained_alarm_nodes
```

When an invariant is intentionally violated, the export must include a reason,
for example `dropped_before_path_search` or `node_not_in_rendered_graph`.

### Count fields

Count fields should be derived from the exported sets, not independently written:

```json
{
  "candidate_alarm_count": 13,
  "explained_alarm_count": 5,
  "unexplained_alarm_count": 8,
  "path_terminal_alarm_count": 4,
  "candidate_strong_alarm_count": 2,
  "explained_strong_alarm_count": 2,
  "strong_alarm_coverage": 1.0
}
```

This makes the `ts5-ts-travel-service-exception-mzjt7x` output understandable:
13 candidates, 5 explained candidates, and 4 terminal alarms can be valid only if
all three scopes are named and linked.

## Implementation plan

### Step 1: introduce an alarm identity model

Add a small normalization layer before alarm strength lookup and graph export.
It should parse both conclusion rows and graph components into a common shape:

```python
@dataclass(frozen=True)
class AlarmIdentity:
    raw: str
    kind: str | None
    service: str | None
    operation: str | None
    http_method: str | None
    http_path: str | None
    host: str | None
```

Normalization rules:

- Strip graph prefixes such as `span|`, `service|`, `container|`.
- Split `service::operation` when present.
- Normalize duplicate whitespace and leading spaces.
- Parse HTTP method/path from both `HTTP GET http://host/path` and `GET /path`.
- Treat `HTTP /path` as path-only with method unknown.
- Preserve the raw string for debugging.

### Step 2: build a conclusion index

Build multiple indexes from `conclusion.parquet` rows:

- by exact raw `SpanName`;
- by canonical operation;
- by `(service_hint, operation)` when service can be inferred from host;
- by `(method, normalized_path)`;
- by `normalized_path` for unique path-only fallback.

The index must return one of three statuses: `matched`, `unmatched`, or
`ambiguous`.

### Step 3: attach conclusion evidence to every candidate alarm

For every candidate alarm, export:

- canonical identity;
- conclusion match status and method;
- matched conclusion row key if present;
- `issue_strength` and `has_issues` derived from the matched row;
- fallback reason if unmatched.

Do not emit `conclusion_row_unavailable` until all canonical lookup strategies
have failed.

### Step 4: split alarm scopes in the schema

Refactor export construction so each stage writes a distinct field:

- detector/E-stage writes `candidate_alarm_nodes`;
- path finder/M-stage writes `explained_alarm_nodes` and
  `unexplained_alarm_nodes`;
- rendered graph writer writes `path_terminal_alarm_nodes`;
- compatibility layer may write `alarm_nodes` plus `alarm_nodes_scope`.

### Step 5: add regression checks

Add fixture-level checks for the audited bad cases:

- `hs4-recommendation-pod-failure-lq9fmq`:
  - `span|frontend::HTTP /recommendations` matches conclusion row
    `HTTP /recommendations`;
  - `candidate_strong_alarm_count >= 1`;
  - issue strength for `/recommendations` is `strong`.
- `ts5-ts-travel-service-exception-mzjt7x`:
  - exported candidate count is `13`;
  - explained count is `5`;
  - path-terminal count is `4`;
  - the three sets are separately exported and set-linked;
  - no consumer has to infer graph alarm scope from the field name alone.

## Acceptance criteria

- Strong conclusion rows are not lost because of `span|service::` prefixes,
  missing HTTP methods, full URL vs path-only representation, or harmless leading
  spaces.
- `conclusion_row_unavailable` means the row is truly unavailable after canonical
  matching, not merely unavailable by exact string key.
- `candidate_strong_alarm_count` is derived from matched conclusion evidence.
- `candidate_alarm_nodes`, `explained_alarm_nodes`, `unexplained_alarm_nodes`,
  and `path_terminal_alarm_nodes` are all explicit and auditable.
- `causal_graph.alarm_nodes` is either removed or explicitly documented with
  `alarm_nodes_scope`.
- Existing labels such as `full causal chain` are not emitted unless all strong
  candidate alarms are either explained or explicitly exported as
  `strong_unexplained` with concrete reasons.

## Expected impact

- The `/recommendations` PodFailure case should no longer report
  `candidate_strong_alarm_count=0`.
- Cases with candidate/explained/graph count differences should become
  explainable instead of appearing internally inconsistent.
- Downstream evaluators can compute alarm recall, strong-alarm coverage, and weak
  noise filtering from one export without reverse-engineering field meanings.
