"""Rule matching against canonical-state node timelines.

Rule predicates speak the canonical-state vocabulary defined in
``ir/states.py`` (``SLOW``, ``ERRORING``, ``DEGRADED``, ``UNAVAILABLE``,
``MISSING``, ``HEALTHY``, ``UNKNOWN``). Specialization labels travel via
``Evidence.specialization_labels`` and are matched separately when a rule
opts in via the ``required_labels`` predicate.
"""

from __future__ import annotations

import logging

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.schema import FirstHopConfig, PropagationDirection, PropagationRule

logger = logging.getLogger(__name__)


# Default first-hop semantics keyed by source PlaceKind. Service is a dummy
# aggregation node so it never requires src states; container/pod/span require
# the seed state to actually be present (the IR seeds it via InjectionAdapter).
DEFAULT_FIRST_HOP_CONFIGS: dict[PlaceKind, FirstHopConfig] = {
    PlaceKind.span: FirstHopConfig(
        require_src_states=True,
        require_dst_states=True,
        lenient_dst_state_match=True,
    ),
    PlaceKind.service: FirstHopConfig(
        require_src_states=False,
        require_dst_states=True,
        lenient_dst_state_match=True,
    ),
    PlaceKind.container: FirstHopConfig(
        require_src_states=False,
        require_dst_states=False,
        lenient_dst_state_match=True,
    ),
    PlaceKind.pod: FirstHopConfig(
        require_src_states=False,
        require_dst_states=False,
        lenient_dst_state_match=True,
    ),
}


