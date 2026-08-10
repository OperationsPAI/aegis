"""TopologyExplorer: BFS traversal and path extraction from heterogeneous graphs.

This module handles topology exploration for fault propagation:
- BFS to find reachable subgraph from injection nodes
- DFS to extract all paths from injection to alarm nodes

Supports "diamond-shaped" paths where a node can be visited multiple times,
e.g., span -> service -> pod -> service -> span (service visited twice).
This is controlled by max_node_visits parameter.
"""

import logging
import time
from collections import Counter, deque
from collections.abc import Callable

from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph

logger = logging.getLogger(__name__)

# Maximum times a single node can appear in a path.
#
# Occam's razor: a diamond like ``A_span -> service -> B_span -> service ->
# C_span`` is structurally the same propagation chain as ``A -> B -> C`` at
# the service level, just padded by re-entering the shared service /
# pod / container infra node. Allowing visits=2 produces 16k+ span-level
# permutations of the same logical service chain (verified empirically on
# Class C train-service-exception: 99994 paths collapse to ~6 distinct
# (src, alarm) endpoint pairs and well under 100 distinct service
# sequences). visits=1 keeps each node once per path so the simpler
# representation always wins.
DEFAULT_MAX_NODE_VISITS = 1

# Safety net for path enumeration (§7.6 step 8). After corridor pruning a
# real benchmark case (~300 nodes, ~800 edges) yields far fewer than this;
# the cap exists only to bound pathological topologies.
DEFAULT_MAX_PATHS: int = 100_000


