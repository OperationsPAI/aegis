"""Rule matching module for fault propagation.

This module provides the RuleMatcher class which handles matching propagation rules
against graph edges and paths. It supports both single-hop and multi-hop rules with
efficient indexing for O(1) lookup.

Extracted from FaultPropagator to provide a unified rule matching interface.
"""

import logging

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.schema import FirstHopConfig, PropagationDirection, PropagationRule

logger = logging.getLogger(__name__)

# Default FirstHopConfig per PlaceKind
# Different source types have different first-hop semantics:
# - Span: Must validate anomalous states at source (direct HTTP fault injection)
# - Service: Dummy aggregation node, lenient matching
# - Container/Pod: May not have exact rule states at injection point
#
# NOTE: All first hops are lenient with dst_states (lenient_dst_state_match=True)
# because we only require destination to have SOME states, not necessarily matching
# possible_dst_states. This is by design - the first hop from injection may
# propagate to unexpected states due to fault semantics.
DEFAULT_FIRST_HOP_CONFIGS: dict[PlaceKind, FirstHopConfig] = {
    PlaceKind.span: FirstHopConfig(
        require_src_states=True,  # Span injection must have anomalous states
        require_dst_states=True,
        lenient_dst_state_match=True,  # Accept any detected states at destination
    ),
    PlaceKind.service: FirstHopConfig(
        require_src_states=False,  # Service is a dummy aggregation node
        require_dst_states=True,
        lenient_dst_state_match=True,  # Accept any span states for service->span
    ),
    PlaceKind.container: FirstHopConfig(
        require_src_states=False,  # Container may not have exact rule states
        require_dst_states=False,  # ✅ 改为 False：允许 Pod 没有状态
        lenient_dst_state_match=True,  # Accept any detected states
    ),
    PlaceKind.pod: FirstHopConfig(
        require_src_states=False,  # Pod may not have exact rule states
        require_dst_states=False,  # ✅ 改为 False：允许下游节点没有状态
        lenient_dst_state_match=True,  # Accept any detected states
    ),
}


