"""Pure path construction over canonical-state timelines.

Given a topology-admitted node sequence, ``PathBuilder.build`` produces a
``CandidatePath`` carrying per-node picked window + per-edge picked rule.
It performs only **structural** validation (an edge exists between adjacent
nodes; some rule admits the (kind, edge_kind, direction) triple). Temporal
ordering, drift, and inject-time checks are the gates' responsibility.

Effective onset and EvidenceLevel awareness now live in
``TemporalValidator.find_admissible_window``: it returns ``(window,
effective_onset)`` directly, so this module no longer needs its own helpers
for the §7.5 admission predicate or the symbolic-onset fallback.
"""

from __future__ import annotations

from dataclasses import dataclass

from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import (
    TemporalValidator,
    _effective_onset,
)
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationDirection, PropagationRule


@dataclass(frozen=True, slots=True)
class CandidatePath:
    """Pure data: node sequence + per-node picked window + per-edge picked rule.

    No validity claim — gates decide whether the candidate is a valid
    propagation. Field lengths:
      - per-node lists have ``len(node_ids)`` entries
      - per-edge lists have ``len(node_ids) - 1`` entries
    """

    node_ids: list[int]
    all_states: list[list[str]]
    picked_states: list[str]
    picked_state_start_times: list[int]
    edge_descs: list[str]
    rule_ids: list[str]
    rule_confidences: list[float]
    propagation_delays: list[float]


