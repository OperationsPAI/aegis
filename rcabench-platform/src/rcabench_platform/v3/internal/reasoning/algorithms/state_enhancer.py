"""State enhancer framework for declarative state propagation.

This module provides a declarative framework for enhancing node states based on
graph topology traversal. Instead of hardcoding state propagation logic, rules
can be defined as StateEnhancement dataclasses that specify:
- Source node kind and states to match
- Graph traversal path (edge kinds and directions)
- Target node kind and states to apply

Example: Container RESTARTING -> MISSING_SPAN on related Spans
"""

import logging
from dataclasses import dataclass, field

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.state import ContainerState, PodState, SpanState, StateWindow
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationDirection

logger = logging.getLogger(__name__)


@dataclass
class TraversalStep:
    """A single step in the graph traversal path.

    Defines how to traverse from one node to another via an edge.
    """

    edge_kind: DepKind
    direction: PropagationDirection

    def __post_init__(self) -> None:
        """Validate the traversal step configuration."""
        if not isinstance(self.edge_kind, DepKind):
            raise ValueError(f"edge_kind must be a DepKind, got {type(self.edge_kind)}")
        if not isinstance(self.direction, PropagationDirection):
            raise ValueError(f"direction must be a PropagationDirection, got {type(self.direction)}")


@dataclass
class StateEnhancement:
    """Declarative definition of state enhancement rules.

    Specifies how to propagate states from source nodes to target nodes
    via a graph traversal path.
    """

    source_kind: PlaceKind
    source_states: set[str]
    traversal_path: list[TraversalStep]
    target_kind: PlaceKind
    target_states: set[str]
    description: str = ""
    extend_to_first_activity: bool = field(default=False)
    """If True, extend the source state window end time to when first target activity occurs."""

    def __post_init__(self) -> None:
        """Validate the enhancement configuration."""
        if not self.source_states:
            raise ValueError("source_states cannot be empty")
        if not self.traversal_path:
            raise ValueError("traversal_path cannot be empty")
        if not self.target_states:
            raise ValueError("target_states cannot be empty")


@dataclass
class InjectionPointEnhancement:
    """Enhancement rule that marks all spans of injection service as affected.

    For infrastructure faults (network, container, pod, dns, time), the injection point
    service's spans may not show detectable anomalies in metrics. This enhancement
    explicitly marks them so propagation rules can work correctly.
    """

    fault_categories: set[str]  # e.g., {"network", "container_resource", "pod_lifecycle", "dns", "time"}
    target_states: set[str]  # States to add, e.g., {SpanState.INJECTION_AFFECTED}


