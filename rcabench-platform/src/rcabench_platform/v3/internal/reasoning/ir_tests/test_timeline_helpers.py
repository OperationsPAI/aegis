"""Helper queries on StateTimeline: ever_in / ever_in_any / labels_at / ever_carries."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.synth import synth_timelines
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


def _t(at: int, to_state: str, **ev: object) -> Transition:
    return Transition(
        node_key="span|GET /foo",
        kind=PlaceKind.span,
        at=at,
        from_state="unknown",
        to_state=to_state,
        trigger="x",
        level=EvidenceLevel.observed,
        evidence=ev,  # type: ignore[arg-type]
    )


def _build(transitions: list[Transition], end: int = 100) -> StateTimeline:
    return synth_timelines(transitions, observation_end=end)["span|GET /foo"]


def test_ever_in_finds_state_in_any_window() -> None:
    tl = _build([_t(10, "healthy"), _t(20, "slow"), _t(30, "erroring")])
    assert tl.ever_in("slow") is True
    assert tl.ever_in("erroring") is True
    assert tl.ever_in("unavailable") is False


def test_ever_in_any_matches_set() -> None:
    tl = _build([_t(10, "healthy"), _t(20, "slow")])
    assert tl.ever_in_any({"slow", "erroring"}) is True
    assert tl.ever_in_any({"erroring", "unavailable"}) is False
    assert tl.ever_in_any(frozenset({"slow"})) is True


def test_labels_at_returns_window_labels() -> None:
    tl = _build(
        [
            _t(10, "slow", specialization_labels=frozenset({"jvm_gc"})),
            _t(20, "erroring", specialization_labels=frozenset({"db_query", "abort"})),
        ]
    )
    assert tl.labels_at(5) == frozenset()
    assert tl.labels_at(15) == frozenset({"jvm_gc"})
    assert tl.labels_at(25) == frozenset({"db_query", "abort"})


def test_labels_at_empty_when_window_lacks_labels() -> None:
    tl = _build([_t(10, "slow")])
    assert tl.labels_at(15) == frozenset()


def test_ever_carries_label_in_any_window() -> None:
    tl = _build(
        [
            _t(10, "slow", specialization_labels=frozenset({"jvm_gc"})),
            _t(20, "erroring", specialization_labels=frozenset({"abort"})),
        ]
    )
    assert tl.ever_carries("jvm_gc") is True
    assert tl.ever_carries("abort") is True
    assert tl.ever_carries("network_partition") is False