class PathBuilder:
    """Construct ``CandidatePath`` from a node-id sequence (no filtering)."""

    def __init__(
        self,
        graph: HyperGraph,
        rules: list[PropagationRule],
        timelines: dict[str, StateTimeline],
        rule_matcher: RuleMatcher,
        temporal_validator: TemporalValidator,
    ) -> None:
        self.graph = graph
        self.rules = rules
        self.timelines = timelines
        self.rule_matcher = rule_matcher
        self.temporal_validator = temporal_validator
        self.rule_index = rule_matcher.rule_index

    def build(self, node_ids: list[int]) -> CandidatePath | None:
        if len(node_ids) < 2:
            return None

        src_labels = self._labels_for_node(node_ids[0])
        full_rule = self.rule_matcher.find_matching_multi_hop_rule(node_ids, self.graph, src_labels=src_labels)
        if full_rule is not None:
            built = self._build_multi_hop(node_ids, full_rule, is_first_hop=True, prev_start_time=None)
            if built is not None:
                return built

        nodes: list[int] = []
        all_states: list[list[str]] = []
        picked_states: list[str] = []
        picked_times: list[int] = []
        edge_descs: list[str] = []
        rule_ids: list[str] = []
        rule_confs: list[float] = []
        delays: list[float] = []

        i = 0
        while i < len(node_ids) - 1:
            sub = self._try_multi_hop_subpath(node_ids, i, picked_times[-1] if picked_times else None)
            if sub is not None:
                sub_nodes, sub_all, sub_picked, sub_times, sub_edges, sub_rules, sub_confs, sub_delays, consumed = sub
                if i == 0:
                    nodes.extend(sub_nodes)
                    all_states.extend(sub_all)
                    picked_states.extend(sub_picked)
                    picked_times.extend(sub_times)
                else:
                    nodes.extend(sub_nodes[1:])
                    all_states.extend(sub_all[1:])
                    picked_states.extend(sub_picked[1:])
                    picked_times.extend(sub_times[1:])
                edge_descs.extend(sub_edges)
                rule_ids.extend(sub_rules)
                rule_confs.extend(sub_confs)
                delays.extend(sub_delays)
                i += consumed
                continue

            hop = self._build_single_hop(
                src_id=node_ids[i],
                dst_id=node_ids[i + 1],
                prev_start_time=picked_times[-1] if picked_times else None,
                is_first_hop=(i == 0),
            )
            if hop is None:
                return None

            (
                src_all,
                src_picked,
                src_time,
                dst_all,
                dst_picked,
                dst_time,
                edge_desc,
                rule_id,
                rule_conf,
                delay,
            ) = hop

            if i == 0:
                nodes.append(node_ids[i])
                all_states.append(src_all)
                picked_states.append(src_picked)
                picked_times.append(src_time)
            nodes.append(node_ids[i + 1])
            all_states.append(dst_all)
            picked_states.append(dst_picked)
            picked_times.append(dst_time)
            edge_descs.append(edge_desc)
            rule_ids.append(rule_id)
            rule_confs.append(rule_conf)
            delays.append(delay)
            i += 1

        return CandidatePath(
            node_ids=nodes,
            all_states=all_states,
            picked_states=picked_states,
            picked_state_start_times=picked_times,
            edge_descs=edge_descs,
            rule_ids=rule_ids,
            rule_confidences=rule_confs,
            propagation_delays=delays,
        )

    def _try_multi_hop_subpath(
        self,
        node_ids: list[int],
        start: int,
        prev_start_time: int | None,
    ) -> (
        tuple[
            list[int],
            list[list[str]],
            list[str],
            list[int],
            list[str],
            list[str],
            list[float],
            list[float],
            int,
        ]
        | None
    ):
        for rule in self.rules:
            if not rule.is_multi_hop or not rule.path:
                continue
            rule_node_count = len(rule.path) + 1
            if start + rule_node_count > len(node_ids):
                continue
            sub = node_ids[start : start + rule_node_count]
            sub_src_labels = self._labels_for_node(sub[0])
            if not self.rule_matcher.matches_multi_hop_rule(rule, sub, self.graph, src_labels=sub_src_labels):
                continue
            built = self._build_multi_hop(sub, rule, is_first_hop=(start == 0), prev_start_time=prev_start_time)
            if built is None:
                continue
            return (
                built.node_ids,
                built.all_states,
                built.picked_states,
                built.picked_state_start_times,
                built.edge_descs,
                built.rule_ids,
                built.rule_confidences,
                built.propagation_delays,
                rule_node_count - 1,
            )
        return None

    def _build_multi_hop(
        self,
        node_ids: list[int],
        rule: PropagationRule,
        is_first_hop: bool,
        prev_start_time: int | None,
    ) -> CandidatePath | None:
        if not rule.path or len(node_ids) != len(rule.path) + 1:
            return None

        src_id = node_ids[0]
        src_node = self.graph.get_node_by_id(src_id)
        if src_node is None:
            return None
        src_tl = self._timeline_for_node(src_id)
        src_states_all = {w.state for w in src_tl.windows} if src_tl else set()
        rule_src_states = set(rule.src_states)

        src_matching_window: TimelineWindow | None = None
        if is_first_hop and rule_src_states and src_tl:
            for w in src_tl.windows:
                if w.state in rule_src_states:
                    src_matching_window = w
                    break
            if src_matching_window is None:
                return None

        if src_matching_window:
            src_picked_state = src_matching_window.state
            src_all_states = [src_matching_window.state]
        elif src_states_all:
            src_picked_state = next(iter(src_states_all))
            src_all_states = sorted(src_states_all)
        else:
            src_picked_state = "unknown"
            src_all_states = ["unknown"]

        if is_first_hop and rule_src_states:
            earliest = self.temporal_validator.onset_for_rule(src_node.uniq_name, rule_src_states)
            if earliest is not None:
                src_start_time = earliest
            elif src_matching_window:
                src_start_time = src_matching_window.start
            elif src_tl and src_tl.windows:
                src_start_time = src_tl.windows[0].start
            else:
                # Source has no timeline at all — refuse to manufacture an
                # onset. The audit (P1-F) showed every such candidate is
                # subsequently rejected by InjectTimeGate, so returning None
                # here only saves compute and preserves the build/admit
                # invariant that PathBuilder never invents timestamps.
                return None
        elif src_matching_window:
            src_start_time = _effective_onset(src_matching_window, prev_start_time)
        elif prev_start_time is not None:
            causal_window = self.temporal_validator.find_causal_window(src_node.uniq_name, prev_start_time)
            src_start_time = _effective_onset(causal_window, prev_start_time) if causal_window else prev_start_time
        elif src_tl and src_tl.windows:
            src_start_time = src_tl.windows[0].start
        else:
            return None

        if (not is_first_hop) and rule_src_states and src_states_all:
            matching = src_states_all & rule_src_states
            if matching:
                src_picked_state = next(iter(matching))

        nodes_out = [src_id]
        all_states_out: list[list[str]] = [src_all_states]
        picked_states_out: list[str] = [src_picked_state]
        picked_times_out: list[int] = [src_start_time]
        edge_descs_out: list[str] = []
        rule_ids_out: list[str] = []
        rule_confs_out: list[float] = []
        delays_out: list[float] = []

        current_start_time = src_start_time
        current_src_state = src_picked_state

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
            next_tl = self._timeline_for_node(next_node_id)
            next_states_all = {w.state for w in next_tl.windows} if next_tl else set()

            is_last_hop = hop_idx == len(rule.path) - 1
            if not is_last_hop:
                if path_hop.intermediate_states is not None:
                    check_states = next_states_all if next_states_all else {"unknown"}
                    allowed_states = set(path_hop.intermediate_states)
                    if not check_states.intersection(allowed_states):
                        return None
                hop_dst_states: set[str] = (
                    set(path_hop.intermediate_states) if path_hop.intermediate_states is not None else next_states_all
                )
            else:
                dst_states_set = set(rule.possible_dst_states)
                if (
                    is_first_hop
                    and dst_states_set
                    and next_states_all
                    and not next_states_all.intersection(dst_states_set)
                ):
                    return None
                hop_dst_states = dst_states_set or next_states_all

            admitted = self.temporal_validator.find_admissible_window(
                next_node.uniq_name,
                src_onset=current_start_time,
                edge_kind=edge_data.kind,
                src_state=current_src_state,
                dst_states=hop_dst_states,
            )
            if admitted is None:
                return None
            causal_window, next_start_time = admitted
            delay = float(next_start_time - current_start_time)
            next_picked_state = causal_window.state

            nodes_out.append(next_node_id)
            all_states_out.append(sorted(next_states_all) if next_states_all else ["unknown"])
            picked_states_out.append(next_picked_state)
            picked_times_out.append(next_start_time)
            edge_descs_out.append(edge_desc)
            rule_ids_out.append(rule.rule_id)
            rule_confs_out.append(rule.confidence)
            delays_out.append(delay)
            current_start_time = next_start_time
            current_src_state = next_picked_state

        return CandidatePath(
            node_ids=nodes_out,
            all_states=all_states_out,
            picked_states=picked_states_out,
            picked_state_start_times=picked_times_out,
            edge_descs=edge_descs_out,
            rule_ids=rule_ids_out,
            rule_confidences=rule_confs_out,
            propagation_delays=delays_out,
        )

    def _build_single_hop(
        self,
        src_id: int,
        dst_id: int,
        prev_start_time: int | None,
        is_first_hop: bool,
    ) -> tuple[list[str], str, int, list[str], str, int, str, str, float, float] | None:
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
            elif r.dst_kind == dst_node.kind:
                valid_rules.append(r)

        if not valid_rules:
            return None

        edge_desc = f"{edge_data.kind.value}_{direction.value}"

        src_tl = self._timeline_for_node(src_id)
        src_states_all = {w.state for w in src_tl.windows} if src_tl else set()
        dst_tl = self._timeline_for_node(dst_id)
        dst_states_all = {w.state for w in dst_tl.windows} if dst_tl else set()

        for rule in valid_rules:
            rule_src_states = set(rule.src_states)
            rule_dst_states = set(rule.possible_dst_states)

            if is_first_hop:
                if (
                    src_node.kind == PlaceKind.span
                    and rule_src_states
                    and not src_states_all.intersection(rule_src_states)
                ):
                    continue
                if not dst_states_all:
                    continue
            else:
                if rule_src_states and not src_states_all.intersection(rule_src_states):
                    continue
                if rule_dst_states and not dst_states_all.intersection(rule_dst_states):
                    continue

            if rule_src_states and src_states_all.intersection(rule_src_states):
                src_state_for_eps = next(iter(src_states_all.intersection(rule_src_states)))
            elif src_states_all:
                src_state_for_eps = next(iter(src_states_all))
            else:
                src_state_for_eps = "unknown"

            admissible_dst_states = (rule_dst_states & dst_states_all) or dst_states_all

            if is_first_hop:
                src_time: int | None = None
                if rule_src_states:
                    src_time = self.temporal_validator.onset_for_rule(src_node.uniq_name, rule_src_states)
                if src_time is None and src_tl and src_tl.windows:
                    src_time = src_tl.windows[0].start
                if src_time is None:
                    # No source timeline — see _build_multi_hop comment above.
                    continue
                anchor = src_time
            else:
                if prev_start_time is not None:
                    src_time = prev_start_time
                elif src_tl and src_tl.windows:
                    src_time = src_tl.windows[0].start
                else:
                    continue
                anchor = prev_start_time if prev_start_time is not None else src_time

            admitted = self.temporal_validator.find_admissible_window(
                dst_node.uniq_name,
                src_onset=anchor,
                edge_kind=edge_data.kind,
                src_state=src_state_for_eps,
                dst_states=admissible_dst_states,
            )
            if admitted is None:
                continue
            causal_window, dst_time = admitted
            dst_picked = causal_window.state
            delay = float(dst_time - anchor)

            src_all = sorted(src_states_all) if src_states_all else ["unknown"]
            dst_all = sorted(dst_states_all) if dst_states_all else ["unknown"]
            return (
                src_all,
                src_state_for_eps,
                src_time,
                dst_all,
                dst_picked,
                dst_time,
                edge_desc,
                rule.rule_id,
                rule.confidence,
                delay,
            )

        return None

    def _get_edge_between(self, src_id: int, dst_id: int) -> tuple[Edge | None, PropagationDirection | None]:
        if self.graph._graph.has_edge(src_id, dst_id):
            edge_attrs = self.graph._graph.get_edge_data(src_id, dst_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.FORWARD
        if self.graph._graph.has_edge(dst_id, src_id):
            edge_attrs = self.graph._graph.get_edge_data(dst_id, src_id)
            if edge_attrs:
                edge_data = list(edge_attrs.values())[0].get("ref")
                if edge_data:
                    return edge_data, PropagationDirection.BACKWARD
        return None, None

    def _timeline_for_node(self, node_id: int) -> StateTimeline | None:
        node = self.graph.get_node_by_id(node_id)
        if node is None:
            return None
        return self.timelines.get(node.uniq_name)

    def _labels_for_node(self, node_id: int) -> frozenset[str]:
        tl = self._timeline_for_node(node_id)
        if tl is None:
            return frozenset()
        labels: set[str] = set()
        for w in tl.windows:
            ws = w.evidence.get("specialization_labels")
            if ws:
                labels.update(ws)
        return frozenset(labels)


# Re-export a light DepKind hook for type-checking; not used at runtime.
__all__ = ["CandidatePath", "PathBuilder", "DepKind"]
