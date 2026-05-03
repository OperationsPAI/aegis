"""Unit tests for evaluation v2 — by example.

Each test case below shows an agent output, the GT it was compared against,
and the resulting score so the contract reads top-to-bottom. The final test
shows how a batch of mixed-quality outputs aggregates via the processer's
`calculate_metrics`.
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.sdk.evaluation.causal_graph import CausalGraph
from rcabench_platform.v3.sdk.evaluation.v2 import (
    AgentRCAOutput,
    Evidence,
    EvidenceKind,
    EvidenceStatus,
    FaultKind,
    GTFault,
    MatchStatus,
    compute_graph_metrics,
    compute_outcome,
    evaluate_v2,
    extract_gt_faults,
    map_chaos_type,
    verify_evidence,
)

# ──────────────────────────────────────────────────────────────────────
# Fault-kind controlled vocabulary
# ──────────────────────────────────────────────────────────────────────


def test_map_chaos_type_known() -> None:
    assert map_chaos_type("NetworkDelay") is FaultKind.NETWORK_DELAY
    assert map_chaos_type("PodFailure") is FaultKind.POD_FAILURE
    assert map_chaos_type("JVMRuntimeMutator") is FaultKind.JVM_MUTATOR
    assert map_chaos_type("HTTPResponseReplaceCode") is FaultKind.HTTP_REPLACE
    assert map_chaos_type("DNSChaos") is FaultKind.DNS


def test_map_chaos_type_unknown() -> None:
    assert map_chaos_type(None) is FaultKind.UNKNOWN
    assert map_chaos_type("UnseenChaos") is FaultKind.UNKNOWN


# ──────────────────────────────────────────────────────────────────────
# GT fault extraction (new + old format)
# ──────────────────────────────────────────────────────────────────────


def test_extract_gt_new_format_hybrid() -> None:
    inj = {
        "engine_config": [
            {"app": "shipping", "chaos_type": "NetworkDelay", "target_service": "quote", "direction": "to"},
            {"app": "payment", "chaos_type": "CPUStress"},
        ],
        "start_time": "2026-05-02T08:00:00Z",
        "end_time": "2026-05-02T08:05:00Z",
    }
    ctx = extract_gt_faults(inj)
    assert len(ctx.faults) == 2

    f0 = ctx.faults[0]
    assert f0.service == "shipping"
    assert f0.fault_kind is FaultKind.NETWORK_DELAY
    assert f0.direction_src == "shipping" and f0.direction_dst == "quote"

    f1 = ctx.faults[1]
    assert f1.service == "payment" and f1.fault_kind is FaultKind.CPU_STRESS

    assert ctx.start_time_ns and ctx.end_time_ns
    assert ctx.end_time_ns - ctx.start_time_ns == 5 * 60 * 1_000_000_000


def test_extract_gt_jvm_method() -> None:
    inj = {
        "engine_config": [
            {
                "app": "ts-basic-service",
                "chaos_type": "JVMRuntimeMutator",
                "class": "com.foo.BasicController",
                "method": "queryForX",
            }
        ]
    }
    ctx = extract_gt_faults(inj)
    assert ctx.faults[0].method == "com.foo.BasicController.queryForX"


def test_extract_gt_old_format_falls_back() -> None:
    """Old-format injection.json has engine_config as an opaque JSON-encoded
    string and a numeric fault_type; only `ground_truth` is reliable."""
    inj = {
        "engine_config": '{"some":"opaque","tree":1}',
        "fault_type": 27,
        "ground_truth": {
            "service": ["ts-cancel-service"],
            "function": ["fdse.cancel.CancelImpl.cancelFromOrder"],
        },
    }
    ctx = extract_gt_faults(inj, case_name="<no-side-channel>")
    assert len(ctx.faults) == 1
    f = ctx.faults[0]
    assert f.service == "ts-cancel-service"
    assert f.method == "fdse.cancel.CancelImpl.cancelFromOrder"
    assert f.fault_kind is FaultKind.UNKNOWN  # numeric fault_type, no data.jsonl


# ──────────────────────────────────────────────────────────────────────
# Type-aware matcher — by example
# ──────────────────────────────────────────────────────────────────────

_DUMMY_EV: dict[str, str] = {
    "kind": "metric",
    "sql": "SELECT 1 FROM read_parquet('m.parquet')",
    "claim": "x",
}


def _agent(rcs: list[dict[str, Any]], propagation: list[dict[str, Any]] | None = None) -> AgentRCAOutput:
    return AgentRCAOutput.model_validate({"root_causes": rcs, "propagation": propagation or []})


def test_match_perfect_single_fault() -> None:
    """Agent: 1 root_cause, kind+service correct → HIT, F1=1, case_correct=True."""
    gt = [GTFault(service="ts-basic-service", fault_kind=FaultKind.JVM_MUTATOR)]
    agent = _agent(
        [
            {
                "service": "ts-basic-service",
                "fault_kind": "jvm_mutator",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.root_cause_f1 == 1.0
    assert out.case_correct is True
    assert out.per_fault[0].status is MatchStatus.HIT


def test_match_network_no_direction_is_wrong_direction() -> None:
    """Agent gets service+kind on Network* but skips direction → WRONG_DIRECTION."""
    gt = [
        GTFault(service="shipping", fault_kind=FaultKind.NETWORK_DELAY, direction_src="shipping", direction_dst="quote")
    ]
    agent = _agent(
        [
            {
                "service": "shipping",
                "fault_kind": "network_delay",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.per_fault[0].status is MatchStatus.WRONG_DIRECTION
    assert out.root_cause_f1 == 0.0


def test_match_network_correct_direction() -> None:
    gt = [
        GTFault(service="shipping", fault_kind=FaultKind.NETWORK_DELAY, direction_src="shipping", direction_dst="quote")
    ]
    agent = _agent(
        [
            {
                "service": "shipping",
                "fault_kind": "network_delay",
                "direction": {"src": "shipping", "dst": "quote"},
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.per_fault[0].status is MatchStatus.HIT
    assert out.root_cause_f1 == 1.0


def test_match_wrong_kind() -> None:
    gt = [GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS)]
    agent = _agent(
        [
            {
                "service": "payment",
                "fault_kind": "mem_stress",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.per_fault[0].status is MatchStatus.WRONG_KIND
    assert out.root_cause_f1 == 0.0


def test_match_partial_hybrid() -> None:
    """Hybrid GT (2 faults). Agent gets one right + an unrelated overclaim → F1=0.5."""
    gt = [
        GTFault(
            service="shipping", fault_kind=FaultKind.NETWORK_DELAY, direction_src="shipping", direction_dst="quote"
        ),
        GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS),
    ]
    agent = _agent(
        [
            {
                "service": "shipping",
                "fault_kind": "network_delay",
                "direction": {"src": "shipping", "dst": "quote"},
                "evidence": [_DUMMY_EV],
            },
            {"service": "noise", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.root_cause_precision == 0.5
    assert out.root_cause_recall == 0.5
    assert out.root_cause_f1 == 0.5
    assert out.overclaim_rate == 0.5
    assert out.case_correct is False
    statuses = sorted(r.status.value for r in out.per_fault)
    assert statuses == ["HIT", "MISS"]


def test_match_overclaim_drops_case_correct() -> None:
    """Agent finds the GT fault but adds an unrelated extra → case_correct=False."""
    gt = [GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS)]
    agent = _agent(
        [
            {"service": "payment", "fault_kind": "cpu_stress", "evidence": [_DUMMY_EV]},
            {"service": "noise", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.root_cause_recall == 1.0
    assert out.root_cause_precision == 0.5
    assert out.case_correct is False
    assert out.overclaim_rate == 0.5


def test_match_normalization_is_uniform_across_systems() -> None:
    """Service name normalization is the same across ts/hs/otel-demo: lowercase
    + drop dashes and underscores. No system-specific prefix stripping.

    The agent must use names that exist in the data (modulo case + dash/underscore
    style). It does NOT get matching for free by dropping a `ts-` prefix that
    the GT actually carries.
    """
    # Same name, different dash/underscore styling → still HIT.
    gt = [GTFault(service="ts-route-plan-service", fault_kind=FaultKind.POD_FAILURE)]
    agent = _agent(
        [
            {
                "service": "ts_route_plan_service",
                "fault_kind": "pod_failure",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    assert compute_outcome(agent, gt).per_fault[0].status is MatchStatus.HIT

    # Mixed case → still HIT.
    agent_caps = _agent(
        [
            {
                "service": "TS-Route-Plan-Service",
                "fault_kind": "pod_failure",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    assert compute_outcome(agent_caps, gt).per_fault[0].status is MatchStatus.HIT

    # Dropping the ts- prefix is now a MISS (the agent must use a name
    # that actually appears in the case data).
    agent_stripped = _agent(
        [
            {
                "service": "route-plan-service",
                "fault_kind": "pod_failure",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    assert compute_outcome(agent_stripped, gt).per_fault[0].status is MatchStatus.MISS


def test_match_service_only_f1_separates_from_kind_f1() -> None:
    """service_f1 counts a fault as matched whenever the agent picked the right
    service, even if it got the kind wrong. root_cause_f1 (kind-level) only
    counts HIT. Both share denominators, so on a 1-fault case where the agent
    nailed the service but missed the kind, service_f1=1.0 and root_cause_f1=0.0.
    """
    gt = [GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS)]
    agent = _agent(
        [
            {
                "service": "payment",
                "fault_kind": "mem_stress",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.service_f1 == 1.0
    assert out.root_cause_f1 == 0.0
    assert out.per_fault[0].status is MatchStatus.WRONG_KIND


# ──────────────────────────────────────────────────────────────────────
# Graph metrics (agent's claimed graph vs GT causal_graph)
# ──────────────────────────────────────────────────────────────────────


def _gt_graph(nodes: list[str], edges: list[tuple[str, str]]) -> CausalGraph:
    return CausalGraph.from_dict(
        {
            "nodes": [{"component": n} for n in nodes],
            "edges": [{"source": s, "target": t} for s, t in edges],
            "component_to_service": {n: n for n in nodes},
        }
    )


def test_graph_metrics_perfect() -> None:
    gt = _gt_graph(["a", "b", "c"], [("a", "b"), ("b", "c")])
    agent = _agent(
        [{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[
            {"from": "a", "to": "b", "evidence": [_DUMMY_EV]},
            {"from": "b", "to": "c", "evidence": [_DUMMY_EV]},
        ],
    )
    gm = compute_graph_metrics(agent, gt)
    assert gm.node_f1 == 1.0
    assert gm.edge_f1 == 1.0


def test_graph_metrics_partial_recall_and_hallucination() -> None:
    """Agent claims one correct edge + one hallucinated edge; misses 2 GT edges."""
    gt = _gt_graph(["a", "b", "c", "d"], [("a", "b"), ("b", "c"), ("c", "d")])
    agent = _agent(
        [{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[
            {"from": "a", "to": "b", "evidence": [_DUMMY_EV]},  # in GT
            {"from": "a", "to": "c", "evidence": [_DUMMY_EV]},  # hallucinated
        ],
    )
    gm = compute_graph_metrics(agent, gt)
    assert gm.node_precision == 1.0  # {a,b,c} ⊆ {a,b,c,d}
    assert abs(gm.node_recall - 0.75) < 1e-9
    assert gm.edge_precision == 0.5
    assert abs(gm.edge_recall - 1 / 3) < 1e-9
    assert gm.hallucinated_edges == [("a", "c")]


def test_graph_metrics_no_gt_marks_inapplicable() -> None:
    agent = _agent([{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}])
    gm = compute_graph_metrics(agent, None)
    assert gm.applicable is False
    assert gm.node_f1 == 0.0


# ──────────────────────────────────────────────────────────────────────
# DuckDB SQL evidence verification
# ──────────────────────────────────────────────────────────────────────


def _make_case(tmp_path: Path) -> Path:
    """Synthesize a tiny case dir with one trace + one metrics parquet."""
    times = pl.datetime_range(
        pl.datetime(2026, 5, 2, 8, 0, 0),
        pl.datetime(2026, 5, 2, 8, 4, 0),
        "1m",
        eager=True,
    )
    pl.DataFrame(
        {
            "time": times,
            "metric": ["latency_p99"] * len(times),
            "value": [10.0, 20.0, 30.0, 40.0, 50.0],
            "service_name": ["shipping"] * len(times),
        }
    ).write_parquet(tmp_path / "abnormal_metrics.parquet")
    pl.DataFrame(
        {
            "time": times,
            "trace_id": ["t"] * len(times),
            "span_id": [str(i) for i in range(len(times))],
            "service_name": ["shipping"] * len(times),
            "duration": [1000, 2000, 3000, 4000, 5000],
        }
    ).write_parquet(tmp_path / "abnormal_traces.parquet")
    return tmp_path


def test_sql_verify_ok_via_view_name(tmp_path: Path) -> None:
    """Each *.parquet in the case dir is mounted as a same-named view, so the
    agent can use bare table names without an explicit read_parquet(...)."""
    case = _make_case(tmp_path)
    ev = Evidence(
        kind=EvidenceKind.METRIC,
        sql="SELECT * FROM abnormal_metrics WHERE service_name='shipping'",
        claim="shipping latency rises",
    )
    r = verify_evidence(ev, parquet_dir=case)
    assert r.status is EvidenceStatus.OK
    assert r.row_count == 5


def test_sql_verify_ok_via_read_parquet(tmp_path: Path) -> None:
    """Relative read_parquet('foo.parquet') paths resolve against the case dir
    because the verifier chdirs into it before executing."""
    case = _make_case(tmp_path)
    ev = Evidence(
        kind=EvidenceKind.METRIC,
        sql="SELECT * FROM read_parquet('abnormal_metrics.parquet') WHERE service_name='shipping'",
        claim="shipping latency rises",
    )
    r = verify_evidence(ev, parquet_dir=case)
    assert r.status is EvidenceStatus.OK
    assert r.row_count == 5


def test_sql_verify_empty(tmp_path: Path) -> None:
    """Query executes but matches no rows → EMPTY (still distinguishable from
    SQL_ERROR, so the agent sees that the SQL parses but its filter is wrong)."""
    case = _make_case(tmp_path)
    ev = Evidence(
        kind=EvidenceKind.METRIC,
        sql="SELECT * FROM abnormal_metrics WHERE service_name='nonexistent'",
        claim="x",
    )
    assert verify_evidence(ev, parquet_dir=case).status is EvidenceStatus.EMPTY


def test_sql_verify_sql_error_on_bad_syntax(tmp_path: Path) -> None:
    """Anything DuckDB can't run surfaces as SQL_ERROR — no curated allowlist."""
    case = _make_case(tmp_path)
    ev = Evidence(kind=EvidenceKind.METRIC, sql="SELECT FROM WHERE", claim="x")
    assert verify_evidence(ev, parquet_dir=case).status is EvidenceStatus.SQL_ERROR


