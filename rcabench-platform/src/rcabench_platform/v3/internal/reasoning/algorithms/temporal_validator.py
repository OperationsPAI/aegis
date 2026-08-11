"""Temporal causality validation over canonical-state StateTimelines.

Given the per-node ``StateTimeline``s produced by the IR pipeline, find
the ``TimelineWindow`` that activates a given node at-or-after a cause's
start time. Used by the propagator when verifying multi-hop paths.

Per §7.5 of ``docs/reasoning-feature-taxonomy.md``, edge admission uses
a measurement-noise-tolerant predicate::

    onset(B) >= onset(A) - epsilon_eff(s_A, s_B, edge_kind)

where ``epsilon_eff`` comes from ``policy.epsilon_eff_seconds``.

EvidenceLevel-aware admission
-----------------------------
The §7.5 predicate compares wall-clock observation onsets. But IR windows
also include ``inferred`` (logically simultaneous with cause; no independent
timestamp) and ``structural`` (synth artefact, e.g. zero-duration container
rollups) levels — for those the literal ``window.start`` is symbolic, not a
real measurement, so applying the wall-clock predicate to them would
spuriously reject the chain.

``find_admissible_window`` therefore branches on ``window.level``:

* ``observed`` windows go through the §7.5 wall-clock predicate as before.
* ``inferred`` / ``structural`` windows are admitted if their state matches
  ``dst_states`` regardless of ``window.start`` (they have no timing claim
  to violate), and their **effective onset** is clamped up to the source's
  onset so they do not appear to precede their cause.

The validator returns ``(window, effective_onset)``: callers no longer
need to compute the onset themselves.
"""

from __future__ import annotations

import bisect

from rcabench_platform.v3.internal.reasoning.algorithms.policy import epsilon_eff_seconds
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind


def _effective_onset(window: TimelineWindow, prev_onset: int | None) -> int:
    """Onset adjusted by the window's evidence level.

    For ``observed`` windows we keep ``window.start`` so observation-channel
    lag (``window.start > prev_onset``) is faithfully represented. For
    ``inferred`` and ``structural`` windows we clamp up to ``prev_onset``
    since their literal start is a synth artefact and they are logically
    simultaneous with their cause.
    """
    if prev_onset is None:
        return window.start
    if window.level == EvidenceLevel.observed:
        return window.start
    return max(window.start, prev_onset)


class TemporalValidator:
    """Locate causally valid TimelineWindow on a StateTimeline."""

    def __init__(self, timelines: dict[str, StateTimeline]) -> None:
        self._timelines = timelines

    def get_timeline(self, node_key: str) -> StateTimeline | None:
        return self._timelines.get(node_key)

    def find_causal_window(
        self,
        node_key: str,
        min_start_time: int,
        required_states: set[str] | None = None,
    ) -> TimelineWindow | None:
        tl = self._timelines.get(node_key)
        if tl is None or not tl.windows:
            return None
        if required_states is not None:
            return self._find_matching_window(tl.windows, min_start_time, required_states)
        return self._find_causal_window(tl.windows, min_start_time)

    def find_admissible_window(
        self,
        node_key: str,
        src_onset: int,
        edge_kind: DepKind,
        src_state: str,
        dst_states: set[str],
    ) -> tuple[TimelineWindow, int] | None:
        """Earliest admissible (window, effective_onset) on ``node_key``.

        Admission rules (level-aware, see module docstring):

        * ``observed`` windows must satisfy the §7.5 tolerant lower bound::

              window.start >= src_onset - epsilon_eff(src_state, window.state, edge_kind)

        * ``inferred`` / ``structural`` windows are admitted on state match
          alone (their literal start is a synth artefact with no timing
          claim to violate).

        Returns ``(window, effective_onset)`` where ``effective_onset`` is
        ``window.start`` for observed windows and ``max(window.start,
        src_onset)`` for inferred / structural windows. Returns ``None`` if
        no candidate window matches.
        """
        tl = self._timelines.get(node_key)
        if tl is None or not tl.windows or not dst_states:
            return None
        for w in tl.windows:
            if w.state not in dst_states:
                continue
            if w.level == EvidenceLevel.observed:
                eps = epsilon_eff_seconds(src_state, w.state, edge_kind)
                if w.start < src_onset - eps:
                    continue
            return w, _effective_onset(w, src_onset)
        return None

    def onset_for_rule(self, node_key: str, src_states: set[str]) -> int | None:
        """Earliest TimelineWindow start whose state is in ``src_states``.

        Per §7.5, when a rule R fires on entity E with ``R.src_states = S``,
        the rule's source onset is the EARLIEST transition into any state
        in S — not the most-recent. Anchoring on ERRORING (the primary
        failure caused by ``do(fault)``) avoids the temporal gate rejecting
        otherwise-valid chains because the source onset has slipped past
        the downstream onset (e.g. ERRORING -> SILENT drift).
        """
        tl = self._timelines.get(node_key)
        if tl is None or not tl.windows or not src_states:
            return None
        for w in tl.windows:
            if w.state in src_states:
                return w.start
        return None

    @staticmethod
    def _find_causal_window(windows: tuple[TimelineWindow, ...], min_start_time: int) -> TimelineWindow | None:
        for w in windows:
            if w.start <= min_start_time < w.end:
                return w
        starts = [w.start for w in windows]
        idx = bisect.bisect_left(starts, min_start_time)
        if idx < len(windows):
            return windows[idx]
        return None

    @staticmethod
    def _find_matching_window(
        windows: tuple[TimelineWindow, ...],
        min_start_time: int,
        required_states: set[str],
    ) -> TimelineWindow | None:
        if not required_states:
            return None
        for w in windows:
            if w.start <= min_start_time < w.end and w.state in required_states:
                return w
        starts = [w.start for w in windows]
        idx = bisect.bisect_left(starts, min_start_time)
        while idx < len(windows):
            w = windows[idx]
            if w.state in required_states:
                return w
            idx += 1
        return None
