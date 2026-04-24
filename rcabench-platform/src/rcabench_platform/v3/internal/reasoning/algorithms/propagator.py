"""Slim FaultPropagator coordinator using extracted modules.

This module provides the FaultPropagator class which coordinates:
- StateEnhancer: Propagates container restart states to spans
- RuleMatcher: Efficient rule lookup and matching
- TopologyExplorer: BFS/DFS graph traversal
- TemporalValidator: Temporal causality validation

This is a refactored version of the original forward_propagator.py that
uses extracted, testable components for better maintainability.
"""

import hashlib
import json
import logging
from collections import Counter
from datetime import datetime
from pathlib import Path

from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.algorithms.state_enhancer import (
    CONTAINER_RESTART_TO_MISSING_SPAN,
    INJECTION_POINT_TO_SPAN_AFFECTED,
    POD_KILLED_TO_CONTAINER_RESTARTING,
    InjectionPointEnhancement,
    StateEnhancer,
)
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import TemporalValidator
from rcabench_platform.v3.internal.reasoning.algorithms.topology_explorer import TopologyExplorer
from rcabench_platform.v3.internal.reasoning.models.graph import Edge, HyperGraph, Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationPath, PropagationResult
from rcabench_platform.v3.internal.reasoning.models.state import StateWindow
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationDirection, PropagationRule

logger = logging.getLogger(__name__)


class RuleUsageStats:
    """Track rule usage statistics during propagation."""

    def __init__(self) -> None:
        self.rule_counts: Counter[str] = Counter()

    def record_rule_use(self, rule_id: str) -> None:
        """Record that a rule was applied."""
        self.rule_counts[rule_id] += 1

    def save_to_file(self, filepath: Path | str) -> None:
        """Save statistics to a JSON file."""
        filepath = Path(filepath)
        stats = {
            "total_applications": sum(self.rule_counts.values()),
            "unique_rules_used": len(self.rule_counts),
            "rule_usage": dict(self.rule_counts.most_common()),
        }
        filepath.write_text(json.dumps(stats, indent=2))

    def get_summary(self) -> str:
        """Get a summary string of rule usage."""
        lines = ["Rule Usage Statistics:"]
        for rule_id, count in self.rule_counts.most_common():
            lines.append(f"  {rule_id}: {count}")
        return "\n".join(lines)