def test_sql_verify_sql_error_on_missing_table(tmp_path: Path) -> None:
    """Reference to a parquet that isn't in the case dir → DuckDB raises a
    catalog error, surfaced as SQL_ERROR."""
    case = _make_case(tmp_path)
    ev = Evidence(kind=EvidenceKind.METRIC, sql="SELECT * FROM nonexistent_table", claim="x")
    assert verify_evidence(ev, parquet_dir=case).status is EvidenceStatus.SQL_ERROR


# ──────────────────────────────────────────────────────────────────────
# End-to-end evaluator. chain_coherence requires an LLM client; tests
# inject a tiny stub that always returns score=1.0 so the deterministic
# axes (rc_f1, sql_executable_rate) stay decoupled from the judge.
# ──────────────────────────────────────────────────────────────────────


def _injection() -> dict[str, Any]:
    return {
        "engine_config": [
            {
                "app": "shipping",
                "chaos_type": "NetworkDelay",
                "target_service": "quote",
                "direction": "to",
            }
        ],
        "start_time": "2026-05-02T08:00:00Z",
        "end_time": "2026-05-02T08:05:00Z",
    }


class _StubChoiceMessage:
    def __init__(self, content: str) -> None:
        self.content = content


class _StubChoice:
    def __init__(self, content: str) -> None:
        self.message = _StubChoiceMessage(content)


