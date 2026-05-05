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
    compute_path_reachability,
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
    assert map_chaos_type("PodKill") is FaultKind.POD_FAILURE
    assert map_chaos_type("PodFailure") is FaultKind.POD_UNAVAILABLE
    assert map_chaos_type("JVMRuntimeMutator") is FaultKind.JVM_METHOD_MUTATED
    assert map_chaos_type("JVMReturn") is FaultKind.JVM_METHOD_MUTATED
    assert map_chaos_type("JVMLatency") is FaultKind.JVM_METHOD_LATENCY
    assert map_chaos_type("JVMGarbageCollector") is FaultKind.JVM_GC_PRESSURE
    assert map_chaos_type("JVMMySQLException") is FaultKind.JVM_JDBC_EXCEPTION
    assert map_chaos_type("HTTPResponseReplaceCode") is FaultKind.HTTP_RESPONSE_STATUS_MODIFIED
    assert map_chaos_type("HTTPRequestDelay") is FaultKind.HTTP_SLOW
    assert map_chaos_type("HTTPResponsePatchBody") is FaultKind.HTTP_PAYLOAD_MODIFIED
    assert map_chaos_type("NetworkBandwidth") is FaultKind.NETWORK_BANDWIDTH_LIMIT
    assert map_chaos_type("DNSChaos") is FaultKind.DNS_RESOLUTION_FAILED  # alias
    assert map_chaos_type("DNSRandom") is FaultKind.DNS_RESOLUTION_WRONG
    assert map_chaos_type("TimeChaos") is FaultKind.CLOCK_SKEW  # alias


def test_map_chaos_type_unknown() -> None:
    assert map_chaos_type(None) is FaultKind.UNKNOWN
    assert map_chaos_type("UnseenChaos") is FaultKind.UNKNOWN


# ──────────────────────────────────────────────────────────────────────
# GT fault extraction. direction / method are still extracted — the
# matcher no longer scores them, but they're surfaced on FaultMatchResult
# for diagnostic display.
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

    assert ctx.start_time_ns is not None and ctx.end_time_ns is not None
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


def test_extract_gt_old_format_decodes_fault_type_index() -> None:
    """Old-format injection.json carries `fault_type` as a numeric index into
    canonical FAULT_TYPES — we decode it without needing data.jsonl. service
    and direction come from `display_config.injection_point` + direction."""
    inj = {
        "engine_config": '{"some":"opaque","tree":1}',
        "fault_type": 22,  # canonical[22] == "NetworkPartition"
        "display_config": json.dumps(
            {
                "direction": "both",
                "injection_point": {"source_service": "mysql", "target_service": "ts-train-service"},
                "namespace": "ts",
            }
        ),
    }
    ctx = extract_gt_faults(inj)
    assert len(ctx.faults) == 1
    assert ctx.faults[0].fault_kind is FaultKind.NETWORK_PARTITION
    assert ctx.faults[0].service == "mysql"


# ──────────────────────────────────────────────────────────────────────
# Single-tier matcher. Status is HIT / WRONG_KIND / MISS — direction
# and method are not part of the match key under the simplified contract.
# ──────────────────────────────────────────────────────────────────────

_DUMMY_EV: dict[str, str] = {
    "kind": "metric",
    "sql": "SELECT 1 FROM read_parquet('m.parquet')",
    "claim": "x",
}


def _agent(rcs: list[dict[str, Any]], propagation: list[dict[str, Any]] | None = None) -> AgentRCAOutput:
    return AgentRCAOutput.model_validate({"root_causes": rcs, "propagation": propagation or []})


