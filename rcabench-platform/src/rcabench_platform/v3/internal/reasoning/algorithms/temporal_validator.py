"""Temporal causality validation for fault propagation.

This module provides temporal validation for fault propagation paths,
ensuring that effects occur after their causes with appropriate delays.
Uses binary search for efficient state window lookup in sorted timelines.
"""

import bisect

from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph
from rcabench_platform.v3.internal.reasoning.models.state import StateWindow


class TemporalValidator:
    """Validates temporal causality constraints for fault propagation.

    This class reads state timelines from graph nodes and provides methods
    to find causally valid state windows - windows where a state is active
    at or after a given timestamp, respecting the principle that effects
    must occur after their causes.
    """

    def __init__(self, graph: HyperGraph) -> None:
        """Initialize the temporal validator.

        Args:
            graph: The hypergraph containing nodes with state timelines.
        """
        self.graph = graph

    def get_timeline(self, node_id: int) -> list[StateWindow]:
        """Get the state timeline for a node from graph.

        Args:
            node_id: The node ID to get timeline for.

        Returns:
            List of StateWindows sorted by start_time, or empty list if not found.
        """
        node = self.graph.get_node_by_id(node_id)
        if node and node.state_timeline:
            return node.state_timeline
        return []

    def find_causal_window(
        self,
        node_id: int,
        min_start_time: int,
        required_states: set[str] | None = None,
    ) -> StateWindow | None:
        """Find a state window that is causally valid (at or after min_start_time).

        If required_states is provided, returns the first window that matches
        both temporal and state constraints. If not provided, returns the first
        window at or after min_start_time regardless of states.

        Args:
            node_id: The node ID to find state window for.
            min_start_time: Minimum start time for temporal causality.
            required_states: Optional set of states the window must contain.

        Returns:
            First matching StateWindow or None if no valid window exists.
        """
        timeline = self.get_timeline(node_id)
        if not timeline:
            return None

        if required_states is not None:
            return self._find_matching_state_window(timeline, min_start_time, required_states)
        else:
            return self._find_causal_state_window(timeline, min_start_time)

    def validate_hop(
        self,
        src_id: int,
        dst_id: int,
        src_time: int,
        required_dst_states: set[str] | None = None,
        min_delay: float | None = None,
        max_delay: float | None = None,
    ) -> tuple[StateWindow | None, float]:
        """Validate a single propagation hop with temporal constraints.

        Checks if the destination node has a valid state window after the source
        time, optionally matching required states and delay constraints.

        Args:
            src_id: Source node ID (used for context, not currently used in logic).
            dst_id: Destination node ID to find state window for.
            src_time: Source state start time (causality reference point).
            required_dst_states: Optional set of states the destination must have.
            min_delay: Optional minimum delay in seconds.
            max_delay: Optional maximum delay in seconds.

        Returns:
            Tuple of (StateWindow or None, delay in seconds).
            If no valid window found or delay constraints violated, returns (None, 0.0).
        """
        # Find causally valid window at destination
        causal_window = self.find_causal_window(dst_id, src_time, required_dst_states)

        if causal_window is None:
            return None, 0.0

        # Calculate delay
        delay = float(causal_window.start_time - src_time)

        # Validate delay constraints
        if min_delay is not None and delay < min_delay:
            return None, 0.0

        if max_delay is not None and delay > max_delay:
            return None, 0.0

        return causal_window, delay

    def _find_causal_state_window(self, timeline: list[StateWindow], min_start_time: int) -> StateWindow | None:
        """Find a state window that is causally valid (contains or starts at/after min_start_time).

        A window is causally valid if:
        1. It CONTAINS min_start_time (start <= min_start_time < end), OR
        2. It STARTS at or after min_start_time

        This handles persistent states (e.g., MISSING_SPAN) where the state is active
        throughout a time range, not just at the start time.

        Args:
            timeline: Pre-sorted list of StateWindows by start_time.
            min_start_time: Minimum start time for temporal causality.

        Returns:
            First causally valid StateWindow, or None if not found.
        """
        if not timeline:
            return None

        # First, check if any window CONTAINS min_start_time
        # This handles persistent states that cover a time range
        for window in timeline:
            if window.start_time <= min_start_time < window.end_time:
                return window

        # Then look for windows that start at or after min_start_time
        start_times = [w.start_time for w in timeline]
        idx = bisect.bisect_left(start_times, min_start_time)

        if idx < len(timeline):
            return timeline[idx]
        return None

    def _find_matching_state_window(
        self,
        timeline: list[StateWindow],
        min_start_time: int,
        required_states: set[str],
    ) -> StateWindow | None:
        """Find a state window that is causally valid AND matches required states.

        Causal validity means: the destination node must be in the required state
        at time >= min_start_time. This is satisfied if:

        1. A window STARTS at or after min_start_time (standard case for point-in-time states)
        2. A window CONTAINS min_start_time (for persistent states like MISSING_SPAN)

        Example for MISSING_SPAN:
        - MISSING_SPAN window [1000, 1600) means "span is missing during entire abnormal period"
        - If cause (e.g., container HIGH_CPU) starts at 1080
        - Question: "Is span MISSING at time >= 1080?"
        - Answer: Yes, because 1080 ∈ [1000, 1600) - the span is missing at that moment

        Args:
            timeline: Pre-sorted list of StateWindows by start_time.
            min_start_time: The cause's start time - we check if effect exists at this time or later.
            required_states: Set of states that the window must intersect with.

        Returns:
            First matching StateWindow or None if no valid window exists.
        """
        if not timeline or not required_states:
            return None

        # First, check if any window CONTAINS min_start_time
        # This handles persistent states (e.g., MISSING_SPAN) that cover a time range
        # where the state is continuously active
        for window in timeline:
            if window.start_time <= min_start_time < window.end_time:
                if set(window.states).intersection(required_states):
                    return window

        # Find starting index using binary search for windows that start after min_start_time
        start_times = [w.start_time for w in timeline]
        idx = bisect.bisect_left(start_times, min_start_time)

        # Search forward for a window with matching states
        while idx < len(timeline):
            window = timeline[idx]
            if set(window.states).intersection(required_states):
                return window
            idx += 1

        return None