class _StubResponse:
    def __init__(self, content: str) -> None:
        self.choices = [_StubChoice(content)]


class _StubCompletions:
    def __init__(self, content: str) -> None:
        self._content = content

    async def create(self, **_kwargs: Any) -> _StubResponse:
        return _StubResponse(self._content)


class _StubChat:
    def __init__(self, content: str) -> None:
        self.completions = _StubCompletions(content)


class _StubLLMClient:
    """Minimal AsyncOpenAI-compatible stub for the chain judge.

    Returns a fixed JSON payload so deterministic axes can be tested without
    flaky LLM behavior. Pass `score=1.0` for the perfect-case test, etc.
    """

    def __init__(self, score: float = 1.0, reasoning: str = "stub") -> None:
        self.chat = _StubChat(json.dumps({"score": score, "reasoning": reasoning}))


def test_evaluate_v2_perfect(tmp_path: Path) -> None:
    """Perfect agent: rc_f1=1, sql=1, chain=1 (stub), headline=1."""
    case = _make_case(tmp_path)
    agent = json.dumps(
        {
            "root_causes": [
                {
                    "service": "shipping",
                    "fault_kind": "network_delay",
                    "direction": {"src": "shipping", "dst": "quote"},
                    "evidence": [
                        {
                            "kind": "metric",
                            "sql": (
                                "SELECT * FROM read_parquet('abnormal_metrics.parquet') WHERE service_name='shipping'"
                            ),
                            "claim": "shipping latency rises",
                        }
                    ],
                }
            ],
            "propagation": [],
        }
    )
    res = asyncio.run(
        evaluate_v2(
            agent,
            _injection(),
            case,
            gt_graph=None,
            llm_client=_StubLLMClient(score=1.0),  # type: ignore[arg-type]
        )
    )
    assert res.root_cause_f1 == 1.0
    assert res.service_f1 == 1.0
    assert res.overclaim_rate == 0.0
    assert res.sql_executable_rate == 1.0
    assert res.chain_coherence == 1.0
    assert res.headline == 1.0
    assert res.case_correct is True


