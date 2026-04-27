"""Slim FaultPropagator coordinator over canonical-state IR timelines.

The propagator coordinates topology exploration (TopologyExplorer), rule
matching (RuleMatcher) and temporal validation (TemporalValidator) on top
of the per-node ``StateTimeline``s produced by ``run_reasoning_ir``.
"""

from __future__ import annotations

import hashlib
import json
import logging
from collections import Counter
from datetime import datetime
from pathlib import Path

from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import TemporalValidator
from rcabench_platform.v3.internal.reasoning.algorithms.topology_explorer import TopologyExplorer
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationPath, PropagationResult
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationDirection, PropagationRule

logger = logging.getLogger(__name__)


class RuleUsageStats:
    def __init__(self) -> None:
        self.rule_counts: Counter[str] = Counter()

    def record_rule_use(self, rule_id: str) -> None:
        self.rule_counts[rule_id] += 1

    def save_to_file(self, filepath: Path | str) -> None:
        filepath = Path(filepath)
        stats = {
            "total_applications": sum(self.rule_counts.values()),
            "unique_rules_used": len(self.rule_counts),
            "rule_usage": dict(self.rule_counts.most_common()),
        }
        filepath.write_text(json.dumps(stats, indent=2))

    def get_summary(self) -> str:
        lines = ["Rule Usage Statistics:"]
        for rule_id, count in self.rule_counts.most_common():
            lines.append(f"  {rule_id}: {count}")
        return "\n".join(lines)


