"""Temporal admission with measurement-noise tolerance (§7.5 + §12.4).

Covers ``policy.epsilon_eff_seconds`` arithmetic, the §7.5 trajectory rule
(``onset_for_rule`` returns the EARLIEST matching transition), and the
ε-tolerant ``find_admissible_window`` lower bound.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.policy import epsilon_eff_seconds
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import TemporalValidator
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, PlaceKind


def _w(start: int, end: int, state: str) -> TimelineWindow:
    return TimelineWindow(
        start=start,
        end=end,
        state=state,
        level=EvidenceLevel.observed,
        trigger="test",
        evidence={},
    )


def _tl(node_key: str, *windows: TimelineWindow) -> StateTimeline:
    return StateTimeline(node_key=node_key, kind=PlaceKind.span, windows=tuple(windows))


# ---------------------------------------------------------------------------
# Test 1: ε_eff arithmetic — basic decomposition.
# ---------------------------------------------------------------------------


def test_epsilon_eff_calls_erroring_to_silent() -> None:
    # ε(calls)=5 + onset_resolution(erroring)=3 + onset_resolution(silent)=30
    assert epsilon_eff_seconds("erroring", "silent", DepKind.calls) == 5 + 3 + 30


def test_epsilon_eff_runs_unavailable_to_restarting() -> None:
    # Lifecycle channel — runs gets the 60s budget per §12.4.
    assert epsilon_eff_seconds("unavailable", "restarting", DepKind.runs) == 60 + 1 + 1


def test_epsilon_eff_routes_to_degraded_to_erroring() -> None:
    # ε(routes_to)=10 + onset_resolution(degraded)=5 + onset_resolution(erroring)=3
    assert epsilon_eff_seconds("degraded", "erroring", DepKind.routes_to) == 10 + 5 + 3


# ---------------------------------------------------------------------------
# Test 2: MISSING inherits onset_resolution from src state.
# ---------------------------------------------------------------------------


def test_epsilon_eff_missing_inherits_src_onset_resolution() -> None:
    # missing has no observed onset — it inherits onset_resolution(src).
    # ε(calls)=5 + onset_resolution(erroring)=3 + onset_resolution(missing<-erroring)=3
    assert epsilon_eff_seconds("erroring", "missing", DepKind.calls) == 5 + 3 + 3


def test_epsilon_eff_missing_inherits_silent_onset_resolution() -> None:
    # missing inherits the larger 30s when src=silent.
    assert epsilon_eff_seconds("silent", "missing", DepKind.calls) == 5 + 30 + 30


# ---------------------------------------------------------------------------
# Test 3: onset_for_rule picks the EARLIEST matching transition (§7.5).
# ---------------------------------------------------------------------------


def test_onset_for_rule_picks_earliest_match() -> None:
    tl = _tl(
        "svc-a::span",
        _w(0, 10, "healthy"),
        _w(10, 40, "erroring"),
        _w(40, 70, "silent"),
    )
    tv = TemporalValidator({"svc-a::span": tl})

    # SILENT-only — only the third window matches.
    assert tv.onset_for_rule("svc-a::span", {"silent"}) == 40
    # ERRORING ∪ SILENT — earliest is the ERRORING window at 10.
    assert tv.onset_for_rule("svc-a::span", {"erroring", "silent"}) == 10


# ---------------------------------------------------------------------------
# Test 4: onset_for_rule returns None when no window matches.
# ---------------------------------------------------------------------------


def test_onset_for_rule_no_match_returns_none() -> None:
    tl = _tl(
        "svc-a::span",
        _w(0, 10, "healthy"),
        _w(10, 40, "erroring"),
    )
    tv = TemporalValidator({"svc-a::span": tl})

    assert tv.onset_for_rule("svc-a::span", {"silent"}) is None
    # Empty src_states is also a no-op.
    assert tv.onset_for_rule("svc-a::span", set()) is None
    # Missing node key.
    assert tv.onset_for_rule("nope", {"erroring"}) is None


# ---------------------------------------------------------------------------
# Test 5: find_admissible_window respects the ε_eff lower bound.
# ---------------------------------------------------------------------------


def test_find_admissible_window_boundary_admit() -> None:
    # ε_eff(erroring, erroring, calls) = 5 + 3 + 3 = 11
    # src_onset=110, dst.start=100 → 100 >= 110 - 11 = 99 → ADMIT
    tl = _tl("dst", _w(100, 200, "erroring"))
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=110,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring"},
    )
    assert w is not None and w.start == 100


def test_find_admissible_window_boundary_reject() -> None:
    # Same setup but src_onset=115 → 100 >= 115 - 11 = 104? 100 < 104 → REJECT
    tl = _tl("dst", _w(100, 200, "erroring"))
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=115,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring"},
    )
    assert w is None


def test_find_admissible_window_state_filter() -> None:
    # Wrong state → reject even though timing is fine.
    tl = _tl("dst", _w(100, 200, "healthy"))
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=110,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring"},
    )
    assert w is None


# ---------------------------------------------------------------------------
# Test 6: find_admissible_window picks earliest admissible (not earliest match).
# ---------------------------------------------------------------------------


def test_find_admissible_window_picks_earliest_admissible() -> None:
    # Two SILENT windows: [40..70] and [80..120].
    # ε_eff(erroring, silent, calls) = 5 + 3 + 30 = 38
    # src_onset=90 → admissibility threshold = 90 - 38 = 52
    #   first window start=40 < 52 → REJECT
    #   second window start=80 >= 52 → ADMIT
    tl = _tl(
        "dst",
        _w(40, 70, "silent"),
        _w(80, 120, "silent"),
    )
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=90,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"silent"},
    )
    assert w is not None
    assert w.start == 80


def test_find_admissible_window_per_state_epsilon() -> None:
    # ε_eff varies per candidate state.
    # First window ERRORING at 100, second SILENT at 80.
    # src_onset=130, src_state=erroring, dst_states={erroring, silent}.
    #   For ERRORING window (start=100): ε_eff=5+3+3=11 → 100 >= 130-11=119? No → REJECT
    #   For SILENT window (start=80):   ε_eff=5+3+30=38 → 80 >= 130-38=92? No → REJECT
    # Both rejected.
    tl = _tl(
        "dst",
        _w(80, 99, "silent"),
        _w(100, 200, "erroring"),
    )
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=130,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring", "silent"},
    )
    assert w is None

    # Tighten src_onset so SILENT admits but ERRORING does not — earliest
    # admissible wins (it's the first in start order).
    # src_onset=110: ERRORING (start=100): 100 >= 110-11=99 → ADMIT (and earliest in iter order
    # is SILENT at 80 → 80 >= 110-38=72 → ADMIT first).
    w2 = tv.find_admissible_window(
        "dst",
        src_onset=110,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring", "silent"},
    )
    assert w2 is not None and w2.start == 80 and w2.state == "silent"