def test_match_perfect_single_fault() -> None:
    gt = [GTFault(service="ts-basic-service", fault_kind=FaultKind.JVM_METHOD_MUTATED)]
    agent = _agent(
        [
            {
                "service": "ts-basic-service",
                "fault_kind": "jvm_method_mutated",
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.f1 == 1.0
    assert out.precision == 1.0
    assert out.recall == 1.0
    assert out.exact_match is True
    assert out.fault_kind_accuracy == 1.0
    assert out.kind_accuracy_denom == 1
    assert out.per_fault[0].status is MatchStatus.HIT


def test_match_wrong_kind() -> None:
    """Service correct but kind wrong → WRONG_KIND, f1=0, but contributes to
    fault_kind_accuracy denominator (0/1 = 0.0)."""
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
    assert out.f1 == 0.0
    assert out.exact_match is False
    assert out.fault_kind_accuracy == 0.0
    assert out.kind_accuracy_denom == 1


def test_match_direction_is_diagnostic_only() -> None:
    """Network fault: agent gives wrong direction → still HIT under the
    simplified contract (matcher only checks service + fault_kind)."""
    gt = [
        GTFault(
            service="shipping",
            fault_kind=FaultKind.NETWORK_DELAY,
            direction_src="shipping",
            direction_dst="quote",
        )
    ]
    agent = _agent(
        [
            {
                "service": "shipping",
                "fault_kind": "network_delay",
                "direction": {"src": "quote", "dst": "shipping"},  # flipped — ignored
                "evidence": [_DUMMY_EV],
            }
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.per_fault[0].status is MatchStatus.HIT
    assert out.f1 == 1.0
    assert out.exact_match is True


def test_match_network_no_direction_still_hits() -> None:
    """Network fault: agent omits direction entirely → HIT (direction unused)."""
    gt = [
        GTFault(
            service="shipping",
            fault_kind=FaultKind.NETWORK_DELAY,
            direction_src="shipping",
            direction_dst="quote",
        )
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
    assert out.per_fault[0].status is MatchStatus.HIT


def test_match_network_either_end_hits() -> None:
    """Network kind: agent may report either direction_src OR direction_dst.

    Rationale: netem rule sits on one side but RTT/drops show on both — from
    spans alone you can't tell which side is patched, so either end counts.
    """
    gt = [
        GTFault(
            service="shipping",
            fault_kind=FaultKind.NETWORK_DELAY,
            direction_src="shipping",
            direction_dst="quote",
        )
    ]
    # Reporting the dst end (quote) — must HIT under the relaxation.
    agent_dst = _agent(
        [{"service": "quote", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]}],
    )
    out = compute_outcome(agent_dst, gt)
    assert out.per_fault[0].status is MatchStatus.HIT
    assert out.f1 == 1.0
    assert out.exact_match is True

    # Reporting the src end (shipping) — also HIT (same as before).
    agent_src = _agent(
        [{"service": "shipping", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]}],
    )
    assert compute_outcome(agent_src, gt).per_fault[0].status is MatchStatus.HIT

    # A third unrelated service — still MISS.
    agent_other = _agent(
        [{"service": "frontend", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]}],
    )
    assert compute_outcome(agent_other, gt).per_fault[0].status is MatchStatus.MISS


def test_match_network_both_ends_overclaims() -> None:
    """Reporting BOTH ends of a single network GT: one HIT + one overclaim.

    Multiset-exact stays strict — ``exact_match`` falls because n_agent > n_gt.
    """
    gt = [
        GTFault(
            service="shipping",
            fault_kind=FaultKind.NETWORK_DELAY,
            direction_src="shipping",
            direction_dst="quote",
        )
    ]
    agent = _agent(
        [
            {"service": "shipping", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]},
            {"service": "quote", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.recall == 1.0
    assert out.precision == 0.5
    assert out.exact_match is False
    assert len(out.overclaim_indices) == 1


def test_match_network_relaxation_does_not_apply_to_http() -> None:
    """HTTP / JVM / Pod / DNS / time stay strict — only the 6 NETWORK_KINDS
    accept either-end matching. A peer-service guess on HTTP is a MISS."""
    gt = [
        GTFault(
            service="shipping",
            fault_kind=FaultKind.HTTP_ABORTED,
            # direction_dst left None — _new_format_faults only fills it for
            # NETWORK_KINDS, but we set service-level both fields here just to
            # prove the matcher is gated by fault_kind, not by what's filled.
            direction_src="shipping",
            direction_dst="quote",
        )
    ]
    agent = _agent(
        [{"service": "quote", "fault_kind": "http_aborted", "evidence": [_DUMMY_EV]}],
    )
    assert compute_outcome(agent, gt).per_fault[0].status is MatchStatus.MISS


def test_match_network_multi_fault_still_requires_full_recall() -> None:
    """Two network GTs: agent must hit both (on either end of each) for exact_match.

    Greedy 1-1 means the same agent_rc can't satisfy two GTs even when its
    service appears in both.
    """
    gt = [
        GTFault(
            service="shipping",
            fault_kind=FaultKind.NETWORK_DELAY,
            direction_src="shipping",
            direction_dst="quote",
        ),
        GTFault(
            service="payment",
            fault_kind=FaultKind.NETWORK_LOSS,
            direction_src="payment",
            direction_dst="quote",
        ),
    ]
    # Agent reports quote twice with the right kinds — each consumes one GT.
    agent = _agent(
        [
            {"service": "quote", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]},
            {"service": "quote", "fault_kind": "network_loss", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.recall == 1.0
    assert out.precision == 1.0
    assert out.exact_match is True

    # Single agent_rc on `quote` cannot cover both — only one GT pairs.
    agent_one = _agent(
        [{"service": "quote", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]}],
    )
    out_one = compute_outcome(agent_one, gt)
    assert out_one.recall == 0.5
    assert out_one.exact_match is False


def test_match_partial_hybrid() -> None:
    """Hybrid GT (2 faults). Agent gets one right + an unrelated overclaim."""
    gt = [
        GTFault(service="shipping", fault_kind=FaultKind.NETWORK_DELAY),
        GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS),
    ]
    agent = _agent(
        [
            {"service": "shipping", "fault_kind": "network_delay", "evidence": [_DUMMY_EV]},
            {"service": "noise", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.precision == 0.5
    assert out.recall == 0.5
    assert out.f1 == 0.5
    assert out.exact_match is False
    statuses = sorted(r.status.value for r in out.per_fault)
    assert statuses == ["HIT", "MISS"]
    # Overclaim's service is wrong, so it doesn't contribute to kind_accuracy denom.
    assert out.kind_accuracy_denom == 1


def test_match_overclaim_drops_exact_match() -> None:
    """Agent finds the GT fault but adds an unrelated extra → exact_match=False."""
    gt = [GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS)]
    agent = _agent(
        [
            {"service": "payment", "fault_kind": "cpu_stress", "evidence": [_DUMMY_EV]},
            {"service": "noise", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.recall == 1.0
    assert out.precision == 0.5
    assert out.exact_match is False
    assert out.overclaim_indices == [1]


def test_match_multiset_two_same_service() -> None:
    """Two GT faults on the same service → agent must HIT both to exact_match."""
    gt = [
        GTFault(service="cart", fault_kind=FaultKind.CPU_STRESS),
        GTFault(service="cart", fault_kind=FaultKind.CPU_STRESS),
    ]
    agent_two = _agent(
        [
            {"service": "cart", "fault_kind": "cpu_stress", "evidence": [_DUMMY_EV]},
            {"service": "cart", "fault_kind": "cpu_stress", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent_two, gt)
    assert out.f1 == 1.0
    assert out.exact_match is True

    agent_one = _agent(
        [{"service": "cart", "fault_kind": "cpu_stress", "evidence": [_DUMMY_EV]}],
    )
    out = compute_outcome(agent_one, gt)
    assert out.recall == 0.5
    assert out.exact_match is False  # one short of multiset


def test_match_kind_accuracy_denom_excludes_pure_misses() -> None:
    """Agent rcs whose service doesn't match anyone don't count toward
    fault_kind_accuracy denom (they're hallucinations, not service-correct)."""
    gt = [GTFault(service="payment", fault_kind=FaultKind.CPU_STRESS)]
    agent = _agent(
        [
            {"service": "noise", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
            {"service": "elsewhere", "fault_kind": "mem_stress", "evidence": [_DUMMY_EV]},
        ]
    )
    out = compute_outcome(agent, gt)
    assert out.kind_accuracy_denom == 0
    # None signals "no service-correct rcs to grade"; aggregator skips this case.
    assert out.fault_kind_accuracy is None


def test_match_normalization_is_uniform_across_systems() -> None:
    """Service name normalization is uniform: lowercase + drop dashes / underscores."""
    gt = [GTFault(service="ts-route-plan-service", fault_kind=FaultKind.POD_FAILURE)]
    agent = _agent(
        [{"service": "ts_route_plan_service", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
    )
    assert compute_outcome(agent, gt).per_fault[0].status is MatchStatus.HIT

    agent_caps = _agent(
        [{"service": "TS-Route-Plan-Service", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
    )
    assert compute_outcome(agent_caps, gt).per_fault[0].status is MatchStatus.HIT

    agent_stripped = _agent(
        [{"service": "route-plan-service", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
    )
    assert compute_outcome(agent_stripped, gt).per_fault[0].status is MatchStatus.MISS


# ──────────────────────────────────────────────────────────────────────
# Graph metrics (agent's claimed graph vs GT causal_graph)
# ──────────────────────────────────────────────────────────────────────


def _gt_graph(
    nodes: list[str],
    edges: list[tuple[str, str]],
    alarms: list[str] | None = None,
) -> CausalGraph:
    alarms = alarms or []
    return CausalGraph.from_dict(
        {
            "nodes": [{"component": n} for n in nodes],
            "edges": [{"source": s, "target": t} for s, t in edges],
            "alarm_nodes": [{"component": a} for a in alarms],
            "component_to_service": {n: n for n in nodes + alarms},
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
    assert gm.node_precision == 1.0
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
# Path reachability (HIT root cause -> alarm service via agent edges)
# ──────────────────────────────────────────────────────────────────────


def test_path_reachability_hit_chains_to_alarm() -> None:
    """HIT rc on `a`, agent edges a->b->c, alarm on `c` → reachable."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["a", "b", "c"], [("a", "b"), ("b", "c")], alarms=["c"])
    agent = _agent(
        [{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[
            {"from": "a", "to": "b", "evidence": [_DUMMY_EV]},
            {"from": "b", "to": "c", "evidence": [_DUMMY_EV]},
        ],
    )
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is True


def test_path_reachability_hit_is_alarm_zero_length() -> None:
    """HIT rc service is itself the alarm service (path length 0) → reachable."""
    gt_faults = [GTFault(service="frontend", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["frontend"], [], alarms=["frontend"])
    agent = _agent([{"service": "frontend", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}])
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is True


def test_path_reachability_no_hit_returns_false() -> None:
    """Agent never identifies a GT fault correctly → False even if propagation
    happens to overlap GT edges. This is the anti-trivial-pass guarantee."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["a", "b", "c"], [("a", "b"), ("b", "c")], alarms=["c"])
    agent = _agent(
        [{"service": "x", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[
            {"from": "a", "to": "b", "evidence": [_DUMMY_EV]},
            {"from": "b", "to": "c", "evidence": [_DUMMY_EV]},
        ],
    )
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is False


def test_path_reachability_hit_but_disconnected() -> None:
    """HIT rc on `a` but agent's edges don't lead to alarm `c`."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["a", "b", "c"], [("a", "b"), ("b", "c")], alarms=["c"])
    agent = _agent(
        [{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[{"from": "a", "to": "b", "evidence": [_DUMMY_EV]}],
    )
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is False


def test_path_reachability_one_of_many_hits_reaches_alarm() -> None:
    """Two HITs; only one walks to an alarm. At-least-one semantics → True."""
    gt_faults = [
        GTFault(service="a", fault_kind=FaultKind.POD_FAILURE),
        GTFault(service="z", fault_kind=FaultKind.POD_FAILURE),
    ]
    gt = _gt_graph(["a", "b", "c", "z"], [("a", "b"), ("b", "c")], alarms=["c"])
    agent = _agent(
        [
            {"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
            {"service": "z", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]},
        ],
        propagation=[
            {"from": "a", "to": "b", "evidence": [_DUMMY_EV]},
            {"from": "b", "to": "c", "evidence": [_DUMMY_EV]},
        ],
    )
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is True


def test_path_reachability_normalization() -> None:
    """Service names normalize across `_` / `-` / case (matches matcher rules)."""
    gt_faults = [GTFault(service="cart-svc", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(
        ["cart-svc", "checkout_svc", "Frontend"],
        [("cart-svc", "checkout_svc"), ("checkout_svc", "Frontend")],
        alarms=["Frontend"],
    )
    agent = _agent(
        [{"service": "CartSvc", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[
            {"from": "cart_svc", "to": "checkout-svc", "evidence": [_DUMMY_EV]},
            {"from": "checkoutsvc", "to": "frontend", "evidence": [_DUMMY_EV]},
        ],
    )
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is True


def test_path_reachability_no_gt_graph_is_none() -> None:
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    agent = _agent([{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}])
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, None) is None


def test_path_reachability_no_alarm_services_is_none() -> None:
    """GT graph exists but declares no alarms → metric not gradeable, return None."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["a", "b"], [("a", "b")], alarms=[])
    agent = _agent([{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}])
    outcome = compute_outcome(agent, gt_faults)
    assert compute_path_reachability(agent, outcome, gt) is None


# ──────────────────────────────────────────────────────────────────────
# any_root_cause_hit — supplement to path_reachability
#
# Per-case invariant: any_root_cause_hit ≥ path_reachability (treating True/False
# as 1/0). path_reachability=True implies a HIT exists; the converse can fail
# when the agent identifies the root cause but its propagation chain doesn't
# reach a GT alarm.
# ──────────────────────────────────────────────────────────────────────


def test_any_root_cause_hit_disconnected_path() -> None:
    """HIT rc but agent edges don't reach the alarm → any_hit=True, path_reach=False."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["a", "b", "c"], [("a", "b"), ("b", "c")], alarms=["c"])
    agent = _agent(
        [{"service": "a", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}],
        propagation=[{"from": "a", "to": "b", "evidence": [_DUMMY_EV]}],
    )
    outcome = compute_outcome(agent, gt_faults)
    any_hit = any(m.status == MatchStatus.HIT for m in outcome.per_fault)
    assert any_hit is True
    assert compute_path_reachability(agent, outcome, gt) is False


def test_any_root_cause_hit_all_miss() -> None:
    """No HITs → any_hit=False (path_reach is also False)."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    gt = _gt_graph(["a", "b", "c"], [("a", "b"), ("b", "c")], alarms=["c"])
    agent = _agent([{"service": "x", "fault_kind": "pod_failure", "evidence": [_DUMMY_EV]}])
    outcome = compute_outcome(agent, gt_faults)
    any_hit = any(m.status == MatchStatus.HIT for m in outcome.per_fault)
    assert any_hit is False
    assert compute_path_reachability(agent, outcome, gt) is False


def test_any_root_cause_hit_wrong_kind_does_not_count() -> None:
    """Service-correct but wrong fault_kind is WRONG_KIND, not HIT → any_hit=False."""
    gt_faults = [GTFault(service="a", fault_kind=FaultKind.POD_FAILURE)]
    agent = _agent([{"service": "a", "fault_kind": "cpu_stress", "evidence": [_DUMMY_EV]}])
    outcome = compute_outcome(agent, gt_faults)
    any_hit = any(m.status == MatchStatus.HIT for m in outcome.per_fault)
    assert any_hit is False


# ──────────────────────────────────────────────────────────────────────
# DuckDB SQL evidence verification
# ──────────────────────────────────────────────────────────────────────


def _make_case(tmp_path: Path) -> Path:
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
    case = _make_case(tmp_path)
    ev = Evidence(
        kind=EvidenceKind.METRIC,
        sql="SELECT * FROM abnormal_metrics WHERE service_name='nonexistent'",
        claim="x",
    )
    assert verify_evidence(ev, parquet_dir=case).status is EvidenceStatus.EMPTY


def test_sql_verify_sql_error_on_bad_syntax(tmp_path: Path) -> None:
    case = _make_case(tmp_path)
    ev = Evidence(kind=EvidenceKind.METRIC, sql="SELECT FROM WHERE", claim="x")
    assert verify_evidence(ev, parquet_dir=case).status is EvidenceStatus.SQL_ERROR


def test_sql_verify_sql_error_on_missing_table(tmp_path: Path) -> None:
    case = _make_case(tmp_path)
    ev = Evidence(kind=EvidenceKind.METRIC, sql="SELECT * FROM nonexistent_table", claim="x")
    assert verify_evidence(ev, parquet_dir=case).status is EvidenceStatus.SQL_ERROR


# ──────────────────────────────────────────────────────────────────────
# End-to-end evaluator. Per-evidence judge requires an LLM client; tests
# inject stubs that return fixed payloads so deterministic axes (f1,
# sql_executable_rate) stay decoupled from judge behavior.
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
    def __init__(self, content: str, finish_reason: str = "stop") -> None:
        self.message = _StubChoiceMessage(content)
        self.finish_reason = finish_reason


class _StubResponse:
    def __init__(self, content: str) -> None:
        self.choices = [_StubChoice(content)]


class _StubCompletions:
    def __init__(self, content: str) -> None:
        self._content = content
        self.last_kwargs: dict[str, Any] = {}

    async def create(self, **kwargs: Any) -> _StubResponse:
        self.last_kwargs = kwargs
        return _StubResponse(self._content)


class _StubChat:
    def __init__(self, content: str) -> None:
        self.completions = _StubCompletions(content)


class _StubLLMClient:
    """Minimal AsyncOpenAI-compatible stub.

    Returns a fixed JSON payload (default ``{"supported": true, ...}``) so
    deterministic axes stay decoupled from judge content. Pass
    ``supported=False`` for an unsupported-evidence test.
    """

    def __init__(self, supported: bool = True, reasoning: str = "stub") -> None:
        self.chat = _StubChat(json.dumps({"supported": supported, "reasoning": reasoning}))


def _agent_perfect_payload() -> str:
    return json.dumps(
        {
            "root_causes": [
                {
                    "service": "shipping",
                    "fault_kind": "network_delay",
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


def test_evaluate_v2_perfect(tmp_path: Path) -> None:
    """Perfect agent: f1=1, sql=1, every evidence judged supported, exact_match=True."""
    case = _make_case(tmp_path)
    res = asyncio.run(
        evaluate_v2(
            _agent_perfect_payload(),
            _injection(),
            case,
            gt_graph=None,
            llm_client=_StubLLMClient(supported=True),  # type: ignore[arg-type]
        )
    )
    assert res.f1 == 1.0
    assert res.precision == 1.0
    assert res.recall == 1.0
    assert res.exact_match is True
    assert res.fault_kind_accuracy == 1.0
    assert res.sql_executable_rate == 1.0
    assert res.evidence_support_rate == 1.0
    assert res.n_evidence_judged == 1
    assert res.n_evidence_judge_failed == 0


def test_evaluate_v2_unsupported_evidence(tmp_path: Path) -> None:
    """Judge says supported=False on every evidence → evidence_support_rate=0,
    but f1 / sql_executable_rate stay at 1 (independent axes)."""
    case = _make_case(tmp_path)
    res = asyncio.run(
        evaluate_v2(
            _agent_perfect_payload(),
            _injection(),
            case,
            gt_graph=None,
            llm_client=_StubLLMClient(supported=False),  # type: ignore[arg-type]
        )
    )
    assert res.f1 == 1.0
    assert res.sql_executable_rate == 1.0
    assert res.evidence_support_rate == 0.0
    assert res.exact_match is True


def test_evaluate_v2_unparseable_response(tmp_path: Path) -> None:
    """Parse-error short-circuits the pipeline — judge is never called."""
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
    assert res.f1 == 0.0
    assert res.exact_match is False
    assert res.parse_error is not None and "JSON" in res.parse_error
    assert res.evidence_support_rate is None  # no evidence even attempted


class _RaisingCompletions:
    async def create(self, **_kwargs: Any) -> Any:
        raise RuntimeError("simulated outage")


class _RaisingChat:
    def __init__(self) -> None:
        self.completions = _RaisingCompletions()


class _StubLLMClientFails:
    """Stub that raises on every call — simulates a judge outage."""

    def __init__(self) -> None:
        self.chat = _RaisingChat()


def test_evaluate_v2_judge_failure_isolates_per_evidence(tmp_path: Path) -> None:
    """Judge raises on every evidence → per-evidence supported=None,
    n_evidence_judge_failed counted; evidence_support_rate falls back to 0
    (case scores 0 in the benchmark mean per README) but f1 / sql stay correct."""
    case = _make_case(tmp_path)
    res = asyncio.run(
        evaluate_v2(
            _agent_perfect_payload(),
            _injection(),
            case,
            gt_graph=None,
            llm_client=_StubLLMClientFails(),  # type: ignore[arg-type]
        )
    )
    assert res.f1 == 1.0
    assert res.sql_executable_rate == 1.0
    assert res.evidence_support_rate == 0.0
    assert res.n_evidence == 1
    assert res.n_evidence_judged == 0
    assert res.n_evidence_judge_failed == 1
    assert res.per_evidence[0].supported is None


# ──────────────────────────────────────────────────────────────────────
# Per-evidence judge: JSON extraction tolerance + bool coercion
# ──────────────────────────────────────────────────────────────────────


def _judge_call(content: str) -> Any:
    """Helper: run evidence_support against a stub that returns ``content``."""
    from rcabench_platform.v3.sdk.evaluation.v2.chain_judge import evidence_support
    from rcabench_platform.v3.sdk.evaluation.v2.schema import Evidence as Ev
    from rcabench_platform.v3.sdk.evaluation.v2.sql_verify import EvidenceStatus, EvidenceVerifyResult

    client = type("C", (), {"chat": _StubChat(content)})()
    ev = Ev(kind=EvidenceKind.METRIC, sql="SELECT 1", claim="x")
    vr = EvidenceVerifyResult(status=EvidenceStatus.OK, row_count=1, sample_rows=[{"x": 1}])
    return asyncio.run(
        evidence_support(
            chain_summary="(empty chain)",
            location="root_cause[0]",
            label="rc[0].ev[0]",
            evidence=ev,
            verify_result=vr,
            llm_client=client,  # type: ignore[arg-type]
        )
    )


def test_evidence_judge_extracts_json_from_markdown_fence() -> None:
    fenced = '```json\n{"supported": true, "reasoning": "ok"}\n```'
    res = _judge_call(fenced)
    assert res.supported is True
    assert res.reasoning == "ok"


def test_evidence_judge_extracts_json_after_prose_preamble() -> None:
    preamble = 'Sure, here is my judgement: {"supported": false, "reasoning": "mismatch"}'
    res = _judge_call(preamble)
    assert res.supported is False


def test_evidence_judge_coerces_string_booleans() -> None:
    """Some models emit "true"/"false" strings instead of JSON booleans."""
    res = _judge_call('{"supported": "yes", "reasoning": "x"}')
    assert res.supported is True
    res = _judge_call('{"supported": "no", "reasoning": "x"}')
    assert res.supported is False


def test_evidence_judge_unparseable_value_returns_none() -> None:
    """Garbage in the supported field → supported=None (not silently coerced)."""
    res = _judge_call('{"supported": "maybe", "reasoning": "x"}')
    assert res.supported is None


def test_evidence_judge_no_response_format_param() -> None:
    """Regression: gateways like litellm 400 on response_format=json_object;
    the judge must rely on prompt-only formatting, not that flag."""
    from rcabench_platform.v3.sdk.evaluation.v2.chain_judge import evidence_support
    from rcabench_platform.v3.sdk.evaluation.v2.schema import Evidence as Ev
    from rcabench_platform.v3.sdk.evaluation.v2.sql_verify import EvidenceStatus, EvidenceVerifyResult

    completions = _StubCompletions(json.dumps({"supported": True, "reasoning": "ok"}))
    chat = type("Chat", (), {"completions": completions})()
    client = type("C", (), {"chat": chat})()

    ev = Ev(kind=EvidenceKind.METRIC, sql="SELECT 1", claim="x")
    vr = EvidenceVerifyResult(status=EvidenceStatus.OK, row_count=1, sample_rows=[{"x": 1}])
    asyncio.run(
        evidence_support(
            chain_summary="(empty chain)",
            location="root_cause[0]",
            label="rc[0].ev[0]",
            evidence=ev,
            verify_result=vr,
            llm_client=client,  # type: ignore[arg-type]
        )
    )
    assert "response_format" not in completions.last_kwargs


# ──────────────────────────────────────────────────────────────────────
# Batch aggregation via RCABenchProcesser.calculate_metrics
# ──────────────────────────────────────────────────────────────────────


def test_calculate_metrics_aggregation() -> None:
    """5 stub samples → aggregator divides by total n=5, except
    ``avg_fault_kind_accuracy`` (uses ``kind_accuracy_denom`` so cases with
    no service-correct rcs don't smear the metric).

    Sample mix:
      [0] perfect    f1=1.0 exact=True  kind_acc=1.0 sql=1.0 ev=1.0 path=True  any_hit=True
      [1] partial    f1=0.5 exact=False kind_acc=0.5 sql=0.8 ev=0.6 path=False any_hit=True (HIT, no path)
      [2] parse-err  zeros + parse_error=True (kind_acc=None, path=None excluded) any_hit=False
      [3] judge-fail f1=1.0 ev=0.0 n_evidence_judge_failed=1 path=True any_hit=True
      [4] eval-err   sample.meta['eval_v2'] = {'error': '...'}
    """
    from rcabench_platform.v3.sdk.llm_eval.eval.processer.rcabench import RCABenchProcesser

    class _StubSample:
        def __init__(self, meta: dict[str, Any]) -> None:
            self.meta: dict[str, Any] = meta

    samples = [
        _StubSample(
            {
                "eval_v2": {
                    "precision": 1.0,
                    "recall": 1.0,
                    "f1": 1.0,
                    "exact_match": True,
                    "fault_kind_accuracy": 1.0,
                    "kind_accuracy_denom": 1,
                    "sql_executable_rate": 1.0,
                    "evidence_support_rate": 1.0,
                    "node_f1": 1.0,
                    "edge_f1": 1.0,
                    "path_reachability": True,
                    "any_root_cause_hit": True,
                    "n_evidence_judge_failed": 0,
                    "per_evidence": [{}],
                }
            }
        ),
        _StubSample(
            {
                "eval_v2": {
                    "precision": 0.5,
                    "recall": 0.5,
                    "f1": 0.5,
                    "exact_match": False,
                    "fault_kind_accuracy": 0.5,
                    "kind_accuracy_denom": 2,
                    "sql_executable_rate": 0.8,
                    "evidence_support_rate": 0.6,
                    "node_f1": 0.4,
                    "edge_f1": 0.2,
                    "path_reachability": False,
                    "any_root_cause_hit": True,
                    "n_evidence_judge_failed": 0,
                    "per_evidence": [{}],
                }
            }
        ),
        _StubSample(
            {
                "eval_v2": {
                    "precision": 0.0,
                    "recall": 0.0,
                    "f1": 0.0,
                    "exact_match": False,
                    "fault_kind_accuracy": None,  # no service-correct rcs → excluded from mean
                    "kind_accuracy_denom": 0,
                    "sql_executable_rate": 0.0,
                    "evidence_support_rate": 0.0,
                    "node_f1": 0.0,
                    "edge_f1": 0.0,
                    "path_reachability": None,
                    "any_root_cause_hit": False,
                    "n_evidence_judge_failed": 0,
                    "parse_error": "bad json",
                    "per_evidence": [],
                }
            }
        ),
        _StubSample(
            {
                "eval_v2": {
                    "precision": 1.0,
                    "recall": 1.0,
                    "f1": 1.0,
                    "exact_match": True,
                    "fault_kind_accuracy": 1.0,
                    "kind_accuracy_denom": 1,
                    "sql_executable_rate": 1.0,
                    "evidence_support_rate": 0.0,
                    "node_f1": 1.0,
                    "edge_f1": 1.0,
                    "path_reachability": True,
                    "any_root_cause_hit": True,
                    "n_evidence_judge_failed": 1,
                    "per_evidence": [{}],
                }
            }
        ),
        _StubSample({"eval_v2": {"error": "missing case dir"}}),
    ]

    proc = RCABenchProcesser.__new__(RCABenchProcesser)
    proc.name = "RCABench"
    metrics = proc.calculate_metrics(samples)  # type: ignore[arg-type]

    assert metrics["total_samples"] == 5
    assert metrics["scored_samples"] == 4
    assert metrics["exact_match_count"] == 2
    assert metrics["exact_match_rate"] == round(2 / 5, 4)
    assert metrics["parse_errors"] == 1
    assert metrics["zero_evidence_outputs"] == 1
    assert metrics["judge_failed"] == 1
    # All P/R/F1/sql/ev_support/node/edge averages divide by total n=5.
    assert metrics["avg_precision"] == round((1.0 + 0.5 + 0.0 + 1.0) / 5, 4)
    assert metrics["avg_recall"] == round((1.0 + 0.5 + 0.0 + 1.0) / 5, 4)
    assert metrics["avg_f1"] == round((1.0 + 0.5 + 0.0 + 1.0) / 5, 4)
    assert metrics["avg_sql_executable_rate"] == round((1.0 + 0.8 + 0.0 + 1.0) / 5, 4)
    assert metrics["avg_evidence_support_rate"] == round((1.0 + 0.6 + 0.0 + 0.0) / 5, 4)
    assert metrics["avg_node_f1"] == round((1.0 + 0.4 + 0.0 + 1.0) / 5, 4)
    assert metrics["avg_edge_f1"] == round((1.0 + 0.2 + 0.0 + 1.0) / 5, 4)
    # fault_kind_accuracy excludes the parse-err case (denom=0); 3 cases contribute.
    assert metrics["kind_accuracy_denom"] == 3
    assert metrics["avg_fault_kind_accuracy"] == round((1.0 + 0.5 + 1.0) / 3, 4)
    # path_reachability divides by total n=5; None / parse-err / eval-err count as 0.
    # 2 of 5 samples are reachable.
    assert metrics["avg_path_reachability"] == round(2 / 5, 4)
    # any_root_cause_hit also divides by n=5. 3 of 5 samples have a HIT.
    # Per-case invariant any_hit ≥ path_reach now lifts to rate level: 3/5 ≥ 2/5.
    assert metrics["any_root_cause_hit_count"] == 3
    assert metrics["avg_any_root_cause_hit"] == round(3 / 5, 4)
    assert metrics["avg_any_root_cause_hit"] >= metrics["avg_path_reachability"]