class FaultPropagator:
    """Bidirectional fault propagation analyzer over canonical-state timelines."""

    def __init__(
        self,
        graph: HyperGraph,
        rules: list[PropagationRule],
        timelines: dict[str, StateTimeline],
        max_hops: int = 5,
    ) -> None:
        """Initialize the fault propagator.

        Args:
            graph: HyperGraph topology (nodes + edges only; no state).
            rules: Propagation rules to evaluate paths against.
            timelines: Per-node ``StateTimeline`` from ``run_reasoning_ir``,
                keyed by ``node.uniq_name``.
            max_hops: Maximum propagation hops (default 5).
        """
        self.graph = graph
        self.rules = rules
        self.timelines = timelines
        self.max_hops = max_hops

        self.rule_matcher = RuleMatcher(rules)
        self.rule_index = self.rule_matcher.rule_index
        self.topology_explorer = TopologyExplorer(graph, max_hops)
        self.temporal_validator = TemporalValidator(timelines)
        self.rule_stats = RuleUsageStats()

    def propagate_from_injection(
        self,
        injection_node_ids: list[int],
        alarm_nodes: set[int],
    ) -> PropagationResult:
        for injection_node_id in injection_node_ids:
            if self.graph.get_node_by_id(injection_node_id) is None:
                raise ValueError(f"Injection node {injection_node_id} not found in graph")

        def edge_filter(src_id: int, dst_id: int, is_first_hop: bool) -> bool:
            src_states = self._states_for_node(src_id)
            dst_states = self._states_for_node(dst_id)
            src_labels = self._labels_for_node(src_id)
            return self.rule_matcher.edge_matches_any_rule(
                src_id, dst_id, self.graph, src_states, dst_states, is_first_hop, src_labels=src_labels
            )

        # §7.6 step 6 + §13.2 step 2.6 — bidirectional corridor + activity filter.
        # corridor       = Reach_forward(injection_set, max_hops_fwd)
        #                ∩ Reach_backward(alarm_set,    max_hops_bwd)
        # relevant_nodes = corridor ∩ (deviating_set ∪ injection_set)
        #
        # ``compute_corridor`` walks pure out_edges/in_edges, which assumes
        # propagation direction == graph edge direction. rcabench's ``calls``
        # edges go caller→callee while propagation flows callee→caller, so
        # we reuse ``find_reachable_subgraph`` (neighbor-based, both edge
        # directions, edge_filter applied) for both passes — matching how
        # the propagator already enumerates walkable subgraphs.
        injection_set = set(injection_node_ids)

        # Reverse-orientation filter for the backward reach pass: an edge
        # (a, b) participates in backward reach from b iff (b → a) matches
        # some rule's forward propagation direction. Critically, we must
        # also reproduce the forward filter's first_hop_config semantics on
        # the injection end of the chain — without this, backward BFS
        # rejects rule-less first edges that forward admits.
        #
        # In rcabench, ``container`` kind's ``DEFAULT_FIRST_HOP_CONFIGS``
        # has ``require_src_states=False`` so a Class C JVMException
        # injection (container.erroring, no matching rule for
        # container.erroring → anything) still crosses the first edge
        # forward. The methodology-aligned refactor exposed this
        # asymmetry: backward without the equivalent relaxation cannot
        # walk back into the injection container from its pod, so the
        # corridor breaks for these cases.
        #
        # The fix sets ``is_first_hop=True`` on the swapped rule check
        # iff ``dst_id`` (the neighbor we're stepping toward in backward
        # BFS, which becomes the SRC of the forward-propagation rule)
        # is in the original ``injection_set``.
        def backward_edge_filter(src_id: int, dst_id: int, is_first_hop_unused: bool) -> bool:
            src_states = self._states_for_node(dst_id)
            dst_states = self._states_for_node(src_id)
            src_labels = self._labels_for_node(dst_id)
            is_first_hop = dst_id in injection_set
            return self.rule_matcher.edge_matches_any_rule(
                dst_id, src_id, self.graph, src_states, dst_states, is_first_hop, src_labels=src_labels
            )

        forward_edges = self.topology_explorer.find_reachable_subgraph(injection_node_ids, alarm_nodes, edge_filter)
        forward_visited: set[int] = set(injection_set)
        for s, d in forward_edges:
            forward_visited.add(s)
            forward_visited.add(d)

        backward_edges = self.topology_explorer.find_reachable_subgraph(
            list(alarm_nodes), set(injection_set), backward_edge_filter
        )
        backward_visited: set[int] = set(alarm_nodes)
        for s, d in backward_edges:
            backward_visited.add(s)
            backward_visited.add(d)

        corridor = forward_visited & backward_visited
        deviating_set = self._compute_deviating_set()
        relevant_nodes = corridor & (deviating_set | injection_set)

        subgraph_edges = [(s, d) for s, d in forward_edges if s in relevant_nodes and d in relevant_nodes]
        warnings: list[str] = []

        if not subgraph_edges:
            warning_msg = f"No reachable edges found from injection nodes {injection_node_ids}"
            warnings.append(warning_msg)
            logger.warning(f"  [WARNING] {warning_msg}")
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=[],
                warnings=warnings,
            )

        all_topology_paths = self.topology_explorer.extract_paths(subgraph_edges, injection_node_ids, alarm_nodes)

        if not all_topology_paths:
            warning_msg = f"No paths extracted from reachable subgraph ({len(subgraph_edges)} edges available)"
            warnings.append(warning_msg)
            logger.warning(f"  [WARNING] {warning_msg}")
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=subgraph_edges,
                warnings=warnings,
            )

        valid_paths: list[PropagationPath] = []
        visited_nodes: set[int] = set()
        max_hops = 0
        for node_ids in all_topology_paths:
            visited_nodes.update(node_ids)
            max_hops = max(max_hops, len(node_ids) - 1)
            path = self._verify_and_build_path(node_ids)
            if path is not None:
                valid_paths.append(path)

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

    def _timeline_for_node(self, node_id: int) -> StateTimeline | None:
        node = self.graph.get_node_by_id(node_id)
        if node is None:
            return None
        return self.timelines.get(node.uniq_name)

    def _states_for_node(self, node_id: int) -> set[str]:
        tl = self._timeline_for_node(node_id)
        if tl is None:
            return set()
        return {w.state for w in tl.windows}

    def _compute_deviating_set(self) -> set[int]:
        """Nodes whose timeline has ever been in a non-{HEALTHY, UNKNOWN}
        state during the abnormal window, OR whose kind is a structural
        k8s infra connector (pod / replica_set / deployment).

        Per §7.4, used by the activity filter in
        :meth:`propagate_from_injection` to restrict path search to nodes
        that actually exhibit anomalous behavior. The methodology's
        argument for dropping HEALTHY middlemen ("propagation rules never
        have HEALTHY as src_state") holds for request-layer kinds
        (service / span) but not for structural connectors: e.g.
        StructuralInheritanceAdapter shortcuts container.degraded →
        service.degraded, leaving the intervening pod with an empty
        timeline. The pod is still the necessary topological hop between
        container and service. Treating pod / replica_set / deployment as
        always-relevant matches the §7.4 invariant 1 spirit ("node
        completeness — service / pod / container nodes are extracted from
        the union of k8s + traces") — they exist in the graph because
        they are part of the cascade structure, not because traces saw
        them.
        """
        structural_kinds = {PlaceKind.pod, PlaceKind.replica_set, PlaceKind.deployment}
        deviating: set[int] = set()
        nominal = {"healthy", "unknown"}
        for node_id in self.graph._graph.nodes:
            node = self.graph.get_node_by_id(node_id)
            if node is not None and node.kind in structural_kinds:
                deviating.add(node_id)
                continue
            tl = self._timeline_for_node(node_id)
            if tl is None:
                continue
            for window in tl.windows:
                if window.state not in nominal:
                    deviating.add(node_id)
                    break
        return deviating

    def _labels_for_node(self, node_id: int) -> frozenset[str]:
        """Aggregate every specialization label ever observed on the node.

        Phase 4 of #163: rules with non-empty ``required_labels`` gate on
        these. Aggregating across the whole timeline (rather than picking
        a specific window) mirrors :meth:`StateTimeline.ever_carries`.
        """
        tl = self._timeline_for_node(node_id)
        if tl is None:
            return frozenset()
        labels: set[str] = set()
        for w in tl.windows:
            ws = w.evidence.get("specialization_labels")
            if ws:
                labels.update(ws)
        return frozenset(labels)

    def _node_start_time(self, node_id: int) -> int | None:
        tl = self._timeline_for_node(node_id)
        if tl is None or not tl.windows:
            return None
        return tl.windows[0].start

    def _format_path_debug(self, node_ids: list[int]) -> str:
        parts = []
        for nid in node_ids:
            node = self.graph.get_node_by_id(nid)
            if node:
                parts.append(f"{node.kind.value}:{node.self_name}")
            else:
                parts.append(f"?:{nid}")
        return " -> ".join(parts)

    def _verify_and_build_path(self, node_ids: list[int]) -> PropagationPath | None:
        if len(node_ids) < 2:
            return None

        src_labels = self._labels_for_node(node_ids[0])
        multi_hop_rule = self.rule_matcher.find_matching_multi_hop_rule(node_ids, self.graph, src_labels=src_labels)
        if multi_hop_rule is not None:
            path = self._verify_multi_hop_path(node_ids, multi_hop_rule)
            if path is not None:
                return path

        nodes: list[int] = []
        states: list[list[str]] = []
        edges: list[str] = []
        rules: list[str] = []
        state_start_times: list[int | None] = []
        propagation_delays: list[float] = []

        i = 0
        while i < len(node_ids) - 1:
            multi_hop_matched = False
            for rule in self.rules:
                if not rule.is_multi_hop or not rule.path:
                    continue
                rule_node_count = len(rule.path) + 1
                if i + rule_node_count > len(node_ids):
                    continue
                sub_path = node_ids[i : i + rule_node_count]
                sub_src_labels = self._labels_for_node(sub_path[0])
                if self.rule_matcher.matches_multi_hop_rule(rule, sub_path, self.graph, src_labels=sub_src_labels):
                    prev_time = state_start_times[-1] if state_start_times else None
                    verified = self._verify_multi_hop_subpath(
                        sub_path, rule, prev_start_time=prev_time, is_first_hop=(i == 0)
                    )
                    if verified is not None:
                        sub_nodes, sub_states, sub_edges, sub_rules, sub_times, sub_delays = verified
                        if i == 0:
                            nodes.extend(sub_nodes)
                            states.extend(sub_states)
                            state_start_times.extend(sub_times)
                        else:
                            nodes.extend(sub_nodes[1:])
                            states.extend(sub_states[1:])
                            state_start_times.extend(sub_times[1:])
                        edges.extend(sub_edges)
                        rules.extend(sub_rules)
                        propagation_delays.extend(sub_delays)
                        i += rule_node_count - 1
                        multi_hop_matched = True
                        break

            if multi_hop_matched:
                continue

            hop_result = self._verify_single_hop(
                hop_index=i,
                src_id=node_ids[i],
                dst_id=node_ids[i + 1],
                prev_start_time=state_start_times[-1] if state_start_times else None,
                is_first_hop=(i == 0),
            )
            if hop_result is None:
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

    def _verify_multi_hop_path(self, node_ids: list[int], rule: PropagationRule) -> PropagationPath | None:
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
        if rule_src_states and src_tl:
            for w in src_tl.windows:
                if w.state in rule_src_states:
                    src_matching_window = w
                    break
            if src_matching_window is None:
                return None

        nodes: list[int] = [src_id]
        if src_matching_window:
            states: list[list[str]] = [[src_matching_window.state]]
        else:
            states = [sorted(src_states_all) if src_states_all else ["unknown"]]
        edges_list: list[str] = []
        rules_list: list[str] = []
        state_start_times: list[int | None] = []
        propagation_delays: list[float] = []

        # §7.5 trajectory rule: anchor src_start_time on the EARLIEST
        # transition into rule.src_states (not the most-recent). This avoids
        # the temporal gate rejecting valid chains because the source onset
        # has slipped past the downstream onset (e.g. ERRORING -> SILENT).
        if rule_src_states:
            earliest = self.temporal_validator.onset_for_rule(src_node.uniq_name, rule_src_states)
            if earliest is not None:
                src_start_time = earliest
            elif src_matching_window:
                src_start_time = src_matching_window.start
            elif src_tl and src_tl.windows:
                src_start_time = src_tl.windows[0].start
            else:
                all_starts = [tl.windows[0].start for tl in self.timelines.values() if tl.windows]
                src_start_time = min(all_starts) if all_starts else 0
        elif src_matching_window:
            src_start_time = src_matching_window.start
        elif src_tl and src_tl.windows:
            src_start_time = src_tl.windows[0].start
        else:
            all_starts = [tl.windows[0].start for tl in self.timelines.values() if tl.windows]
            src_start_time = min(all_starts) if all_starts else 0

        state_start_times.append(src_start_time)
        current_start_time = src_start_time
        # Track the state currently anchoring the chain — starts with the
        # rule.src_state that matched the source, then advances to whatever
        # state the previous hop's causal_window matched.
        current_src_state: str = (
            src_matching_window.state
            if src_matching_window
            else (next(iter(src_states_all)) if src_states_all else "unknown")
        )

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
                # ε-tolerant admission against the intermediate-state set
                hop_dst_states: set[str] = (
                    set(path_hop.intermediate_states) if path_hop.intermediate_states is not None else next_states_all
                )
            else:
                dst_states_set = set(rule.possible_dst_states)
                if dst_states_set and next_states_all and not next_states_all.intersection(dst_states_set):
                    return None
                hop_dst_states = dst_states_set or next_states_all

            causal_window = self.temporal_validator.find_admissible_window(
                next_node.uniq_name,
                src_onset=current_start_time,
                edge_kind=edge_data.kind,
                src_state=current_src_state,
                dst_states=hop_dst_states,
            )
            if causal_window is not None:
                delay = float(causal_window.start - current_start_time)
                next_start_time = causal_window.start
                current_src_state = causal_window.state
            else:
                delay = 0.0
                next_start_time = current_start_time
                # current_src_state stays — no observation advanced the chain

            nodes.append(next_node_id)
            states.append(sorted(next_states_all) if next_states_all else ["unknown"])
            edges_list.append(edge_desc)
            rules_list.append(rule.rule_id)
            state_start_times.append(next_start_time)
            propagation_delays.append(delay)
            current_start_time = next_start_time

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
        elif rule_src_states and src_states_all:
            if not src_states_all.intersection(rule_src_states):
                return None

        nodes: list[int] = [src_id]
        if src_matching_window:
            states: list[list[str]] = [[src_matching_window.state]]
        else:
            states = [sorted(src_states_all) if src_states_all else ["unknown"]]
        edges_list: list[str] = []
        rules_list: list[str] = []
        state_start_times: list[int | None] = []
        propagation_delays: list[float] = []

        # §7.5 trajectory rule for the first hop of the subpath: anchor on
        # the EARLIEST transition into rule.src_states. For non-first
        # subpaths, prev_start_time already reflects the upstream chain.
        if is_first_hop and rule_src_states:
            earliest = self.temporal_validator.onset_for_rule(src_node.uniq_name, rule_src_states)
            if earliest is not None:
                src_start_time = earliest
            elif src_matching_window:
                src_start_time = src_matching_window.start
            elif src_tl and src_tl.windows:
                src_start_time = src_tl.windows[0].start
            else:
                all_starts = [tl.windows[0].start for tl in self.timelines.values() if tl.windows]
                src_start_time = min(all_starts) if all_starts else 0
        elif src_matching_window:
            src_start_time = src_matching_window.start
        elif prev_start_time is not None:
            # Locate where on the source timeline the previous chain landed
            # (intra-chain bookkeeping, not a propagation hop).
            causal_window = self.temporal_validator.find_causal_window(src_node.uniq_name, prev_start_time)
            src_start_time = causal_window.start if causal_window else prev_start_time
        elif src_tl and src_tl.windows:
            src_start_time = src_tl.windows[0].start
        else:
            all_starts = [tl.windows[0].start for tl in self.timelines.values() if tl.windows]
            src_start_time = min(all_starts) if all_starts else 0

        state_start_times.append(src_start_time)
        current_start_time = src_start_time
        # Track the state currently anchoring the chain — see _verify_multi_hop_path.
        if src_matching_window:
            current_src_state: str = src_matching_window.state
        elif rule_src_states and src_states_all:
            matching = src_states_all.intersection(rule_src_states)
            current_src_state = (
                next(iter(matching)) if matching else (next(iter(src_states_all)) if src_states_all else "unknown")
            )
        elif src_states_all:
            current_src_state = next(iter(src_states_all))
        else:
            current_src_state = "unknown"

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
                hop_dst_states = dst_states_set or next_states_all

            causal_window = self.temporal_validator.find_admissible_window(
                next_node.uniq_name,
                src_onset=current_start_time,
                edge_kind=edge_data.kind,
                src_state=current_src_state,
                dst_states=hop_dst_states,
            )
            if causal_window is not None:
                delay = float(causal_window.start - current_start_time)
                next_start_time = causal_window.start
                current_src_state = causal_window.state
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
                result = self._process_first_hop(hop_index, src_id, dst_id, edge_desc, edge_data.kind, rule)
            else:
                result = self._process_subsequent_hop(
                    hop_index, src_id, dst_id, src_node, dst_node, edge_desc, edge_data.kind, rule, prev_start_time
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
        edge_kind: DepKind,
        rule: PropagationRule,
    ) -> tuple[int, int, list[str], list[str], int | None, int | None, str, str, float] | None:
        src_node = self.graph.get_node_by_id(src_id)
        dst_node = self.graph.get_node_by_id(dst_id)
        if src_node is None or dst_node is None:
            return None

        src_tl = self._timeline_for_node(src_id)
        src_states_all = {w.state for w in src_tl.windows} if src_tl else set()
        dst_tl = self._timeline_for_node(dst_id)
        dst_states_all = {w.state for w in dst_tl.windows} if dst_tl else set()

        rule_src_states = set(rule.src_states)
        if src_node.kind == PlaceKind.span:
            if rule_src_states and not src_states_all.intersection(rule_src_states):
                return None

        if not dst_states_all:
            return None

        # §7.5 trajectory rule: anchor on the EARLIEST transition into
        # rule.src_states (not the first window, which may be HEALTHY).
        src_time: int | None = None
        if rule_src_states:
            src_time = self.temporal_validator.onset_for_rule(src_node.uniq_name, rule_src_states)
        if src_time is None and src_tl and src_tl.windows:
            src_time = src_tl.windows[0].start

        # Pick the src_state that anchors ε_eff: prefer a rule.src_states match.
        src_state_for_eps: str
        if rule_src_states and src_states_all.intersection(rule_src_states):
            src_state_for_eps = next(iter(src_states_all.intersection(rule_src_states)))
        elif src_states_all:
            src_state_for_eps = next(iter(src_states_all))
        else:
            src_state_for_eps = "unknown"

        rule_dst_states = set(rule.possible_dst_states)
        admissible_dst_states = (rule_dst_states & dst_states_all) or dst_states_all

        if src_time is not None:
            causal_window = self.temporal_validator.find_admissible_window(
                dst_node.uniq_name,
                src_onset=src_time,
                edge_kind=edge_kind,
                src_state=src_state_for_eps,
                dst_states=admissible_dst_states,
            )
            if causal_window is not None:
                dst_time: int | None = causal_window.start
                delay = float(causal_window.start - src_time)
            else:
                dst_time = None
                delay = 0.0
        else:
            dst_time = dst_tl.windows[0].start if dst_tl and dst_tl.windows else None
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
        edge_kind: DepKind,
        rule: PropagationRule,
        prev_start_time: int | None,
    ) -> tuple[int, int, list[str], list[str], int | None, int | None, str, str, float] | None:
        src_tl = self._timeline_for_node(src_id)
        src_states_all = {w.state for w in src_tl.windows} if src_tl else set()
        dst_tl = self._timeline_for_node(dst_id)
        dst_states_all = {w.state for w in dst_tl.windows} if dst_tl else set()

        rule_src_states = set(rule.src_states)
        if rule_src_states and not src_states_all.intersection(rule_src_states):
            return None
        rule_dst_states = set(rule.possible_dst_states)
        if rule_dst_states and not dst_states_all.intersection(rule_dst_states):
            return None

        # Pick the src_state that anchors ε_eff (see _process_first_hop).
        if rule_src_states and src_states_all.intersection(rule_src_states):
            src_state_for_eps = next(iter(src_states_all.intersection(rule_src_states)))
        elif src_states_all:
            src_state_for_eps = next(iter(src_states_all))
        else:
            src_state_for_eps = "unknown"

        admissible_dst_states = (rule_dst_states & dst_states_all) or dst_states_all

        if prev_start_time is not None:
            causal_window = self.temporal_validator.find_admissible_window(
                dst_node.uniq_name,
                src_onset=prev_start_time,
                edge_kind=edge_kind,
                src_state=src_state_for_eps,
                dst_states=admissible_dst_states,
            )
            if causal_window is None:
                return None
            dst_time: int | None = causal_window.start
            delay = float(causal_window.start - prev_start_time)
        else:
            dst_time = dst_tl.windows[0].start if dst_tl and dst_tl.windows else None
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
