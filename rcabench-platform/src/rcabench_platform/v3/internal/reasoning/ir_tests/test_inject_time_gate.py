from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext
from rcabench_platform.v3.internal.reasoning.algorithms.gates.inject_time import InjectTimeGate
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.algorithms.policy import DOWNSTREAM_NODE_PRE_GRACE_SECONDS


def _path(onsets: list[int]) -> CandidatePath:
    return CandidatePath(
        node_ids=[1, 2],
        all_states=[["slow"], ["slow"]],
        picked_states=["slow", "slow"],
        picked_state_start_times=onsets,
        edge_descs=["calls"],
        rule_ids=["test"],
        rule_confidences=[1.0],
        propagation_delays=[0.0],
    )


def _ctx(t0: int = 100) -> GateContext:
    return GateContext(
        graph=None,  # type: ignore[arg-type]
        timelines={},
        rules=[],
        rule_matcher=None,  # type: ignore[arg-type]
        injection_window=(t0, 200),
        injection_node_ids=frozenset({1}),
    )


def test_inject_time_gate_allows_small_downstream_pre_window_jitter() -> None:
    t0 = 100
    result = InjectTimeGate().evaluate(_path([t0, t0 - DOWNSTREAM_NODE_PRE_GRACE_SECONDS]), _ctx(t0))

    assert result.passed
    downstream = result.evidence["nodes"][1]
    assert downstream["pre_grace_seconds"] == DOWNSTREAM_NODE_PRE_GRACE_SECONDS
    assert downstream["lower_bound"] == t0 - DOWNSTREAM_NODE_PRE_GRACE_SECONDS


def test_inject_time_gate_rejects_downstream_onsets_before_pre_grace() -> None:
    t0 = 100
    result = InjectTimeGate().evaluate(_path([t0, t0 - DOWNSTREAM_NODE_PRE_GRACE_SECONDS - 1]), _ctx(t0))

    assert not result.passed
    assert result.reason == "1 node(s) outside window"