class TopologyExplorer:
    """Explores graph topology using BFS/DFS for fault propagation analysis.

    This class handles pure topology exploration without rule semantics:
    - Finding reachable edges from injection nodes via BFS
    - Extracting all paths from injection to alarm nodes via DFS
    """

    def __init__(
        self,
        graph: HyperGraph,
        max_hops: int = 5,
        max_node_visits: int = DEFAULT_MAX_NODE_VISITS,
        max_paths: int = DEFAULT_MAX_PATHS,
    ):
        """Initialize the topology explorer.

        Args:
            graph: The hypergraph containing topology
            max_hops: Maximum propagation hops (default 5)
            max_node_visits: Maximum times a node can appear in a path (default 2).
                            Set to 2 to support diamond-shaped paths like
                            span -> service -> pod -> service -> span.
            max_paths: Safety-net cap on the number of admissible paths
                       returned by :meth:`extract_paths` (§7.6 step 8). DFS
                       short-circuits once this cap is hit and emits a
                       warning. Methodology treats this purely as a guard
                       against pathological topologies; with corridor
                       pruning real cases stay well under the default.
        """
        self.graph = graph
        self.max_hops = max_hops
        self.max_node_visits = max_node_visits
        self.max_paths = max_paths

    def get_neighbors(self, node_id: int) -> list[int]:
        """Get all neighbors (forward and backward) for a node.

        Args:
            node_id: The node to get neighbors for

        Returns:
            List of neighbor node IDs (both forward and backward edges)
        """
        neighbors: list[int] = []

        # Forward neighbors (outgoing edges)
        for _, dst_id, _ in self.graph._graph.out_edges(node_id, keys=True):  # type: ignore[call-arg]
            neighbors.append(dst_id)

        # Backward neighbors (incoming edges)
        for src_id, _, _ in self.graph._graph.in_edges(node_id, keys=True):  # type: ignore[call-arg]
            neighbors.append(src_id)

        return neighbors

    def find_reachable_subgraph(
        self,
        injection_node_ids: list[int],
        alarm_nodes: set[int],
        edge_filter: Callable[[int, int, bool], bool] | None = None,
    ) -> list[tuple[int, int]]:
        """Find all reachable edges from injection nodes using BFS.

        Performs BFS from injection nodes, filtering edges that don't match
        propagation rules. Returns the subgraph as a list of edges.

        Args:
            injection_node_ids: Starting node IDs for propagation
            alarm_nodes: Target alarm node IDs (used for logging)
            edge_filter: Optional function (src_id, dst_id, is_first_hop) -> bool
                        to filter edges. If None, all edges are included.

        Returns:
            List of (src_id, dst_id) tuples representing reachable edges
        """
        start_time = time.time()
        injection_set = set(injection_node_ids)
        visited: set[int] = set(injection_set)
        edges: set[tuple[int, int]] = set()

        queue: deque[tuple[int, int]] = deque()  # (node_id, distance)
        for nid in injection_node_ids:
            queue.append((nid, 0))

        while queue:
            current_node_id, dist = queue.popleft()

            if dist >= self.max_hops:
                continue

            for neighbor_id in self.get_neighbors(current_node_id):
                # Apply edge filter if provided
                is_first_hop = current_node_id in injection_set
                if edge_filter is not None and not edge_filter(current_node_id, neighbor_id, is_first_hop):
                    continue

                edges.add((current_node_id, neighbor_id))
                if neighbor_id not in visited:
                    visited.add(neighbor_id)
                    queue.append((neighbor_id, dist + 1))

        reachable_alarms = alarm_nodes & visited - injection_set
        elapsed_time = time.time() - start_time
        logger.debug(
            f"    [DEBUG] Reachable subgraph: {len(edges)} edges, "
            f"{len(visited)} nodes, {len(reachable_alarms)} reachable alarms, "
            f"time: {elapsed_time:.3f}s"
        )
        return list(edges)

    def compute_corridor(
        self,
        injection_node_ids: list[int],
        alarm_node_ids: set[int],
        max_hops_fwd: int | None = None,
        max_hops_bwd: int | None = None,
        edge_filter: Callable[[int, int, bool], bool] | None = None,
    ) -> set[int]:
        """Bidirectional BFS corridor per §7.4.

        corridor = Reach_forward(injection_set, max_hops_fwd)
                 ∩ Reach_backward(alarm_set,    max_hops_bwd)

        The forward-only ``find_reachable_subgraph`` over-includes services
        reachable from the injection but unable to reach any alarm — those
        wander into dead-end branches during downstream DFS. Intersecting
        with the backward reach from the alarm set keeps only nodes that
        can actually sit on an injection→alarm path within the hop budget.

        Args:
            injection_node_ids: Starting node IDs for the forward pass.
            alarm_node_ids: Target alarm node IDs for the backward pass.
            max_hops_fwd: Forward BFS hop budget (defaults to ``self.max_hops``).
            max_hops_bwd: Backward BFS hop budget (defaults to ``self.max_hops``).
            edge_filter: Optional ``(src_id, dst_id, is_first_hop) -> bool``
                applied to both passes. ``is_first_hop`` is computed against
                ``injection_set`` for forward and against ``alarm_set`` for
                backward — callers that need direction-specific filtering
                should pass a filter that ignores the flag.

        Returns:
            Set of node IDs in the corridor (the intersection). Empty when
            either ``injection_node_ids`` or ``alarm_node_ids`` is empty.
        """
        if not injection_node_ids or not alarm_node_ids:
            return set()

        start_time = time.time()

        max_hops_fwd = self.max_hops if max_hops_fwd is None else max_hops_fwd
        max_hops_bwd = self.max_hops if max_hops_bwd is None else max_hops_bwd

        injection_set = set(injection_node_ids)

        # Forward BFS: traverse out_edges from injection nodes.
        forward_visited: set[int] = set(injection_set)
        forward_queue: deque[tuple[int, int]] = deque((nid, 0) for nid in injection_set)
        while forward_queue:
            current_node_id, dist = forward_queue.popleft()
            if dist >= max_hops_fwd:
                continue
            is_first_hop = current_node_id in injection_set
            for _, dst_id, _ in self.graph._graph.out_edges(current_node_id, keys=True):  # type: ignore[call-arg]
                if edge_filter is not None and not edge_filter(current_node_id, dst_id, is_first_hop):
                    continue
                if dst_id not in forward_visited:
                    forward_visited.add(dst_id)
                    forward_queue.append((dst_id, dist + 1))

        # Backward BFS: traverse in_edges (predecessors) from alarm nodes.
        backward_visited: set[int] = set(alarm_node_ids)
        backward_queue: deque[tuple[int, int]] = deque((nid, 0) for nid in alarm_node_ids)
        while backward_queue:
            current_node_id, dist = backward_queue.popleft()
            if dist >= max_hops_bwd:
                continue
            is_first_hop = current_node_id in alarm_node_ids
            for src_id, _, _ in self.graph._graph.in_edges(current_node_id, keys=True):  # type: ignore[call-arg]
                # For backward traversal the edge goes src_id -> current_node_id.
                # Pass it to the filter in its natural (src, dst) orientation.
                if edge_filter is not None and not edge_filter(src_id, current_node_id, is_first_hop):
                    continue
                if src_id not in backward_visited:
                    backward_visited.add(src_id)
                    backward_queue.append((src_id, dist + 1))

        corridor = forward_visited & backward_visited
        elapsed_time = time.time() - start_time
        logger.debug(
            f"    [DEBUG] Corridor: {len(corridor)} nodes "
            f"(fwd={len(forward_visited)}, bwd={len(backward_visited)}, "
            f"max_hops_fwd={max_hops_fwd}, max_hops_bwd={max_hops_bwd}), "
            f"time: {elapsed_time:.3f}s"
        )
        return corridor

    def extract_paths(
        self,
        edges: list[tuple[int, int]],
        injection_node_ids: list[int],
        alarm_nodes: set[int],
    ) -> list[list[int]]:
        """Extract the SET of admissible paths from injection to alarm nodes.

        Per §7.6 step 9 the output is a set of admissible paths — no chain
        confidence scoring, no ranking. Uses DFS to enumerate all paths
        from any injection node to any alarm node within the subgraph.
        Paths terminate strictly at alarm nodes. A node may appear up to
        ``max_node_visits`` times to support diamond shapes (e.g.
        ``span -> service -> pod -> service -> span``).

        DFS short-circuits once ``self.max_paths`` admissible paths have
        been collected (§7.6 step 8 safety net) and logs a warning. With
        the corridor + activity filter applied upstream, real cases stay
        well under the default cap.

        Each path is unique by construction: the per-node visit-count cap
        prevents revisiting beyond ``max_node_visits``, and the DFS only
        records a path once each time it touches an alarm node.

        Args:
            edges: List of (src_id, dst_id) tuples from
                ``find_reachable_subgraph`` (post corridor pruning).
            injection_node_ids: Starting node IDs.
            alarm_nodes: Target alarm node IDs (DFS termination).

        Returns:
            List of unique admissible paths, each a list of node IDs.
        """
        if not edges:
            return []

        # Build adjacency list from edges
        adj: dict[int, list[int]] = {}
        all_nodes: set[int] = set()
        for src, dst in edges:
            adj.setdefault(src, []).append(dst)
            all_nodes.add(src)
            all_nodes.add(dst)

        reachable_alarms = alarm_nodes & all_nodes
        injection_set = set(injection_node_ids)
        paths: list[list[int]] = []
        max_visits = self.max_node_visits
        max_paths = self.max_paths

        # DFS from each injection node
        def dfs(node_id: int, path: list[int], visit_counts: Counter[int]) -> None:
            # Safety-net short-circuit at the start of each recursive frame.
            if len(paths) >= max_paths:
                return

            if node_id in reachable_alarms and node_id not in injection_set:
                paths.append(path.copy())
                # Continue to find longer paths through this alarm

            if len(path) > self.max_hops + 1:
                return

            for neighbor_id in adj.get(node_id, []):
                # Safety-net short-circuit inside the neighbor loop too —
                # avoids wasted recursion once the cap has fired.
                if len(paths) >= max_paths:
                    return
                # Allow visiting a node up to max_node_visits times
                if visit_counts[neighbor_id] < max_visits:
                    path.append(neighbor_id)
                    visit_counts[neighbor_id] += 1
                    dfs(neighbor_id, path, visit_counts)
                    path.pop()
                    visit_counts[neighbor_id] -= 1

        for injection_id in injection_node_ids:
            if injection_id in all_nodes:
                initial_counts: Counter[int] = Counter({injection_id: 1})
                dfs(injection_id, [injection_id], initial_counts)

        if len(paths) >= max_paths:
            logger.warning(
                "    [WARNING] extract_paths hit max_paths=%d safety cap; "
                "truncating output. Consider tightening the corridor "
                "(reduce max_hops) if this fires on real data.",
                max_paths,
            )

        return paths