class StateEnhancer:
    """Framework for applying declarative state enhancements to a graph.

    StateEnhancer manages a collection of StateEnhancement rules and applies
    them to modify node states based on graph topology traversal.
    """

    def __init__(
        self,
        graph: HyperGraph,
    ):
        """Initialize the state enhancer.

        Args:
            graph: The hypergraph containing topology (states stored in nodes)
        """
        self.graph = graph
        self._enhancements: list[StateEnhancement] = []

    def register(self, enhancement: StateEnhancement) -> None:
        """Register a state enhancement rule.

        Args:
            enhancement: The enhancement rule to register
        """
        self._enhancements.append(enhancement)

    def apply_all(self) -> None:
        """Apply all registered state enhancements."""
        for enhancement in self._enhancements:
            self._apply_enhancement(enhancement)

    def apply_injection_point_enhancement(
        self,
        injection_service_id: int,
        fault_category: str,
        enhancement: InjectionPointEnhancement,
        abnormal_start_time: int | None = None,
        abnormal_end_time: int | None = None,
        source_service_id: int | None = None,
        target_service_id: int | None = None,
        fault_direction: str | None = None,
    ) -> int:
        """Mark spans with enhancement states based on fault type.

        For network/DNS faults with source/target info, only mark cross-service call spans.
        For other faults (container, pod, time), mark all spans of the injection service.

        Args:
            injection_service_id: The service where fault was injected
            fault_category: The fault category (e.g., "network", "container_resource")
            enhancement: The enhancement rule to apply
            abnormal_start_time: Optional start time of abnormal period
            abnormal_end_time: Optional end time of abnormal period
            source_service_id: For network/DNS faults, the source service
            target_service_id: For network/DNS faults, the target service
            fault_direction: For network faults, the direction ("to", "from", "both")

        Returns:
            Number of spans marked with enhancement states
        """
        if fault_category not in enhancement.fault_categories:
            logger.debug(
                f"StateEnhancer: Skipping injection point enhancement for category "
                f"'{fault_category}' (not in {enhancement.fault_categories})"
            )
            return 0

        # Network fault with precise targeting
        # NOTE: source_service/target_service refers to PACKET direction, not call direction.
        #
        # For direction="to": packets from source to target are blocked
        #   - Affects: source calling target (request blocked)
        #   - Affects: target calling source (response blocked)
        #
        # For direction="from": packets from target to source are blocked
        #   - Affects: target calling source (request blocked)
        #   - Affects: source calling target (response blocked)
        #
        # For direction="both": all packets between source and target are blocked
        #   - Affects all calls between source and target
        #
        # In all cases, we need to find spans involved in calls BETWEEN the two services
        # (regardless of which service is the caller), because both request and response
        # travel through the network.
        if fault_category == "network" and source_service_id is not None and target_service_id is not None:
            # Find all spans involved in calls between the two services (both directions)
            span_ids_src_to_tgt = self._get_spans_between_services(
                source_service_id,
                target_service_id,
                "to",
            )
            span_ids_tgt_to_src = self._get_spans_between_services(
                target_service_id,
                source_service_id,
                "to",
            )
            span_ids = list(set(span_ids_src_to_tgt) | set(span_ids_tgt_to_src))

            if span_ids:
                logger.debug(
                    f"StateEnhancer: Network fault (direction={fault_direction}) - "
                    f"found {len(span_ids)} spans between services "
                    f"({len(span_ids_src_to_tgt)} src->tgt calls, {len(span_ids_tgt_to_src)} tgt->src calls)"
                )
            else:
                # Fallback: no direct calls found between services, mark all source spans
                span_ids = self._get_spans_for_service(source_service_id)
                logger.debug(
                    f"StateEnhancer: Network fault - no cross-service calls found, "
                    f"falling back to all {len(span_ids)} spans of source service {source_service_id}"
                )
        # DNS fault: app_name (source) cannot resolve domain (target)
        elif fault_category == "dns" and source_service_id is not None and target_service_id is not None:
            span_ids = self._get_spans_between_services(
                source_service_id,
                target_service_id,
                "to",  # DNS resolution only affects outgoing calls
            )
            if span_ids:
                logger.debug(
                    f"StateEnhancer: DNS fault - found {len(span_ids)} spans from "
                    f"service {source_service_id} to {target_service_id}"
                )
            else:
                # Fallback: no direct calls found, mark all source spans
                span_ids = self._get_spans_for_service(source_service_id)
                logger.debug(
                    f"StateEnhancer: DNS fault - no cross-service calls found, "
                    f"falling back to all {len(span_ids)} spans of source service {source_service_id}"
                )
        else:
            # Fallback: mark all spans of injection service (for container/pod/time)
            span_ids = self._get_spans_for_service(injection_service_id)

        if not span_ids:
            logger.debug(f"StateEnhancer: No spans found for service {injection_service_id}")
            return 0

        # Determine time window for enhancement
        start_time, end_time = self._determine_abnormal_window(abnormal_start_time, abnormal_end_time)

        marked_count = 0
        for span_id in span_ids:
            span_node = self.graph.get_node_by_id(span_id)
            if span_node is None:
                continue

            self._add_enhancement_states_to_span(span_id, enhancement.target_states, start_time, end_time)
            marked_count += 1

        service_node = self.graph.get_node_by_id(injection_service_id)
        service_name = service_node.self_name if service_node else str(injection_service_id)
        logger.info(
            f"StateEnhancer: Applied injection point enhancement: {marked_count} spans "
            f"of service '{service_name}' marked with {enhancement.target_states}"
        )

        return marked_count

    def _get_spans_for_service(self, service_id: int) -> list[int]:
        """Get all span IDs included by a service.

        Args:
            service_id: The service node ID

        Returns:
            List of span node IDs
        """
        span_ids: list[int] = []

        # Traverse 'includes' edges from service to spans
        for src_id, dst_id, key in self.graph._graph.out_edges(service_id, keys=True):
            edge_data = self.graph._graph.edges[src_id, dst_id, key].get("ref")
            if edge_data and edge_data.kind == DepKind.includes:
                dst_node = self.graph.get_node_by_id(dst_id)
                if dst_node and dst_node.kind == PlaceKind.span:
                    span_ids.append(dst_id)

        return span_ids

    def _get_spans_between_services(
        self,
        source_service_id: int,
        target_service_id: int,
        direction: str = "both",
    ) -> list[int]:
        """Get spans representing calls between two services.

        For network and DNS faults, only spans that have direct calls edges crossing
        the service boundary should be marked. This method finds those spans.

        Args:
            source_service_id: Service where traffic originates
            target_service_id: Service receiving traffic
            direction: "to" (source calls target), "from" (target calls source), "both"

        Returns:
            List of span IDs that have calls edges crossing service boundary
        """
        source_spans = set(self._get_spans_for_service(source_service_id))
        target_spans = set(self._get_spans_for_service(target_service_id))

        result: set[int] = set()

        # direction="to" or "both": find source spans that call target spans
        if direction in ("to", "both"):
            for src_span_id in source_spans:
                for _, dst_id, key in self.graph._graph.out_edges(src_span_id, keys=True):
                    edge_data = self.graph._graph.edges[src_span_id, dst_id, key].get("ref")
                    if edge_data and edge_data.kind == DepKind.calls and dst_id in target_spans:
                        result.add(src_span_id)
                        result.add(dst_id)  # Also mark the target span

        # direction="from" or "both": find target spans that call source spans
        if direction in ("from", "both"):
            for tgt_span_id in target_spans:
                for _, dst_id, key in self.graph._graph.out_edges(tgt_span_id, keys=True):
                    edge_data = self.graph._graph.edges[tgt_span_id, dst_id, key].get("ref")
                    if edge_data and edge_data.kind == DepKind.calls and dst_id in source_spans:
                        result.add(tgt_span_id)
                        result.add(dst_id)

        return list(result)

    def _determine_abnormal_window(
        self,
        abnormal_start_time: int | None,
        abnormal_end_time: int | None,
    ) -> tuple[int, int]:
        """Determine the abnormal time window for enhancement.

        If explicit times are not provided, derive from existing node timelines.

        Args:
            abnormal_start_time: Optional explicit start time
            abnormal_end_time: Optional explicit end time

        Returns:
            Tuple of (start_time, end_time)
        """
        if abnormal_start_time is not None and abnormal_end_time is not None:
            return abnormal_start_time, abnormal_end_time

        # Derive from existing timelines in graph
        all_starts: list[int] = []
        all_ends: list[int] = []

        for node in self.graph._node_id_map.values():
            if node.state_timeline:
                for window in node.state_timeline:
                    all_starts.append(window.start_time)
                    all_ends.append(window.end_time)

        if all_starts and all_ends:
            start = abnormal_start_time if abnormal_start_time is not None else min(all_starts)
            end = abnormal_end_time if abnormal_end_time is not None else max(all_ends)
            return start, end

        # Fallback to epoch 0 and far future
        return 0, 2**31 - 1

    def _add_enhancement_states_to_span(
        self,
        span_id: int,
        target_states: set[str],
        start_time: int,
        end_time: int,
    ) -> None:
        """Add enhancement states to a span's timeline.

        Args:
            span_id: The span node ID
            target_states: States to add
            start_time: Start time of enhancement window
            end_time: End time of enhancement window
        """
        span_node = self.graph.get_node_by_id(span_id)
        if span_node is None:
            return

        timeline = list(span_node.state_timeline) if span_node.state_timeline else []

        if not timeline:
            # Create a new timeline with enhancement states
            new_timeline = [
                StateWindow(
                    start_time=start_time,
                    end_time=end_time,
                    states=target_states.copy(),
                )
            ]
            self.graph.set_node_state_timeline(span_id, new_timeline)
            return

        # Add enhancement states to existing windows that overlap
        updated_timeline: list[StateWindow] = []
        for window in timeline:
            if window.start_time <= end_time and window.end_time >= start_time:
                # Overlapping window - add enhancement states
                new_states = window.states | target_states
                updated_timeline.append(
                    StateWindow(
                        start_time=window.start_time,
                        end_time=window.end_time,
                        states=new_states,
                    )
                )
            else:
                updated_timeline.append(window)

        self.graph.set_node_state_timeline(span_id, updated_timeline)

    def _apply_enhancement(self, enhancement: StateEnhancement) -> None:
        """Apply a single state enhancement rule.

        Args:
            enhancement: The enhancement rule to apply
        """
        # Get all nodes of the source kind
        source_nodes = self.graph.get_nodes_by_kind(enhancement.source_kind)

        for source_node in source_nodes:
            if source_node.id is None:
                continue

            # Get source node timeline and find matching state windows
            source_timeline = self._get_timeline(source_node.id)
            matching_windows = [w for w in source_timeline if enhancement.source_states & w.states]

            if not matching_windows:
                continue

            # Calculate the source state window (earliest start, latest end)
            source_start = min(w.start_time for w in matching_windows)
            source_end = max(w.end_time for w in matching_windows)

            # Traverse graph to find target nodes
            target_node_ids = self._traverse_to_targets(
                source_node.id,
                enhancement.traversal_path,
                enhancement.target_kind,
            )

            if not target_node_ids:
                continue

            # Optionally extend the window to first target activity
            if enhancement.extend_to_first_activity:
                first_activity = self._find_first_activity(target_node_ids, source_start)
                if first_activity is not None:
                    source_end = max(source_end, first_activity)

            # Create the source window with extended time
            source_window = StateWindow(
                start_time=source_start,
                end_time=source_end,
                states=enhancement.source_states,
            )

            logger.debug(
                f"StateEnhancer: Applying {enhancement.description} from "
                f"node {source_node.id} to {len(target_node_ids)} targets "
                f"(window: {source_start} - {source_end})"
            )

            # Apply target states to all target nodes
            for target_id in target_node_ids:
                self._add_states_to_node(
                    target_id,
                    source_window,
                    enhancement.target_states,
                )

    def _get_timeline(self, node_id: int) -> list[StateWindow]:
        """Get the state timeline for a node from graph.

        Args:
            node_id: The node ID

        Returns:
            List of StateWindow objects, sorted by start_time
        """
        node = self.graph.get_node_by_id(node_id)
        if node and node.state_timeline:
            return node.state_timeline
        return []

    def _traverse_to_targets(
        self,
        start_node_id: int,
        traversal_path: list[TraversalStep],
        target_kind: PlaceKind,
    ) -> list[int]:
        """Traverse the graph from a start node to find target nodes.

        Args:
            start_node_id: The starting node ID
            traversal_path: List of traversal steps defining the path
            target_kind: Expected PlaceKind of target nodes

        Returns:
            List of target node IDs
        """
        current_nodes = {start_node_id}

        for step in traversal_path:
            next_nodes: set[int] = set()

            for node_id in current_nodes:
                neighbors = self._get_neighbors_by_edge(
                    node_id,
                    step.edge_kind,
                    step.direction,
                )
                next_nodes.update(neighbors)

            current_nodes = next_nodes

            if not current_nodes:
                return []

        # Filter to only nodes matching target_kind
        result: list[int] = []
        for node_id in current_nodes:
            node = self.graph.get_node_by_id(node_id)
            if node is not None and node.kind == target_kind:
                result.append(node_id)

        return result

    def _get_neighbors_by_edge(
        self,
        node_id: int,
        edge_kind: DepKind,
        direction: PropagationDirection,
    ) -> list[int]:
        """Get neighboring nodes connected by a specific edge kind and direction.

        Args:
            node_id: The source node ID
            edge_kind: The type of edge to traverse
            direction: FORWARD (follow edge) or BACKWARD (against edge)

        Returns:
            List of neighbor node IDs
        """
        neighbors: list[int] = []

        if direction == PropagationDirection.FORWARD:
            # Follow edge direction: node -> neighbor
            for src_id, dst_id, key in self.graph._graph.out_edges(node_id, keys=True):
                edge_data = self.graph._graph.edges[src_id, dst_id, key].get("ref")
                if edge_data and edge_data.kind == edge_kind:
                    neighbors.append(dst_id)
        else:
            # Against edge direction: neighbor -> node
            for src_id, dst_id, key in self.graph._graph.in_edges(node_id, keys=True):
                edge_data = self.graph._graph.edges[src_id, dst_id, key].get("ref")
                if edge_data and edge_data.kind == edge_kind:
                    neighbors.append(src_id)

        return neighbors

    def _find_first_activity(self, node_ids: list[int], after_time: int) -> int | None:
        """Find the earliest timestamp when any node has activity after a given time.

        Args:
            node_ids: List of node IDs to check
            after_time: Look for activity after this timestamp

        Returns:
            The timestamp of the first activity, or None if no activity found
        """
        first_activity: int | None = None

        for node_id in node_ids:
            timeline = self._get_timeline(node_id)
            for window in timeline:
                if window.start_time >= after_time:
                    if first_activity is None or window.start_time < first_activity:
                        first_activity = window.start_time
                    break

        return first_activity

    def _add_states_to_node(
        self,
        node_id: int,
        source_window: StateWindow,
        target_states: set[str],
    ) -> None:
        """Add target states to a node's timeline based on source window timing.

        This creates/modifies StateWindows to add the target states during the
        time period when the source state was active.

        Args:
            node_id: The target node ID
            source_window: The source state window defining the time period
            target_states: The states to add to the target node
        """
        timeline = list(self._get_timeline(node_id))

        # If node has no timeline, create one with the target states
        if not timeline:
            new_timeline = [
                StateWindow(
                    start_time=source_window.start_time,
                    end_time=source_window.end_time,
                    states=target_states,
                )
            ]
            self.graph.set_node_state_timeline(node_id, new_timeline)
            return

        # Check if there's a gap before the first window
        first_window = timeline[0]
        if first_window.start_time > source_window.start_time:
            gap_end = min(first_window.start_time, source_window.end_time)
            gap_window = StateWindow(
                start_time=source_window.start_time,
                end_time=gap_end,
                states=target_states,
            )
            timeline = [gap_window] + timeline

            logger.debug(f"StateEnhancer: Added gap window for node {node_id}: {source_window.start_time} - {gap_end}")

        # Update existing windows that overlap with source window
        updated_timeline: list[StateWindow] = []
        for window in timeline:
            # Check if this window overlaps with source window
            if window.start_time <= source_window.end_time and window.end_time >= source_window.start_time:
                # Add target states if not already present
                if not target_states.issubset(window.states):
                    new_states = window.states | target_states
                    updated_timeline.append(
                        StateWindow(
                            start_time=window.start_time,
                            end_time=window.end_time,
                            states=new_states,
                        )
                    )
                else:
                    updated_timeline.append(window)
            else:
                updated_timeline.append(window)

        self.graph.set_node_state_timeline(node_id, updated_timeline)


