"""Temporal admission with channel-only ε (§7.5 + FORGE rework §3.3).

Covers ``policy.epsilon_eff_seconds`` arithmetic, the §7.5 trajectory rule
(``onset_for_rule`` returns the EARLIEST matching transition), and the
ε-tolerant ``find_admissible_window`` lower bound.

Phase 3 (FORGE rework) drops the ``onset_resolution(state)`` term from
``epsilon_eff``: the per-edge tolerance is now just
``edge_epsilon_seconds(edge_kind)`` (5s for synchronous calls/includes,
10s for routes_to, 60s for runs/schedules). The lost measurement-noise
compensation is replaced by manifest magnitude-band evidence on the
downstream node — see ``ManifestLayerGate``.
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
    # FORGE rework §3.3: ε is just ε(edge_kind) — no onset_resolution.
    assert epsilon_eff_seconds("erroring", "silent", DepKind.calls) == 5


def test_epsilon_eff_runs_unavailable_to_restarting() -> None:
    # Lifecycle channel — runs gets the 60s budget per §12.4.
    assert epsilon_eff_seconds("unavailable", "restarting", DepKind.runs) == 60


def test_epsilon_eff_routes_to_degraded_to_erroring() -> None:
    # ε(routes_to) = 10 (no onset_resolution add).
    assert epsilon_eff_seconds("degraded", "erroring", DepKind.routes_to) == 10


# ---------------------------------------------------------------------------
# Test 2: state arguments are accepted but no longer affect ε (§3.3).
# ---------------------------------------------------------------------------


def test_epsilon_eff_state_arguments_are_ignored() -> None:
    # Whatever the state pair, ε equals ε(edge_kind).
    assert epsilon_eff_seconds("erroring", "missing", DepKind.calls) == 5
    assert epsilon_eff_seconds("silent", "missing", DepKind.calls) == 5
    assert epsilon_eff_seconds("healthy", "healthy", DepKind.includes) == 5
    assert epsilon_eff_seconds("unknown", "unknown", DepKind.schedules) == 60


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
    # FORGE §3.3: ε_eff = ε(calls) = 5
    # src_onset=104, dst.start=100 → 100 >= 104 - 5 = 99 → ADMIT
    tl = _tl("dst", _w(100, 200, "erroring"))
    tv = TemporalValidator({"dst": tl})

    admitted = tv.find_admissible_window(
        "dst",
        src_onset=104,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring"},
    )
    assert admitted is not None
    w, onset = admitted
    assert w.start == 100
    # Observed window keeps wall-clock start as effective onset.
    assert onset == 100


def test_find_admissible_window_boundary_reject() -> None:
    # Same setup but src_onset=110 → 100 >= 110 - 5 = 105? 100 < 105 → REJECT
    tl = _tl("dst", _w(100, 200, "erroring"))
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=110,
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
        src_onset=104,
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
    # FORGE §3.3: ε_eff = ε(calls) = 5
    # src_onset=84 → admissibility threshold = 84 - 5 = 79
    #   first window start=40 < 79 → REJECT
    #   second window start=80 >= 79 → ADMIT
    tl = _tl(
        "dst",
        _w(40, 70, "silent"),
        _w(80, 120, "silent"),
    )
    tv = TemporalValidator({"dst": tl})

    admitted = tv.find_admissible_window(
        "dst",
        src_onset=84,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"silent"},
    )
    assert admitted is not None
    w, _ = admitted
    assert w.start == 80


def test_find_admissible_window_per_state_epsilon() -> None:
    # FORGE §3.3: ε is channel-only — no per-state variation.
    # src_onset=110, ε(calls)=5, threshold=105.
    #   First window SILENT (start=80) < 105 → REJECT
    #   Second window ERRORING (start=100) < 105 → REJECT
    tl = _tl(
        "dst",
        _w(80, 99, "silent"),
        _w(100, 200, "erroring"),
    )
    tv = TemporalValidator({"dst": tl})

    w = tv.find_admissible_window(
        "dst",
        src_onset=110,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring", "silent"},
    )
    assert w is None

    # Tighten src_onset so the ERRORING window admits.
    # src_onset=104: threshold=99. SILENT (80) < 99 → REJECT;
    # ERRORING (100) >= 99 → ADMIT. Iteration order picks the ERRORING window.
    admitted2 = tv.find_admissible_window(
        "dst",
        src_onset=104,
        edge_kind=DepKind.calls,
        src_state="erroring",
        dst_states={"erroring", "silent"},
    )
    assert admitted2 is not None
    w2, _ = admitted2
    assert w2.start == 100 and w2.state == "erroring"