def test_evaluate_v2_wrong_direction(tmp_path: Path) -> None:
    """Service+kind right but direction flipped → rc_f1=0, headline=0.
    service_f1 stays at 1.0 because the service was correctly identified.
    """
    case = _make_case(tmp_path)
    agent = json.dumps(
        {
            "root_causes": [
                {
                    "service": "shipping",
                    "fault_kind": "network_delay",
                    "direction": {"src": "quote", "dst": "shipping"},  # flipped
                    "evidence": [
                        {
                            "kind": "metric",
                            "sql": (
                                "SELECT * FROM read_parquet('abnormal_metrics.parquet') WHERE service_name='shipping'"
                            ),
                            "claim": "x",
                        }
                    ],
                }
            ],
            "propagation": [],
        }
    )
    res = asyncio.run(
        evaluate_v2(
            agent,
            _injection(),
            case,
            gt_graph=None,
            llm_client=_StubLLMClient(score=0.5),  # type: ignore[arg-type]
        )
    )
    assert res.root_cause_f1 == 0.0
    assert res.service_f1 == 1.0
    assert res.headline == 0.0
    assert res.case_correct is False
    assert res.per_fault[0].status is MatchStatus.WRONG_DIRECTION


def test_evaluate_v2_unparseable_response(tmp_path: Path) -> None:
    """Parse-error path doesn't reach the chain judge, so llm_client is unused."""
    case = _make_case(tmp_path)
    res = asyncio.run(
        evaluate_v2(
            "NOT JSON",
            _injection(),
            case,
            gt_graph=None,
            llm_client=_StubLLMClient(),  # type: ignore[arg-type]
        )
    )
    assert res.headline == 0.0
    assert res.parse_error is not None and "JSON" in res.parse_error