class RuleMatcher:
    """Unified rule matcher for single-hop and multi-hop propagation rules.

    Provides efficient rule lookup via indices and handles:
    - Single-hop rule matching by (src_kind, edge_kind, direction)
    - Multi-hop rule matching by path topology
    - First-hop lenient matching for injection nodes
    """

    def __init__(self, rules: list[PropagationRule]):
        """Initialize the rule matcher.

        Args:
            rules: List of propagation rules to match against
        """
        self.rules = rules
        self._build_rule_indices()

    def _build_rule_indices(self) -> None:
        """Build indices for efficient rule lookup.

        Index rules by (src_kind, edge_kind, direction) for O(1) lookup.
        Multi-hop rules are indexed by their first hop.
        """
        self.rule_index: dict[
            tuple[PlaceKind, DepKind | None, PropagationDirection | None],
            list[PropagationRule],
        ] = {}

        for rule in self.rules:
            if rule.path:
                # Multi-hop rule: index by first hop
                first_hop = rule.path[0]
                key: tuple[PlaceKind, DepKind | None, PropagationDirection | None] = (
                    rule.src_kind,
                    first_hop.edge_kind,
                    first_hop.direction,
                )
            else:
                # Single-hop rule: index by edge_kind and direction
                key = (rule.src_kind, rule.edge_kind, rule.direction)

            if key not in self.rule_index:
                self.rule_index[key] = []
            self.rule_index[key].append(rule)

    def get_rules_for_edge(
        self,
        src_kind: PlaceKind,
        edge_kind: DepKind,
        direction: PropagationDirection,
    ) -> list[PropagationRule]:
        """Get rules that could match an edge with given characteristics.

        Args:
            src_kind: Source node PlaceKind
            edge_kind: Edge type
            direction: Propagation direction

        Returns:
            List of potentially matching rules
        """
        key = (src_kind, edge_kind, direction)
        return self.rule_index.get(key, [])

    def matches_multi_hop_rule(
        self,
        rule: PropagationRule,
        topology_path: list[int],
        graph: HyperGraph,
    ) -> bool:
        """Check if a topology path matches a multi-hop rule structure.

        Args:
            rule: The multi-hop rule to check
            topology_path: List of node IDs forming the path
            graph: The graph containing the nodes

        Returns:
            True if the path structure matches the rule
        """
        if not rule.path:
            return False

        # Path should be: src + intermediate_nodes + dst
        expected_len = len(rule.path) + 1
        if len(topology_path) != expected_len:
            return False

        # Verify each hop in the path
        for hop_idx, path_hop in enumerate(rule.path):
            src_node_id = topology_path[hop_idx]
            dst_node_id = topology_path[hop_idx + 1]

            src_node = graph.get_node_by_id(src_node_id)
            dst_node = graph.get_node_by_id(dst_node_id)

            if src_node is None or dst_node is None:
                return False

            # Check intermediate node kind (if specified)
            if hop_idx < len(rule.path) - 1:  # Not the last hop
                if path_hop.intermediate_kind and dst_node.kind != path_hop.intermediate_kind:
                    return False
            else:  # Last hop
                if dst_node.kind != rule.dst_kind:
                    return False

            # Find the edge between src and dst
            edge_data = self._get_edge_data(graph, src_node_id, dst_node_id, path_hop.direction)
            if edge_data is None:
                return False

            # Check edge kind
            if edge_data.kind != path_hop.edge_kind:
                return False

            # Check edge condition if specified
            if path_hop.edge_condition and not path_hop.edge_condition(edge_data):
                return False

        return True

    def find_matching_multi_hop_rule(
        self,
        topology_path: list[int],
        graph: HyperGraph,
    ) -> PropagationRule | None:
        """Find a multi-hop rule that matches the given topology path.

        Args:
            topology_path: List of node IDs forming the path
            graph: The graph containing the nodes

        Returns:
            Matching PropagationRule or None if no match found
        """
        if len(topology_path) < 2:
            return None

        # Get source node to filter rules
        src_node = graph.get_node_by_id(topology_path[0])
        if src_node is None:
            return None

        # Check all multi-hop rules
        for rule in self.rules:
            if not rule.is_multi_hop:
                continue

            # Check source kind matches
            if rule.src_kind != src_node.kind:
                continue

            # Check if topology matches multi-hop rule
            if self.matches_multi_hop_rule(rule, topology_path, graph):
                return rule

        return None

    def matches_edge(
        self,
        src_node_id: int,
        dst_node_id: int,
        graph: HyperGraph,
        src_states: set[str],
        dst_states: set[str],
        is_first_hop: bool = False,
    ) -> list[PropagationRule]:
        """Find rules that match an edge with given node states.

        This method unifies single-hop and any-hop-of-multi-hop matching.

        Args:
            src_node_id: Source node ID
            dst_node_id: Destination node ID
            graph: The graph containing the nodes
            src_states: States observed at source node
            dst_states: States observed at destination node
            is_first_hop: Whether this is the first hop from injection node

        Returns:
            List of rules that match this edge
        """
        src_node = graph.get_node_by_id(src_node_id)
        dst_node = graph.get_node_by_id(dst_node_id)
        if src_node is None or dst_node is None:
            return []

        edge_data, direction = self._get_edge_between(graph, src_node_id, dst_node_id)
        if edge_data is None or direction is None:
            if is_first_hop:
                logger.debug(
                    f"      [RULE_MATCHER] No edge found between "
                    f"{src_node.kind.value}:{src_node.uniq_name} -> {dst_node.kind.value}:{dst_node.uniq_name}"
                )
            return []

        matching_rules: list[PropagationRule] = []

        # Get rules indexed by (src_kind, edge_kind, direction)
        rule_key = (src_node.kind, edge_data.kind, direction)
        indexed_rules = self.rule_index.get(rule_key, [])

        if is_first_hop:
            logger.debug(
                f"      [RULE_MATCHER] Edge: {src_node.kind.value} --{edge_data.kind.value}({direction.value})--> "
                f"{dst_node.kind.value}, rule_key={rule_key}, indexed_rules={len(indexed_rules)}"
            )

        # Check indexed rules (first hop matches)
        for rule in indexed_rules:
            match_result = self._rule_matches_edge(
                rule,
                src_node.kind,
                dst_node.kind,
                src_states,
                dst_states,
                is_first_hop,
                is_first_hop_of_rule=True,
            )
            if is_first_hop:
                logger.debug(
                    f"        [RULE_MATCHER] Checking rule {rule.rule_id}: "
                    f"src_states={rule.src_states}, dst_kind={rule.dst_kind.value}, "
                    f"match_result={match_result}"
                )
            if match_result:
                matching_rules.append(rule)

        # Also check subsequent hops of multi-hop rules
        for rule in self.rules:
            if not rule.is_multi_hop or not rule.path:
                continue

            # Debug: Check service -> pod edge matching
            if src_node.kind == PlaceKind.service and dst_node.kind == PlaceKind.pod:
                logger.debug(
                    f"    [DEBUG] Checking rule {rule.rule_id} for service->pod edge, "
                    f"edge_data.kind={edge_data.kind}, direction={direction}"
                )

            # Check each hop (except the first, which is already indexed)
            for hop_idx, path_hop in enumerate(rule.path):
                if hop_idx == 0:
                    continue  # First hop already checked above

                # Debug: Check each hop's matching
                if src_node.kind == PlaceKind.service and dst_node.kind == PlaceKind.pod:
                    logger.debug(
                        f"      [DEBUG] hop_idx={hop_idx}, "
                        f"path_hop.edge_kind={path_hop.edge_kind}, "
                        f"path_hop.direction={path_hop.direction}, "
                        f"edge_kind_match={path_hop.edge_kind == edge_data.kind}, "
                        f"direction_match={path_hop.direction == direction}"
                    )

                # Check if edge matches this hop's edge_kind and direction
                if path_hop.edge_kind != edge_data.kind or path_hop.direction != direction:
                    continue

                # For intermediate hops, check intermediate_kind
                if hop_idx < len(rule.path) - 1:
                    # Not the last hop - dst should match intermediate_kind
                    if path_hop.intermediate_kind and path_hop.intermediate_kind != dst_node.kind:
                        continue
                else:
                    # Last hop - dst should match rule.dst_kind
                    if rule.dst_kind != dst_node.kind:
                        continue

                # Check src_node kind matches what would be expected from previous hop
                prev_hop = rule.path[hop_idx - 1]
                if prev_hop.intermediate_kind and prev_hop.intermediate_kind != src_node.kind:
                    continue

                # NOTE: Skip intermediate_states check during BFS exploration.
                # State validation happens later in path verification phase.
                # This prevents premature pruning of valid topology paths.

                # For the last hop, check dst_states matches possible_dst_states
                if hop_idx == len(rule.path) - 1:
                    if dst_states and not dst_states.intersection(set(rule.possible_dst_states)):
                        continue

                # This edge matches a subsequent hop of a multi-hop rule
                if rule not in matching_rules:
                    matching_rules.append(rule)
                break  # Don't add the same rule multiple times

        return matching_rules

    def edge_matches_any_rule(
        self,
        src_node_id: int,
        dst_node_id: int,
        graph: HyperGraph,
        src_states: set[str],
        dst_states: set[str],
        is_first_hop: bool = False,
    ) -> bool:
        """Check if an edge matches any propagation rule.

        Args:
            src_node_id: Source node ID
            dst_node_id: Destination node ID
            graph: The graph containing the nodes
            src_states: States observed at source node
            dst_states: States observed at destination node
            is_first_hop: Whether this is the first hop from injection node

        Returns:
            True if any rule matches
        """
        return (
            len(
                self.matches_edge(
                    src_node_id,
                    dst_node_id,
                    graph,
                    src_states,
                    dst_states,
                    is_first_hop,
                )
            )
            > 0
        )

    def _get_first_hop_config(self, rule: PropagationRule, src_kind: PlaceKind) -> FirstHopConfig:
        """Get the FirstHopConfig for a rule and source kind.

        Uses rule.first_hop_config if specified, otherwise falls back to
        DEFAULT_FIRST_HOP_CONFIGS based on src_kind.

        Args:
            rule: The rule to get config for
            src_kind: Source node PlaceKind

        Returns:
            FirstHopConfig to use for first-hop validation
        """
        if rule.first_hop_config is not None:
            return rule.first_hop_config

        # Use default config for the source kind, or a sensible fallback
        return DEFAULT_FIRST_HOP_CONFIGS.get(
            src_kind,
            FirstHopConfig(
                require_src_states=False,
                require_dst_states=True,
                lenient_dst_state_match=False,
            ),
        )

    def _rule_matches_edge(
        self,
        rule: PropagationRule,
        src_kind: PlaceKind,
        dst_kind: PlaceKind,
        src_states: set[str],
        dst_states: set[str],
        is_first_hop: bool,
        is_first_hop_of_rule: bool,
    ) -> bool:
        """Check if a specific rule matches an edge.

        Uses FirstHopConfig (from rule or defaults) when is_first_hop=True.

        Args:
            rule: Rule to check
            src_kind: Source node PlaceKind
            dst_kind: Destination node PlaceKind
            src_states: States observed at source
            dst_states: States observed at destination
            is_first_hop: Whether this is first hop from injection
            is_first_hop_of_rule: Whether this is the first hop of the rule

        Returns:
            True if rule matches
        """
        # Check destination kind matches
        if rule.is_multi_hop and rule.path and is_first_hop_of_rule:
            # Multi-hop rule: check if first hop's intermediate_kind matches dst_node
            first_hop = rule.path[0]
            if first_hop.intermediate_kind != dst_kind:
                return False
        else:
            # Single-hop rule: dst_kind must match
            if rule.dst_kind != dst_kind:
                return False

        # Get FirstHopConfig for first-hop validation
        first_hop_config = self._get_first_hop_config(rule, src_kind) if is_first_hop else None

        # Check source states
        if is_first_hop and first_hop_config:
            # Use FirstHopConfig to determine source state validation
            if first_hop_config.require_src_states:
                # Strict: source must have states matching rule.src_states
                rule_src_states = set(rule.src_states)
                if rule_src_states:
                    src_match = len(src_states.intersection(rule_src_states)) > 0
                else:
                    # Rule has no src_states constraint, accept any states
                    src_match = len(src_states) > 0
            else:
                # Lenient: don't require source states to match - always accept
                src_match = True
        else:
            # Non-first-hop: standard matching - accept if no states or states match
            src_match = len(src_states) == 0 or len(src_states.intersection(set(rule.src_states))) > 0

        if not src_match:
            return False

        # Check destination states
        if is_first_hop and first_hop_config:
            # Use FirstHopConfig to determine destination state validation
            if first_hop_config.require_dst_states:
                if first_hop_config.lenient_dst_state_match:
                    # Lenient: accept any detected states at destination
                    dst_match = len(dst_states) > 0
                    if is_first_hop and not dst_match:
                        logger.debug(
                            f"          [RULE_MATCHER] Rule {rule.rule_id} REJECTED: "
                            f"require_dst_states=True, lenient=True, but dst_states is empty! "
                            f"dst_states={dst_states}"
                        )
                else:
                    # Strict: destination must have states matching rule.possible_dst_states
                    dst_match = len(dst_states) > 0 and len(dst_states.intersection(set(rule.possible_dst_states))) > 0
                    if is_first_hop and not dst_match:
                        logger.debug(
                            f"          [RULE_MATCHER] Rule {rule.rule_id} REJECTED: "
                            f"strict dst_states matching failed. dst_states={dst_states}, "
                            f"rule.possible_dst_states={rule.possible_dst_states}"
                        )
            else:
                # No destination states required
                dst_match = True
        elif rule.is_multi_hop and rule.path and is_first_hop_of_rule:
            # For multi-hop rules (not first hop from injection), check intermediate_states
            first_hop = rule.path[0]
            if first_hop.intermediate_states is not None:
                # If no states detected, treat as "unknown"
                check_states = dst_states if dst_states else {"unknown"}
                allowed_states = set(first_hop.intermediate_states)
                dst_match = len(check_states.intersection(allowed_states)) > 0
                if is_first_hop and not dst_match:
                    logger.debug(
                        f"          [RULE_MATCHER] Rule {rule.rule_id} REJECTED: "
                        f"multi-hop intermediate_states check failed. dst_states={dst_states}, "
                        f"allowed_states={allowed_states}"
                    )
            else:
                # No intermediate_states constraint, accept any destination
                dst_match = len(dst_states) > 0
                if is_first_hop and not dst_match:
                    logger.debug(
                        f"          [RULE_MATCHER] Rule {rule.rule_id} REJECTED: "
                        f"multi-hop rule requires dst to have states, but dst_states is empty!"
                    )
        else:
            # Standard matching: accept if no states or states match
            dst_match = len(dst_states) == 0 or len(dst_states.intersection(set(rule.possible_dst_states))) > 0

        if is_first_hop and dst_match:
            logger.debug(f"          [RULE_MATCHER] Rule {rule.rule_id} ACCEPTED: src_match=True, dst_match=True")

        return dst_match

    def _get_edge_between(
        self,
        graph: HyperGraph,
        src_id: int,
        dst_id: int,
    ) -> tuple[Edge | None, PropagationDirection | None]:
        """Get edge data and direction between two nodes.

        Checks both forward (src -> dst) and backward (dst -> src) directions.

        Args:
            graph: The graph containing the nodes
            src_id: Source node ID
            dst_id: Destination node ID

        Returns:
            Tuple of (Edge, PropagationDirection) or (None, None) if no edge
        """
        # Check FORWARD direction (src -> dst)
        if graph._graph.has_edge(src_id, dst_id):
            edge_attrs = graph._graph.get_edge_data(src_id, dst_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.FORWARD

        # Check BACKWARD direction (dst -> src)
        if graph._graph.has_edge(dst_id, src_id):
            edge_attrs = graph._graph.get_edge_data(dst_id, src_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.BACKWARD

        return None, None

    def _get_edge_data(
        self,
        graph: HyperGraph,
        src_id: int,
        dst_id: int,
        direction: PropagationDirection,
    ) -> Edge | None:
        """Get edge data for a specific direction.

        Args:
            graph: The graph containing the nodes
            src_id: Source node ID
            dst_id: Destination node ID
            direction: Expected direction

        Returns:
            Edge data or None if not found
        """
        if direction == PropagationDirection.FORWARD:
            if graph._graph.has_edge(src_id, dst_id):
                edge_attrs = graph._graph.get_edge_data(src_id, dst_id)
                if edge_attrs:
                    edge_data: Edge | None = list(edge_attrs.values())[0].get("ref")
                    return edge_data
        else:  # BACKWARD
            if graph._graph.has_edge(dst_id, src_id):
                edge_attrs = graph._graph.get_edge_data(dst_id, src_id)
                if edge_attrs:
                    edge_data = list(edge_attrs.values())[0].get("ref")
                    return edge_data
        return None
