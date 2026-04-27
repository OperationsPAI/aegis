"""Intra-tier precedence at same (entity, time): direct observation wins.

Covers the methodology fix from ``docs/reasoning-feature-taxonomy.md`` §7.1:
when two adapters emit transitions at the same ``at`` on the same node and
their target states share a severity tier, the tie must be broken by
direct-observation precedence rather than stream order. The losing state's
evidence is preserved under ``evidence['shadowed']`` so the lower-precedence
signal is not silently lost (e.g. Class C: SILENT alongside ERRORING must
not overwrite ERRORING).
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.synth import synth_timelines
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


def _t(
    node_key: str,
    kind: PlaceKind,
    at: int,
    to_state: str,
    trigger: str = "x",
    **ev: object,
) -> Transition:
    return Transition(
        node_key=node_key,
        kind=kind,
        at=at,
        from_state="unknown",
        to_state=to_state,
        trigger=trigger,
        level=EvidenceLevel.observed,
        evidence=ev,  # type: ignore[arg-type]
    )


def test_erroring_wins_over_silent_same_at() -> None:
    erroring = _t("span|GET /foo", PlaceKind.span, 10, "erroring", trigger="err_rate")
    silent = _t("span|GET /foo", PlaceKind.span, 10, "silent", trigger="rate_drop")
    # Emit silent first to prove the result does not depend on stream order.
    timelines = synth_timelines([silent, erroring], observation_end=20)
    win = timelines["span|GET /foo"].windows[0]
    assert win.state == "erroring"
    assert win.trigger == "err_rate"
    shadowed = win.evidence.get("shadowed", ())
    assert len(shadowed) == 1
    assert shadowed[0][0] == "silent"


def test_unavailable_wins_over_missing_same_at() -> None:
    unavailable = _t("pod|p1", PlaceKind.pod, 10, "unavailable", trigger="ready_false")
    missing = _t("pod|p1", PlaceKind.pod, 10, "missing", trigger="no_obs")
    timelines = synth_timelines([missing, unavailable], observation_end=20)
    win = timelines["pod|p1"].windows[0]
    assert win.state == "unavailable"
    shadowed = win.evidence.get("shadowed", ())
    assert any(s[0] == "missing" for s in shadowed)


def test_restarting_wins_over_degraded_same_at() -> None:
    restarting = _t("pod|p2", PlaceKind.pod, 10, "restarting", trigger="cycling")
    degraded = _t("pod|p2", PlaceKind.pod, 10, "degraded", trigger="cpu_pressure")
    timelines = synth_timelines([degraded, restarting], observation_end=20)
    win = timelines["pod|p2"].windows[0]
    assert win.state == "restarting"
    shadowed = win.evidence.get("shadowed", ())
    assert any(s[0] == "degraded" for s in shadowed)


def test_higher_tier_wins_irrespective_of_intra_tier() -> None:
    slow = _t("span|GET /bar", PlaceKind.span, 10, "slow", trigger="latency")
    erroring = _t("span|GET /bar", PlaceKind.span, 10, "erroring", trigger="err_rate")
    timelines = synth_timelines([slow, erroring], observation_end=20)
    win = timelines["span|GET /bar"].windows[0]
    assert win.state == "erroring"
    shadowed = win.evidence.get("shadowed", ())
    assert any(s[0] == "slow" for s in shadowed)


def test_same_state_two_adapters_merges_no_shadow() -> None:
    a = _t(
        "span|GET /baz",
        PlaceKind.span,
        10,
        "slow",
        specialization_labels=frozenset({"jvm_gc"}),
    )
    b = _t(
        "span|GET /baz",
        PlaceKind.span,
        10,
        "slow",
        specialization_labels=frozenset({"db_query"}),
    )
    timelines = synth_timelines([a, b], observation_end=20)
    win = timelines["span|GET /baz"].windows[0]
    assert win.state == "slow"
    assert "shadowed" not in win.evidence
    assert win.evidence.get("specialization_labels") == frozenset({"jvm_gc", "db_query"})


def test_specialization_labels_from_demoted_state_survive() -> None:
    erroring = _t(
        "span|GET /qux",
        PlaceKind.span,
        10,
        "erroring",
        trigger="err_rate",
        specialization_labels=frozenset({"frequent_gc"}),
    )
    silent = _t(
        "span|GET /qux",
        PlaceKind.span,
        10,
        "silent",
        trigger="rate_drop",
        specialization_labels=frozenset({"high_load"}),
    )
    timelines = synth_timelines([erroring, silent], observation_end=20)
    win = timelines["span|GET /qux"].windows[0]
    assert win.state == "erroring"
    assert win.evidence.get("specialization_labels") == frozenset({"frequent_gc", "high_load"})
    shadowed = win.evidence.get("shadowed", ())
    assert any(s[0] == "silent" for s in shadowed)