class RuleMatcher:
    """Match propagation rules against canonical-state node observations."""

    def __init__(self, rules: list[PropagationRule]):
        self.rules = rules
        self._build_rule_indices()

    def _build_rule_indices(self) -> None:
        self.rule_index: dict[
            tuple[PlaceKind, DepKind | None, PropagationDirection | None],
            list[PropagationRule],
        ] = {}
        for rule in self.rules:
            if rule.path:
                first_hop = rule.path[0]
                key: tuple[PlaceKind, DepKind | None, PropagationDirection | None] = (
                    rule.src_kind,
                    first_hop.edge_kind,
                    first_hop.direction,
                )
            else:
                key = (rule.src_kind, rule.edge_kind, rule.direction)
            self.rule_index.setdefault(key, []).append(rule)

    def get_rules_for_edge(
        self,
        src_kind: PlaceKind,
        edge_kind: DepKind,
        direction: PropagationDirection,
    ) -> list[PropagationRule]:
        return self.rule_index.get((src_kind, edge_kind, direction), [])

    def matches_multi_hop_rule(
        self,
        rule: PropagationRule,
        topology_path: list[int],
        graph: HyperGraph,
    ) -> bool:
        if not rule.path:
            return False
        if len(topology_path) != len(rule.path) + 1:
            return False

        for hop_idx, path_hop in enumerate(rule.path):
            src_node_id = topology_path[hop_idx]
            dst_node_id = topology_path[hop_idx + 1]
            src_node = graph.get_node_by_id(src_node_id)
            dst_node = graph.get_node_by_id(dst_node_id)
            if src_node is None or dst_node is None:
                return False

            if hop_idx < len(rule.path) - 1:
                if path_hop.intermediate_kind and dst_node.kind != path_hop.intermediate_kind:
                    return False
            else:
                if dst_node.kind != rule.dst_kind:
                    return False

            edge_data = self._get_edge_data(graph, src_node_id, dst_node_id, path_hop.direction)
            if edge_data is None:
                return False
            if edge_data.kind != path_hop.edge_kind:
                return False
            if path_hop.edge_condition and not path_hop.edge_condition(edge_data):
                return False

        return True

    def find_matching_multi_hop_rule(
        self,
        topology_path: list[int],
        graph: HyperGraph,
    ) -> PropagationRule | None:
        if len(topology_path) < 2:
            return None
        src_node = graph.get_node_by_id(topology_path[0])
        if src_node is None:
            return None
        for rule in self.rules:
            if not rule.is_multi_hop:
                continue
            if rule.src_kind != src_node.kind:
                continue
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
        src_node = graph.get_node_by_id(src_node_id)
        dst_node = graph.get_node_by_id(dst_node_id)
        if src_node is None or dst_node is None:
            return []

        edge_data, direction = self._get_edge_between(graph, src_node_id, dst_node_id)
        if edge_data is None or direction is None:
            return []

        matching_rules: list[PropagationRule] = []
        rule_key = (src_node.kind, edge_data.kind, direction)
        for rule in self.rule_index.get(rule_key, []):
            if self._rule_matches_edge(
                rule,
                src_node.kind,
                dst_node.kind,
                src_states,
                dst_states,
                is_first_hop,
                is_first_hop_of_rule=True,
            ):
                matching_rules.append(rule)

        for rule in self.rules:
            if not rule.is_multi_hop or not rule.path:
                continue
            for hop_idx, path_hop in enumerate(rule.path):
                if hop_idx == 0:
                    continue
                if path_hop.edge_kind != edge_data.kind or path_hop.direction != direction:
                    continue
                if hop_idx < len(rule.path) - 1:
                    if path_hop.intermediate_kind and path_hop.intermediate_kind != dst_node.kind:
                        continue
                else:
                    if rule.dst_kind != dst_node.kind:
                        continue
                prev_hop = rule.path[hop_idx - 1]
                if prev_hop.intermediate_kind and prev_hop.intermediate_kind != src_node.kind:
                    continue
                if hop_idx == len(rule.path) - 1:
                    if dst_states and not dst_states.intersection(set(rule.possible_dst_states)):
                        continue
                if rule not in matching_rules:
                    matching_rules.append(rule)
                break

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
        if rule.first_hop_config is not None:
            return rule.first_hop_config
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
        if rule.is_multi_hop and rule.path and is_first_hop_of_rule:
            first_hop = rule.path[0]
            if first_hop.intermediate_kind != dst_kind:
                return False
        else:
            if rule.dst_kind != dst_kind:
                return False

        first_hop_config = self._get_first_hop_config(rule, src_kind) if is_first_hop else None

        if is_first_hop and first_hop_config:
            if first_hop_config.require_src_states:
                rule_src_states = set(rule.src_states)
                if rule_src_states:
                    src_match = bool(src_states.intersection(rule_src_states))
                else:
                    src_match = bool(src_states)
            else:
                src_match = True
        else:
            src_match = (not src_states) or bool(src_states.intersection(set(rule.src_states)))

        if not src_match:
            return False

        if is_first_hop and first_hop_config:
            if first_hop_config.require_dst_states:
                if first_hop_config.lenient_dst_state_match:
                    dst_match = bool(dst_states)
                else:
                    dst_match = bool(dst_states) and bool(dst_states.intersection(set(rule.possible_dst_states)))
            else:
                dst_match = True
        elif rule.is_multi_hop and rule.path and is_first_hop_of_rule:
            first_hop = rule.path[0]
            if first_hop.intermediate_states is not None:
                check_states = dst_states if dst_states else {"unknown"}
                dst_match = bool(check_states.intersection(set(first_hop.intermediate_states)))
            else:
                dst_match = bool(dst_states)
        else:
            dst_match = (not dst_states) or bool(dst_states.intersection(set(rule.possible_dst_states)))

        return dst_match

    def _get_edge_between(
        self,
        graph: HyperGraph,
        src_id: int,
        dst_id: int,
    ) -> tuple[Edge | None, PropagationDirection | None]:
        if graph._graph.has_edge(src_id, dst_id):
            edge_attrs = graph._graph.get_edge_data(src_id, dst_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.FORWARD
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
        if direction == PropagationDirection.FORWARD:
            if graph._graph.has_edge(src_id, dst_id):
                edge_attrs = graph._graph.get_edge_data(src_id, dst_id)
                if edge_attrs:
                    edge_data: Edge | None = list(edge_attrs.values())[0].get("ref")
                    return edge_data
        else:
            if graph._graph.has_edge(dst_id, src_id):
                edge_attrs = graph._graph.get_edge_data(dst_id, src_id)
                if edge_attrs:
                    edge_data = list(edge_attrs.values())[0].get("ref")
                    return edge_data
        return None