def test_evaluate_v2_requires_llm_client(tmp_path: Path) -> None:
    """chain_coherence has no fallback now: passing llm_client=None on a
    parseable agent output raises so misconfiguration fails loudly instead of
    silently double-counting sql_executable_rate as the chain score.
    """
    import pytest

    case = _make_case(tmp_path)
    agent = json.dumps(
        {
            "root_causes": [
                {
                    "service": "shipping",
                    "fault_kind": "network_delay",
                    "direction": {"src": "shipping", "dst": "quote"},
                    "evidence": [
                        {
                            "kind": "metric",
                            "sql": (
                                "SELECT * FROM read_parquet('abnormal_metrics.parquet') WHERE service_name='shipping'"
                            ),
                            "claim": "x",
                        }
                    ],
                }
            ],
            "propagation": [],
        }
    )
    with pytest.raises(ValueError, match="chain_coherence requires an LLM client"):
        asyncio.run(evaluate_v2(agent, _injection(), case, gt_graph=None, llm_client=None))


# ──────────────────────────────────────────────────────────────────────
# Batch aggregation via RCABenchProcesser.calculate_metrics
# ──────────────────────────────────────────────────────────────────────


def test_calculate_metrics_aggregation() -> None:
    """4 stub samples → aggregate is the mean over the 3 successfully scored.

    Sample mix:
      [0] perfect    rc_f1=1.0  sql=1.0  chain=1.0  headline=1.0  correct=True
      [1] partial    rc_f1=0.5  sql=0.8  chain=0.6  headline=0.24
      [2] parse-err  zeros + parse_error=True
      [3] eval-err   sample.meta['eval_v2'] = {'error': '...'}  → excluded
    Expected averages over the 3 scored samples (excluding [3]).
    """
    from rcabench_platform.v3.sdk.llm_eval.eval.processer.rcabench import RCABenchProcesser

    class _StubSample:
        def __init__(self, meta: dict[str, Any]) -> None:
            self.meta: dict[str, Any] = meta

    samples = [
        _StubSample(
            {
                "eval_v2": {
                    "service_f1": 1.0,
                    "root_cause_f1": 1.0,
                    "overclaim_rate": 0.0,
                    "sql_executable_rate": 1.0,
                    "chain_coherence": 1.0,
                    "node_f1": 1.0,
                    "edge_f1": 1.0,
                    "headline": 1.0,
                    "case_correct": True,
                    "per_evidence": [{}],
                }
            }
        ),
        _StubSample(
            {
                "eval_v2": {
                    "service_f1": 0.7,
                    "root_cause_f1": 0.5,
                    "overclaim_rate": 0.5,
                    "sql_executable_rate": 0.8,
                    "chain_coherence": 0.6,
                    "node_f1": 0.4,
                    "edge_f1": 0.2,
                    "headline": 0.24,
                    "case_correct": False,
                    "per_evidence": [{}],
                }
            }
        ),
        _StubSample(
            {
                "eval_v2": {
                    "service_f1": 0.0,
                    "root_cause_f1": 0.0,
                    "overclaim_rate": 1.0,
                    "sql_executable_rate": 0.0,
                    "chain_coherence": 0.0,
                    "node_f1": 0.0,
                    "edge_f1": 0.0,
                    "headline": 0.0,
                    "case_correct": False,
                    "parse_error": "bad json",
                    "per_evidence": [],
                }
            }
        ),
        _StubSample({"eval_v2": {"error": "missing case dir"}}),
    ]

    proc = RCABenchProcesser.__new__(RCABenchProcesser)
    proc.name = "RCABench"
    metrics = proc.calculate_metrics(samples)  # type: ignore[arg-type]

    assert metrics["total_samples"] == 4
    assert metrics["scored_samples"] == 3
    assert metrics["case_correct"] == 1
    assert metrics["case_correct_rate"] == round(1 / 3, 4)
    assert metrics["parse_errors"] == 1
    assert metrics["zero_evidence_outputs"] == 1
    assert metrics["avg_service_f1"] == round((1.0 + 0.7 + 0.0) / 3, 4)
    assert metrics["avg_root_cause_f1"] == round((1.0 + 0.5 + 0.0) / 3, 4)
    assert metrics["avg_sql_executable_rate"] == round((1.0 + 0.8 + 0.0) / 3, 4)
    assert metrics["avg_chain_coherence"] == round((1.0 + 0.6 + 0.0) / 3, 4)
    assert metrics["avg_node_f1"] == round((1.0 + 0.4 + 0.0) / 3, 4)
    assert metrics["avg_edge_f1"] == round((1.0 + 0.2 + 0.0) / 3, 4)
    assert metrics["avg_headline"] == round((1.0 + 0.24 + 0.0) / 3, 4)
