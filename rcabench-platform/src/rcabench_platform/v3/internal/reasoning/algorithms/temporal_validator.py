"""Temporal causality validation over canonical-state StateTimelines.

Given the per-node ``StateTimeline``s produced by the IR pipeline, find
the ``TimelineWindow`` that activates a given node at-or-after a cause's
start time. Used by the propagator when verifying multi-hop paths.
"""

from __future__ import annotations

import bisect

from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow


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