class FaultPropagator:
    """Bidirectional fault propagation analyzer using extracted components.

    This coordinator class uses:
    - StateEnhancer for state propagation (e.g., container restart -> MISSING_SPAN)
    - RuleMatcher for efficient rule lookup and matching
    - TopologyExplorer for BFS/DFS graph traversal
    - TemporalValidator for temporal causality validation
    """

    def __init__(
        self,
        graph: HyperGraph,
        rules: list[PropagationRule],
        max_hops: int = 5,
    ) -> None:
        """Initialize the fault propagator.

        Args:
            graph: The hypergraph containing topology (with node states already populated)
            rules: List of propagation rules
            max_hops: Maximum propagation hops (default 5)
        """
        self.graph = graph
        self.rules = rules
        self.max_hops = max_hops

        # Create and store state enhancer for later use
        # Order matters: Pod KILLED -> Container RESTARTING -> Span MISSING_SPAN
        self.state_enhancer = StateEnhancer(self.graph)
        self.state_enhancer.register(POD_KILLED_TO_CONTAINER_RESTARTING)
        self.state_enhancer.register(CONTAINER_RESTART_TO_MISSING_SPAN)
        self.state_enhancer.apply_all()

        # Build rule matcher with indices
        self.rule_matcher = RuleMatcher(rules)
        # Keep rule_index reference for backward compatibility
        self.rule_index = self.rule_matcher.rule_index

        # Create topology explorer
        self.topology_explorer = TopologyExplorer(graph, max_hops)

        # Create temporal validator
        self.temporal_validator = TemporalValidator(self.graph)

        # Initialize rule usage statistics
        self.rule_stats = RuleUsageStats()

    def propagate_from_injection(
        self,
        injection_node_ids: list[int],
        alarm_nodes: set[int],
        fault_category: str | None = None,
        injection_service_id: int | None = None,
        injection_point_enhancement: InjectionPointEnhancement | None = None,
        source_service_id: int | None = None,
        target_service_id: int | None = None,
        fault_direction: str | None = None,
    ) -> PropagationResult:
        """Propagate fault from injection nodes to alarm nodes.

        Args:
            injection_node_ids: List of injection node IDs
            alarm_nodes: Set of alarm node IDs to reach
            fault_category: Optional fault category for injection point enhancement
            injection_service_id: Optional service ID where fault was injected
            injection_point_enhancement: Optional custom enhancement (defaults to builtin)
            source_service_id: For network/DNS faults, the source service ID
            target_service_id: For network/DNS faults, the target service ID
            fault_direction: For network faults, the direction ("to", "from", "both")

        Returns:
            PropagationResult with valid paths
        """
        # Apply injection point enhancement if applicable
        if fault_category and injection_service_id is not None:
            enhancement = injection_point_enhancement or INJECTION_POINT_TO_SPAN_AFFECTED
            self.state_enhancer.apply_injection_point_enhancement(
                injection_service_id=injection_service_id,
                fault_category=fault_category,
                enhancement=enhancement,
                source_service_id=source_service_id,
                target_service_id=target_service_id,
                fault_direction=fault_direction,
            )

        for injection_node_id in injection_node_ids:
            injection_node = self.graph.get_node_by_id(injection_node_id)
            if injection_node is None:
                raise ValueError(f"Injection node {injection_node_id} not found in graph")

        # Use edge filter based on rule matching
        def edge_filter(src_id: int, dst_id: int, is_first_hop: bool) -> bool:
            """Initial screening filter for BFS topology exploration.

            Strategy: Recall-first for multi-hop rules - an edge passes if it matches
            ANY hop of a multi-hop rule (not just the first hop). This prevents premature
            pruning during BFS. Complete path validation happens later in extract_paths().
            """
            src_states = self._get_states_for_node(src_id)
            dst_states = self._get_states_for_node(dst_id)
            src_node = self.graph.get_node_by_id(src_id)
            dst_node = self.graph.get_node_by_id(dst_id)

            result = self.rule_matcher.edge_matches_any_rule(
                src_id, dst_id, self.graph, src_states, dst_states, is_first_hop
            )

            # Debug: log span->span edges to diagnose propagation issues
            if src_node and dst_node and src_node.kind == PlaceKind.span and dst_node.kind == PlaceKind.span:
                if "injection_affected" in src_states or not result:
                    logger.debug(
                        f"    [EDGE_FILTER] span->span: {src_node.self_name} -> {dst_node.self_name}, "
                        f"src_states={src_states}, dst_states={dst_states}, result={result}"
                    )

            if is_first_hop:
                logger.debug(
                    f"    [EDGE_FILTER] is_first_hop={is_first_hop}, "
                    f"src={src_node.kind.value if src_node else 'None'}:{src_node.uniq_name if src_node else 'None'}, "
                    f"dst={dst_node.kind.value if dst_node else 'None'}:{dst_node.uniq_name if dst_node else 'None'}, "
                    f"src_states={src_states}, dst_states={dst_states}, result={result}"
                )

            return result

        # Find reachable subgraph using TopologyExplorer
        subgraph_edges = self.topology_explorer.find_reachable_subgraph(injection_node_ids, alarm_nodes, edge_filter)

        warnings: list[str] = []

        if len(subgraph_edges) == 0:
            warning_msg = f"No reachable edges found from injection nodes {injection_node_ids}"
            warnings.append(warning_msg)
            logger.warning(f"  [WARNING] {warning_msg}")
            # Return early with empty result
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=[],
                warnings=warnings,
            )

        logger.debug(f"  [DEBUG] Subgraph has {len(subgraph_edges)} edges")

        # Extract paths from subgraph
        all_topology_paths = self.topology_explorer.extract_paths(subgraph_edges, injection_node_ids, alarm_nodes)

        if len(all_topology_paths) == 0:
            warning_msg = f"No paths extracted from reachable subgraph ({len(subgraph_edges)} edges available)"
            warnings.append(warning_msg)
            logger.warning(f"  [WARNING] {warning_msg}")
            # Return with subgraph but no paths
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=subgraph_edges,
                warnings=warnings,
            )

        logger.debug(f"  [DEBUG] Extracted {len(all_topology_paths)} paths")
        for node_path in all_topology_paths:
            logger.debug(f"    [DEBUG]   Path: {self._format_path_debug(node_path)}")

        valid_paths: list[PropagationPath] = []
        visited_nodes: set[int] = set()
        max_hops = 0

        for node_ids in all_topology_paths:
            visited_nodes.update(node_ids)
            max_hops = max(max_hops, len(node_ids) - 1)

            path = self._verify_and_build_path(node_ids)
            if path is not None:
                valid_paths.append(path)

        # Log and save rule usage statistics
        if self.rule_stats.rule_counts:
            logger.info(self.rule_stats.get_summary())

            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            injection_hash = hashlib.md5(str(sorted(injection_node_ids)).encode()).hexdigest()[:8]
            stat_dir = Path("output/stat") / f"{timestamp}-{injection_hash}"
            stat_dir.mkdir(parents=True, exist_ok=True)
            stat_file = stat_dir / "rule_stats.json"
            self.rule_stats.save_to_file(stat_file)
            logger.info(f"Saved rule statistics to {stat_file}")

        return PropagationResult(
            injection_node_ids=injection_node_ids,
            injection_states=["unknown"] * len(injection_node_ids),
            paths=valid_paths,
            visited_nodes=visited_nodes,
            max_hops_reached=max_hops,
            subgraph_edges=subgraph_edges,
            warnings=warnings,
        )

    def _get_states_for_node(self, node_id: int) -> set[str]:
        """Get all states for a node from graph.node.state_timeline."""
        node = self.graph.get_node_by_id(node_id)
        if node and node.state_timeline:
            states: set[str] = set()
            for window in node.state_timeline:
                states.update(window.states)
            return states
        return set()

    def _get_node_states(self, node_id: int) -> list[StateWindow]:
        """Get state timeline for a node from graph.node.state_timeline."""
        node = self.graph.get_node_by_id(node_id)
        if node and node.state_timeline:
            return node.state_timeline
        return []

    def _format_path_debug(self, node_ids: list[int]) -> str:
        """Format node IDs for debug output."""
        parts = []
        for nid in node_ids:
            node = self.graph.get_node_by_id(nid)
            if node:
                parts.append(f"{node.kind.value}:{node.self_name}")
            else:
                parts.append(f"?:{nid}")
        return " -> ".join(parts)

    # ========================================================================
    # Path verification methods
    # These methods are imported from the original forward_propagator.py
    # to maintain backward compatibility with tests
    # ========================================================================

    def _verify_and_build_path(self, node_ids: list[int]) -> PropagationPath | None:
        """Verify and build a propagation path from node IDs.

        This method coordinates:
        1. Multi-hop rule matching via RuleMatcher
        2. Temporal validation via TemporalValidator
        3. Single-hop fallback verification

        Args:
            node_ids: List of node IDs forming the path

        Returns:
            PropagationPath if valid, None otherwise
        """
        if len(node_ids) < 2:
            return None

        # First, check if this path matches any multi-hop rules
        multi_hop_rule = self._find_matching_multi_hop_rule(node_ids)
        if multi_hop_rule is not None:
            path = self._verify_multi_hop_path(node_ids, multi_hop_rule)
            if path is not None:
                return path

        # Fall back to hop-by-hop verification
        nodes: list[int] = []
        states: list[list[str]] = []
        edges: list[str] = []
        rules: list[str] = []
        state_start_times: list[int | None] = []
        propagation_delays: list[float] = []

        i = 0
        while i < len(node_ids) - 1:
            # Try to match a multi-hop rule starting at this position
            multi_hop_matched = False
            for rule in self.rules:
                if not rule.is_multi_hop or not rule.path:
                    continue

                rule_node_count = len(rule.path) + 1
                if i + rule_node_count > len(node_ids):
                    continue

                sub_path = node_ids[i : i + rule_node_count]
                logger.debug(
                    f"    [DEBUG] Trying multi-hop rule {rule.rule_id} at position {i}, "
                    f"sub_path length={len(sub_path)}, path={self._format_path_debug(sub_path)}"
                )
                if self._matches_multi_hop_rule(rule, sub_path):
                    prev_time = state_start_times[-1] if state_start_times else None
                    verified = self._verify_multi_hop_subpath(
                        sub_path, rule, prev_start_time=prev_time, is_first_hop=(i == 0)
                    )
                    if verified is not None:
                        (
                            sub_nodes,
                            sub_states,
                            sub_edges,
                            sub_rules,
                            sub_times,
                            sub_delays,
                        ) = verified

                        if i == 0:
                            nodes.extend(sub_nodes)
                            states.extend(sub_states)
                        else:
                            nodes.extend(sub_nodes[1:])
                            states.extend(sub_states[1:])

                        edges.extend(sub_edges)
                        rules.extend(sub_rules)

                        if i == 0:
                            state_start_times.extend(sub_times)
                        else:
                            state_start_times.extend(sub_times[1:])

                        propagation_delays.extend(sub_delays)

                        # After matching [i, i+1, ..., i+rule_node_count-1], next hop starts at i+rule_node_count-1
                        # e.g., matched [A,B,C] (i=0, rule_node_count=3), next check C->D (i=2)
                        i += rule_node_count - 1
                        multi_hop_matched = True
                        break

            if multi_hop_matched:
                continue

            # Fall back to single-hop verification
            hop_result = self._verify_single_hop(
                hop_index=i,
                src_id=node_ids[i],
                dst_id=node_ids[i + 1],
                prev_start_time=state_start_times[-1] if state_start_times else None,
                is_first_hop=(i == 0),
            )
            if hop_result is None:
                self._log_verification_failure(
                    node_ids=node_ids,
                    failed_hop_index=i,
                    matched_nodes=nodes,
                    matched_edges=edges,
                    matched_rules=rules,
                )
                return None

            (
                hop_src_id,
                hop_dst_id,
                hop_src_states,
                hop_dst_states,
                hop_src_time,
                hop_dst_time,
                hop_edge_desc,
                hop_rule_id,
                hop_delay,
            ) = hop_result

            if i == 0:
                nodes.append(hop_src_id)
                states.append(hop_src_states)
                state_start_times.append(hop_src_time)

            nodes.append(hop_dst_id)
            states.append(hop_dst_states)
            state_start_times.append(hop_dst_time)
            edges.append(hop_edge_desc)
            rules.append(hop_rule_id)
            propagation_delays.append(hop_delay)

            i += 1

        return PropagationPath(
            nodes=nodes,
            states=states,
            edges=edges,
            rules=rules,
            confidence=1.0,
            state_start_times=state_start_times,
            propagation_delays=propagation_delays,
        )

    def _find_matching_multi_hop_rule(self, node_ids: list[int]) -> PropagationRule | None:
        """Find a multi-hop rule matching the entire path."""
        return self.rule_matcher.find_matching_multi_hop_rule(node_ids, self.graph)

    def _matches_multi_hop_rule(self, rule: PropagationRule, node_ids: list[int]) -> bool:
        """Check if node_ids matches a multi-hop rule structure."""
        matches = self.rule_matcher.matches_multi_hop_rule(rule, node_ids, self.graph)
        if not matches:
            logger.debug(
                f"      [DEBUG] Multi-hop rule {rule.rule_id} did NOT match path: {self._format_path_debug(node_ids)}"
            )
        else:
            logger.debug(
                f"      [DEBUG] Multi-hop rule {rule.rule_id} MATCHED path: {self._format_path_debug(node_ids)}"
            )
        return matches

    def _verify_multi_hop_path(self, node_ids: list[int], rule: PropagationRule) -> PropagationPath | None:
        """Verify a multi-hop path against a rule with temporal validation."""
        if not rule.path or len(node_ids) != len(rule.path) + 1:
            return None

        src_id = node_ids[0]
        src_timeline = self._get_node_states(src_id)
        src_states_all: set[str] = set()
        for window in src_timeline:
            src_states_all.update(window.states)

        src_node = self.graph.get_node_by_id(src_id)
        if src_node is None:
            return None

        # Find first window with matching src_states
        rule_src_states = set(rule.src_states)
        src_matching_window = None
        if rule_src_states and src_timeline:
            for window in src_timeline:
                if window.states.intersection(rule_src_states):
                    src_matching_window = window
                    break

            if src_matching_window is None:
                logger.debug(
                    f"    [DEBUG] Multi-hop path rejected: source {src_node.self_name} "
                    f"never has required states {rule_src_states}, actual: {src_states_all}"
                )
                return None

        # Initialize tracking
        nodes: list[int] = [src_id]
        if src_matching_window:
            states: list[list[str]] = [sorted(src_matching_window.states)]
        else:
            states = [sorted(src_states_all) if src_states_all else ["unknown"]]
        edges_list: list[str] = []
        rules_list: list[str] = []
        state_start_times: list[int | None] = []
        propagation_delays: list[float] = []

        # Get start time for source
        if src_matching_window:
            src_start_time = src_matching_window.start_time
        elif src_timeline:
            src_start_time = src_timeline[0].start_time
        else:
            src_start_time = 0
            # Find earliest state start time from all nodes in graph
            all_starts = [
                node.state_timeline[0].start_time for node in self.graph._node_id_map.values() if node.state_timeline
            ]
            if all_starts:
                src_start_time = min(all_starts)

        state_start_times.append(src_start_time)
        current_start_time = src_start_time

        # Validate each hop
        for hop_idx, path_hop in enumerate(rule.path):
            current_node_id = node_ids[hop_idx]
            next_node_id = node_ids[hop_idx + 1]

            edge_data, direction = self._get_edge_between(current_node_id, next_node_id)
            if edge_data is None or direction is None:
                return None

            if edge_data.kind != path_hop.edge_kind or direction != path_hop.direction:
                return None

            edge_desc = f"{edge_data.kind.value}_{direction.value}"

            next_node = self.graph.get_node_by_id(next_node_id)
            if next_node is None:
                return None

            next_timeline = self._get_node_states(next_node_id)
            next_states_all: set[str] = set()
            for window in next_timeline:
                next_states_all.update(window.states)

            # Check intermediate_states constraint
            is_last_hop = hop_idx == len(rule.path) - 1
            if not is_last_hop:
                if path_hop.intermediate_states is not None:
                    check_states = next_states_all if next_states_all else {"unknown"}
                    allowed_states = set(path_hop.intermediate_states)
                    if not check_states.intersection(allowed_states):
                        logger.debug(
                            f"    [DEBUG] Multi-hop rejected: intermediate {next_node.self_name} "
                            f"has {check_states}, allowed: {allowed_states}"
                        )
                        return None
            else:
                # Last hop: check possible_dst_states
                dst_states_set = set(rule.possible_dst_states)
                if dst_states_set and next_states_all:
                    if not next_states_all.intersection(dst_states_set):
                        logger.debug(
                            f"    [DEBUG] Multi-hop rejected: dst {next_node.self_name} "
                            f"has {next_states_all}, required: {dst_states_set}"
                        )
                        return None

            # Temporal validation using TemporalValidator
            causal_window = self.temporal_validator.find_causal_window(next_node_id, current_start_time)
            if causal_window is not None:
                delay = float(causal_window.start_time - current_start_time)
                next_start_time = causal_window.start_time
            else:
                delay = 0.0
                next_start_time = current_start_time

            # Update tracking
            nodes.append(next_node_id)
            states.append(sorted(next_states_all) if next_states_all else ["unknown"])
            edges_list.append(edge_desc)
            rules_list.append(rule.rule_id)
            state_start_times.append(next_start_time)
            propagation_delays.append(delay)

            current_start_time = next_start_time

        # Record rule usage
        self.rule_stats.record_rule_use(rule.rule_id)

        return PropagationPath(
            nodes=nodes,
            states=states,
            edges=edges_list,
            rules=rules_list,
            confidence=rule.confidence,
            state_start_times=state_start_times,
            propagation_delays=propagation_delays,
        )

    def _verify_multi_hop_subpath(
        self,
        node_ids: list[int],
        rule: PropagationRule,
        prev_start_time: int | None,
        is_first_hop: bool,
    ) -> (
        tuple[
            list[int],
            list[list[str]],
            list[str],
            list[str],
            list[int | None],
            list[float],
        ]
        | None
    ):
        """Verify a multi-hop sub-path within a larger path."""
        if not rule.path or len(node_ids) != len(rule.path) + 1:
            return None

        src_id = node_ids[0]
        src_timeline = self._get_node_states(src_id)
        src_states_all: set[str] = set()
        for window in src_timeline:
            src_states_all.update(window.states)

        src_node = self.graph.get_node_by_id(src_id)
        if src_node is None:
            return None

        rule_src_states = set(rule.src_states)
        src_matching_window = None

        if is_first_hop and rule_src_states and src_timeline:
            for window in src_timeline:
                if window.states.intersection(rule_src_states):
                    src_matching_window = window
                    break

            if src_matching_window is None:
                logger.debug(
                    f"    [DEBUG] Multi-hop subpath rejected: source {src_node.self_name} "
                    f"never has required states {rule_src_states}, actual: {src_states_all}"
                )
                return None
        elif rule_src_states and src_states_all:
            # Not first hop - still check src_states
            if not src_states_all.intersection(rule_src_states):
                logger.debug(
                    f"    [DEBUG] Multi-hop subpath rejected: source {src_node.self_name} "
                    f"lacks required states {rule_src_states}, actual: {src_states_all}"
                )
                return None

        nodes: list[int] = [src_id]
        if src_matching_window:
            states: list[list[str]] = [sorted(src_matching_window.states)]
        else:
            states = [sorted(src_states_all) if src_states_all else ["unknown"]]
        edges_list: list[str] = []
        rules_list: list[str] = []
        state_start_times: list[int | None] = []
        propagation_delays: list[float] = []

        # Get start time for source
        if src_matching_window:
            src_start_time = src_matching_window.start_time
        elif prev_start_time is not None:
            causal_window = self.temporal_validator.find_causal_window(src_id, prev_start_time)
            src_start_time = causal_window.start_time if causal_window else prev_start_time
        elif src_timeline:
            src_start_time = src_timeline[0].start_time
        else:
            src_start_time = 0
            # Find earliest state start time from all nodes in graph
            all_starts = [
                node.state_timeline[0].start_time for node in self.graph._node_id_map.values() if node.state_timeline
            ]
            if all_starts:
                src_start_time = min(all_starts)

        state_start_times.append(src_start_time)
        current_start_time = src_start_time

        for hop_idx, path_hop in enumerate(rule.path):
            current_node_id = node_ids[hop_idx]
            next_node_id = node_ids[hop_idx + 1]

            edge_data, direction = self._get_edge_between(current_node_id, next_node_id)
            if edge_data is None or direction is None:
                return None

            if edge_data.kind != path_hop.edge_kind or direction != path_hop.direction:
                return None

            edge_desc = f"{edge_data.kind.value}_{direction.value}"

            next_node = self.graph.get_node_by_id(next_node_id)
            if next_node is None:
                return None

            next_timeline = self._get_node_states(next_node_id)
            next_states_all: set[str] = set()
            for window in next_timeline:
                next_states_all.update(window.states)

            is_last_hop = hop_idx == len(rule.path) - 1
            if not is_last_hop:
                if path_hop.intermediate_states is not None:
                    check_states = next_states_all if next_states_all else {"unknown"}
                    allowed_states = set(path_hop.intermediate_states)
                    if not check_states.intersection(allowed_states):
                        return None

            causal_window = self.temporal_validator.find_causal_window(next_node_id, current_start_time)
            if causal_window is not None:
                delay = float(causal_window.start_time - current_start_time)
                next_start_time = causal_window.start_time
            else:
                delay = 0.0
                next_start_time = current_start_time

            nodes.append(next_node_id)
            states.append(sorted(next_states_all) if next_states_all else ["unknown"])
            edges_list.append(edge_desc)
            rules_list.append(rule.rule_id)
            state_start_times.append(next_start_time)
            propagation_delays.append(delay)

            current_start_time = next_start_time

        self.rule_stats.record_rule_use(rule.rule_id)

        return nodes, states, edges_list, rules_list, state_start_times, propagation_delays

    def _verify_single_hop(
        self,
        hop_index: int,
        src_id: int,
        dst_id: int,
        prev_start_time: int | None,
        is_first_hop: bool,
    ) -> tuple[int, int, list[str], list[str], int | None, int | None, str, str, float] | None:
        """Verify a single hop in the propagation path."""
        src_node = self.graph.get_node_by_id(src_id)
        dst_node = self.graph.get_node_by_id(dst_id)

        if src_node is None or dst_node is None:
            return None

        edge_data, direction = self._get_edge_between(src_id, dst_id)
        if edge_data is None or direction is None:
            return None

        rule_key = (src_node.kind, edge_data.kind, direction)
        matching_rules = self.rule_index.get(rule_key, [])

        valid_rules: list[PropagationRule] = []
        for r in matching_rules:
            if r.is_multi_hop:
                first_hop = r.path[0]  # type: ignore[index]
                if first_hop.intermediate_kind == dst_node.kind:
                    valid_rules.append(r)
            else:
                if r.dst_kind == dst_node.kind:
                    valid_rules.append(r)

        if not valid_rules:
            return None

        edge_desc = f"{edge_data.kind.value}_{direction.value}"

        for rule in valid_rules:
            if is_first_hop:
                result = self._process_first_hop(hop_index, src_id, dst_id, edge_desc, rule)
            else:
                result = self._process_subsequent_hop(
                    hop_index, src_id, dst_id, src_node, dst_node, edge_desc, rule, prev_start_time
                )

            if result is not None:
                return result

        return None

    def _process_first_hop(
        self,
        hop_index: int,
        src_id: int,
        dst_id: int,
        edge_desc: str,
        rule: PropagationRule,
    ) -> tuple[int, int, list[str], list[str], int | None, int | None, str, str, float] | None:
        """Process the first hop from injection node."""
        src_node = self.graph.get_node_by_id(src_id)
        if src_node is None:
            return None

        src_timeline = self._get_node_states(src_id)
        src_states_all: set[str] = set()
        for window in src_timeline:
            src_states_all.update(window.states)

        dst_timeline = self._get_node_states(dst_id)
        dst_states_all: set[str] = set()
        for window in dst_timeline:
            dst_states_all.update(window.states)

        # Check source state requirement for span-direct starts
        if src_node.kind == PlaceKind.span:
            rule_src_states = set(rule.src_states)
            if rule_src_states and not src_states_all.intersection(rule_src_states):
                return None

        # Destination must have some states
        if not dst_states_all:
            return None

        # Get source time
        if src_timeline:
            src_time: int | None = src_timeline[0].start_time
        else:
            src_time = None

        # Get destination time using temporal validator
        if src_time is not None:
            causal_window = self.temporal_validator.find_causal_window(dst_id, src_time)
            if causal_window is not None:
                dst_time: int | None = causal_window.start_time
                delay = float(causal_window.start_time - src_time)
            else:
                dst_time = None
                delay = 0.0
        else:
            if dst_timeline:
                dst_time = dst_timeline[0].start_time
            else:
                dst_time = None
            delay = 0.0

        self.rule_stats.record_rule_use(rule.rule_id)

        return (
            src_id,
            dst_id,
            sorted(src_states_all) if src_states_all else ["unknown"],
            sorted(dst_states_all),
            src_time,
            dst_time,
            edge_desc,
            rule.rule_id,
            delay,
        )

    def _process_subsequent_hop(
        self,
        hop_index: int,
        src_id: int,
        dst_id: int,
        src_node: Node,
        dst_node: Node,
        edge_desc: str,
        rule: PropagationRule,
        prev_start_time: int | None,
    ) -> tuple[int, int, list[str], list[str], int | None, int | None, str, str, float] | None:
        """Process a subsequent hop in the path."""
        src_timeline = self._get_node_states(src_id)
        src_states_all: set[str] = set()
        for window in src_timeline:
            src_states_all.update(window.states)

        dst_timeline = self._get_node_states(dst_id)
        dst_states_all: set[str] = set()
        for window in dst_timeline:
            dst_states_all.update(window.states)

        # Check source states match rule
        rule_src_states = set(rule.src_states)
        if rule_src_states and not src_states_all.intersection(rule_src_states):
            return None

        # Check destination states match rule
        rule_dst_states = set(rule.possible_dst_states)
        if rule_dst_states and not dst_states_all.intersection(rule_dst_states):
            return None

        # Temporal validation
        if prev_start_time is not None:
            causal_window = self.temporal_validator.find_causal_window(dst_id, prev_start_time)
            if causal_window is not None:
                dst_time: int | None = causal_window.start_time
                delay = float(causal_window.start_time - prev_start_time)
            else:
                # No causal window found
                return None
        else:
            if dst_timeline:
                dst_time = dst_timeline[0].start_time
            else:
                dst_time = None
            delay = 0.0

        src_time = prev_start_time

        self.rule_stats.record_rule_use(rule.rule_id)

        return (
            src_id,
            dst_id,
            sorted(src_states_all) if src_states_all else ["unknown"],
            sorted(dst_states_all) if dst_states_all else ["unknown"],
            src_time,
            dst_time,
            edge_desc,
            rule.rule_id,
            delay,
        )

    def _get_edge_between(self, src_id: int, dst_id: int) -> tuple[Edge | None, PropagationDirection | None]:
        """Get edge data and direction between two nodes."""
        # Check FORWARD direction (src -> dst)
        if self.graph._graph.has_edge(src_id, dst_id):
            edge_attrs = self.graph._graph.get_edge_data(src_id, dst_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.FORWARD

        # Check BACKWARD direction (dst -> src)
        if self.graph._graph.has_edge(dst_id, src_id):
            edge_attrs = self.graph._graph.get_edge_data(dst_id, src_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.BACKWARD

        return None, None

    def _log_verification_failure(
        self,
        node_ids: list[int],
        failed_hop_index: int,
        matched_nodes: list[int],
        matched_edges: list[str],
        matched_rules: list[str],
    ) -> None:
        """Log detailed diagnostic information when path verification fails."""
        logger.debug(f"    [DEBUG] Path verification failed at hop {failed_hop_index}")

        full_path = self._format_path_debug(node_ids)
        logger.debug(f"    [DEBUG]   Intended path: {full_path}")

        if matched_nodes:
            matched_path = self._format_path_debug(matched_nodes)
            logger.debug(f"    [DEBUG]   Matched ({len(matched_nodes)} nodes): {matched_path}")
            if matched_edges:
                logger.debug(f"    [DEBUG]   Edges: {' -> '.join(matched_edges)}")
            if matched_rules:
                logger.debug(f"    [DEBUG]   Rules: {' -> '.join(matched_rules)}")
        else:
            logger.debug("    [DEBUG]   No hops matched yet (failed at first hop)")

        src_node = self.graph.get_node_by_id(node_ids[failed_hop_index])
        dst_node = self.graph.get_node_by_id(node_ids[failed_hop_index + 1])
        if src_node and dst_node:
            logger.debug(
                f"    [DEBUG]   Failed hop: {src_node.kind.value}:{src_node.self_name} -> "
                f"{dst_node.kind.value}:{dst_node.self_name}"
            )