# Builtin enhancement: Container RESTARTING -> MISSING_SPAN on related Spans
# Traversal path: Container <- Pod <- Service -> Span
# (BACKWARD via runs, BACKWARD via routes_to, FORWARD via includes)
CONTAINER_RESTART_TO_MISSING_SPAN = StateEnhancement(
    source_kind=PlaceKind.container,
    source_states={ContainerState.RESTARTING.value},
    traversal_path=[
        TraversalStep(DepKind.runs, PropagationDirection.BACKWARD),
        TraversalStep(DepKind.routes_to, PropagationDirection.BACKWARD),
        TraversalStep(DepKind.includes, PropagationDirection.FORWARD),
    ],
    target_kind=PlaceKind.span,
    target_states={SpanState.MISSING_SPAN.value},
    description="Container restart propagates MISSING_SPAN to related spans",
    extend_to_first_activity=True,
)

# Builtin enhancement: Pod KILLED -> Container RESTARTING
# When a Pod is killed (e.g., PodKill fault), its containers are implicitly restarted.
# Traversal path: Pod -> Container (FORWARD via runs)
POD_KILLED_TO_CONTAINER_RESTARTING = StateEnhancement(
    source_kind=PlaceKind.pod,
    source_states={PodState.KILLED.value},
    traversal_path=[
        TraversalStep(DepKind.runs, PropagationDirection.FORWARD),
    ],
    target_kind=PlaceKind.container,
    target_states={ContainerState.RESTARTING.value},
    description="Pod killed propagates RESTARTING to its containers",
    extend_to_first_activity=False,
)

# Builtin injection point enhancement: Mark all spans of injection service as INJECTION_AFFECTED
# This is applied for infrastructure faults where metrics may not detect anomalies
INJECTION_POINT_TO_SPAN_AFFECTED = InjectionPointEnhancement(
    fault_categories={"container_resource", "pod_lifecycle", "network", "dns", "time", "jvm_method"},
    target_states={SpanState.INJECTION_AFFECTED.value},
)
