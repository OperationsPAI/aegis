"""synth_timelines: severity selection, evidence union, time ordering."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.synth import synth_timelines
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


def _t(at: int, to_state: str, trigger: str = "x", **ev: object) -> Transition:
    return Transition(
        node_key="span|GET /foo",
        kind=PlaceKind.span,
        at=at,
        from_state="unknown",
        to_state=to_state,
        trigger=trigger,
        level=EvidenceLevel.observed,
        evidence=ev,  # type: ignore[arg-type]
    )


def test_sequential_transitions_build_windows() -> None:
    transitions = [_t(10, "healthy"), _t(20, "slow"), _t(30, "erroring")]
    timelines = synth_timelines(transitions, observation_end=40)
    tl = timelines["span|GET /foo"]
    states = [(w.start, w.end, w.state) for w in tl.windows]
    assert states == [(10, 20, "healthy"), (20, 30, "slow"), (30, 40, "erroring")]


def test_pre_first_transition_window_is_unknown() -> None:
    transitions = [_t(10, "healthy")]
    timelines = synth_timelines(transitions, observation_start=0, observation_end=20)
    tl = timelines["span|GET /foo"]
    assert tl.windows[0].state == "unknown"
    assert tl.windows[0].level == EvidenceLevel.inferred
    assert tl.windows[0].start == 0
    assert tl.windows[0].end == 10


def test_same_at_different_state_picks_higher_severity() -> None:
    transitions = [_t(10, "slow", trigger="latency"), _t(10, "erroring", trigger="err_rate")]
    timelines = synth_timelines(transitions, observation_end=20)
    tl = timelines["span|GET /foo"]
    assert len(tl.windows) == 1
    assert tl.windows[0].state == "erroring"
    assert tl.windows[0].trigger == "err_rate"


def test_same_at_same_state_unions_labels() -> None:
    a = _t(10, "slow", specialization_labels=frozenset({"jvm_gc"}))
    b = _t(10, "slow", specialization_labels=frozenset({"db_query"}))
    timelines = synth_timelines([a, b], observation_end=20)
    win = timelines["span|GET /foo"].windows[0]
    assert win.state == "slow"
    labels = win.evidence.get("specialization_labels")
    assert labels == frozenset({"jvm_gc", "db_query"})


def test_out_of_order_input_sorts_by_at() -> None:
    transitions = [_t(30, "erroring"), _t(10, "healthy"), _t(20, "slow")]
    timelines = synth_timelines(transitions, observation_end=40)
    states = [w.state for w in timelines["span|GET /foo"].windows]
    assert states == ["healthy", "slow", "erroring"]


def test_multiple_nodes_synthesized_independently() -> None:
    a = Transition(
        node_key="span|A",
        kind=PlaceKind.span,
        at=10,
        from_state="unknown",
        to_state="slow",
        trigger="x",
        level=EvidenceLevel.observed,
        evidence={},
    )
    b = Transition(
        node_key="pod|B",
        kind=PlaceKind.pod,
        at=15,
        from_state="unknown",
        to_state="unavailable",
        trigger="y",
        level=EvidenceLevel.structural,
        evidence={},
    )
    timelines = synth_timelines([a, b], observation_end=30)
    assert set(timelines.keys()) == {"span|A", "pod|B"}
    assert timelines["span|A"].kind == PlaceKind.span
    assert timelines["pod|B"].kind == PlaceKind.pod


def test_state_at_locates_window() -> None:
    transitions = [_t(10, "healthy"), _t(20, "erroring")]
    tl = synth_timelines(transitions, observation_end=30)["span|GET /foo"]
    assert tl.state_at(5) is None
    assert tl.state_at(15) == "healthy"
    assert tl.state_at(25) == "erroring"
