import itertools
import logging
from collections import defaultdict
from datetime import datetime
from enum import auto
from typing import Any

import networkx as nx  # type: ignore[import-untyped]
import numpy as np
from pydantic import BaseModel, Field

from rcabench_platform.compat import StrEnum

logger = logging.getLogger(__name__)


class PlaceKind(StrEnum):
    machine = auto()
    """k8s node"""

    namespace = auto()
    """k8s namespace"""

    stateful_set = auto()
    """k8s stateful set"""

    deployment = auto()
    """k8s deployment"""

    replica_set = auto()
    """k8s replica set"""

    service = auto()
    """k8s service"""

    pod = auto()
    """k8s pod"""

    container = auto()
    """k8s container"""

    pvc = auto()
    """k8s persistent volume claim"""

    pv = auto()
    """k8s persistent volume"""

    configmap = auto()
    """k8s config map"""

    secret = auto()
    """k8s secret"""

    ingress = auto()
    """k8s ingress"""

    endpoints = auto()
    """k8s endpoints"""

    daemon_set = auto()
    """k8s daemon set"""

    function = auto()
    """function"""

    span = auto()
    """distributed tracing span"""


class DepKind(StrEnum):
    owns = auto()
    """namespace -> (service | stateful_set | deployment)"""

    routes_to = auto()
    """service -> pod"""

    scales = auto()
    """deployment -> replica_set"""

    manages = auto()
    """(stateful_set | replica_set) -> pod"""

    schedules = auto()
    """machine -> pod"""

    runs = auto()
    """pod -> container"""

    claims = auto()
    """pod -> pvc"""

    binds_to = auto()
    """pvc -> pv"""

    calls = auto()
    """(function | span) -> (function | span) - API call relationships in distributed tracing"""

    includes = auto()
    """service -> (function | span) - service contains API endpoints/spans"""

    related_to = auto()
    """service -> (stateful_set | deployment)"""

    mounts = auto()
    """container -> (pvc | configmap | secret)"""

    exposes = auto()
    """service -> endpoints"""

    depends_on = auto()
    """generic dependency relationship"""

    affects = auto()
    """resource interference/impact relationship"""


_STRUCTURAL_MEDIATOR_KINDS: frozenset[PlaceKind] = frozenset(
    {PlaceKind.pod, PlaceKind.replica_set, PlaceKind.deployment}
)
"""PlaceKinds that participate in the cascade structure but have no per-entity
timeline in the IR (state on them is asserted via inferred-level rollup from
pods/containers, never via direct observation channels).

Used by: drift gate exemption, deviating-set inclusion, first-hop strictness
in path_builder. Single source of truth: change here, all sites pick it up.
"""


def is_structural_mediator(kind: PlaceKind) -> bool:
    """Whether ``kind`` is a topological mediator without its own timeline.

    Pod, replica_set and deployment exist in the graph because they are part
    of the k8s containment cascade structure, not because any adapter
    observes a state on them directly. Multiple sites need to special-case
    them — see ``_STRUCTURAL_MEDIATOR_KINDS`` for rationale.
    """
    return kind in _STRUCTURAL_MEDIATOR_KINDS


class EdgeData(BaseModel):
    pass


class CallsEdgeData(EdgeData):
    # Baseline (normal period) statistics
    baseline_call_count: int = Field(default=0, ge=0, description="baseline total calls")
    baseline_error_count: int = Field(default=0, ge=0, description="baseline errors (5xx status)")
    baseline_avg_latency: float = Field(default=0.0, ge=0, description="baseline average latency in seconds")
    baseline_median_latency: float = Field(default=0.0, ge=0, description="baseline median (p50) latency in seconds")
    baseline_p90_latency: float = Field(default=0.0, ge=0, description="baseline p90 latency in seconds")
    baseline_p99_latency: float = Field(default=0.0, ge=0, description="baseline p99 latency in seconds")

    # Abnormal period statistics
    abnormal_call_count: int = Field(default=0, ge=0, description="abnormal total calls")
    abnormal_error_count: int = Field(default=0, ge=0, description="abnormal errors (5xx status)")
    abnormal_avg_latency: float = Field(default=0.0, ge=0, description="abnormal average latency in seconds")
    abnormal_median_latency: float = Field(default=0.0, ge=0, description="abnormal median (p50) latency in seconds")
    abnormal_p90_latency: float = Field(default=0.0, ge=0, description="abnormal p90 latency in seconds")
    abnormal_p99_latency: float = Field(default=0.0, ge=0, description="abnormal p99 latency in seconds")

    @property
    def baseline_qps(self) -> float:
        return float(self.baseline_call_count)

    @property
    def baseline_error_rate(self) -> float:
        return self.baseline_error_count / self.baseline_call_count if self.baseline_call_count > 0 else 0.0

    @property
    def abnormal_qps(self) -> float:
        return float(self.abnormal_call_count)

    @property
    def abnormal_error_rate(self) -> float:
        return self.abnormal_error_count / self.abnormal_call_count if self.abnormal_call_count > 0 else 0.0


class Node(BaseModel):
    model_config = {"arbitrary_types_allowed": True}

    id: int | None = Field(default=None, description="node identifier")
    kind: PlaceKind = Field(..., description="node type")
    self_name: str = Field(..., description="node's own name")
    uniq_name: str = Field(default="", description="unique identifier combining kind and name")
    baseline_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = Field(
        default_factory=dict,
        description="baseline (normal period) time-series metrics. "
        "metric_name -> (timestamps, values). "
        "timestamps: Unix seconds (shape: n); values: metric measurements (shape: n). ",
    )
    abnormal_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = Field(
        default_factory=dict,
        description="abnormal period time-series metrics. "
        "metric_name -> (timestamps, values). "
        "timestamps: Unix seconds (shape: n); values: metric measurements (shape: n). ",
    )


class Edge(BaseModel):
    id: int | None = Field(default=None, description="edge identifier")
    src_id: int = Field(..., description="source node ID")
    dst_id: int = Field(..., description="target node ID")
    src_name: str = Field(..., description="source node unique name")
    dst_name: str = Field(..., description="target node unique name")
    kind: DepKind = Field(..., description="edge type")
    weight: float = Field(default=1.0, ge=0, description="edge weight")
    data: CallsEdgeData | None = Field(..., description="type-specific metadata (CallsEdgeData, AffectsEdgeData, etc.)")


class HyperGraph:
    def __init__(self) -> None:
        self._graph: nx.MultiDiGraph = nx.MultiDiGraph()

        # node id generator
        self._node_id_gen = itertools.count(1)

        # edge id generator
        self._edge_id_gen = itertools.count(1)

        # node.id -> node
        self._node_id_map: dict[int, Node] = {}

        # edge.id -> edge
        self._edge_id_map: dict[int, Edge] = {}

        # node.uniq_name -> node
        self._node_name_map: dict[str, Node] = {}

        # node.kind -> set of node ids
        self._node_kind_map: defaultdict[PlaceKind, set[int]] = defaultdict(set)

        # edge.kind -> set of edge ids
        self._edge_kind_map: defaultdict[DepKind, set[int]] = defaultdict(set)

        # custom data
        self.data: dict[str, Any] = {}

    def add_node(self, node: Node, *, strict: bool = True) -> Node:
        if not node.uniq_name:
            node.uniq_name = f"{node.kind}|{node.self_name}"

        if strict:
            assert node.uniq_name not in self._node_name_map
        else:
            prev = self._node_name_map.get(node.uniq_name)
            if prev:
                return prev

        if not node.id:
            node.id = next(self._node_id_gen)
        assert node.id not in self._node_id_map

        self._graph.add_node(node.id, ref=node)
        self._node_id_map[node.id] = node
        self._node_name_map[node.uniq_name] = node
        self._node_kind_map[node.kind].add(node.id)

        return node

    def add_edge(self, edge: Edge, *, strict: bool = True) -> Edge:
        assert edge.src_id in self._node_id_map
        assert edge.dst_id in self._node_id_map

        edge_data = self._graph.edges.get((edge.src_id, edge.dst_id, edge.kind))
        if strict:
            assert edge_data is None
        else:
            if edge_data:
                return edge_data["ref"]  # type: ignore[no-any-return]

        if not edge.id:
            edge.id = next(self._edge_id_gen)
        assert edge.id not in self._edge_id_map

        src_node = self.get_node_by_id(edge.src_id)
        dst_node = self.get_node_by_id(edge.dst_id)
        assert src_node is not None and dst_node is not None

        self._graph.add_edge(edge.src_id, edge.dst_id, edge.kind, ref=edge)
        self._edge_id_map[edge.id] = edge
        self._edge_kind_map[edge.kind].add(edge.id)

        return edge

    def get_node_by_id(self, node_id: int) -> Node:
        node = self._node_id_map.get(node_id)
        assert node is not None and node.id is not None
        return node

    def get_node_by_name(self, uniq_name: str) -> Node | None:
        node = self._node_name_map.get(uniq_name)
        if node is not None:
            assert node.id is not None
        return node

    def get_edge_by_id(self, edge_id: int) -> Edge | None:
        return self._edge_id_map.get(edge_id)

    def get_nodes_by_kind(self, kind: PlaceKind) -> list[Node]:
        node_ids = self._node_kind_map.get(kind, set())
        return [self._node_id_map[nid] for nid in node_ids]

    def get_edges_by_kind(self, kind: DepKind) -> list[Edge]:
        edge_ids = self._edge_kind_map.get(kind, set())
        return [self._edge_id_map[eid] for eid in edge_ids]

    def find_paths(self, source_id: int, target_id: int, max_depth: int = 5) -> list[list[int]]:
        if source_id not in self._node_id_map or target_id not in self._node_id_map:
            return []

        try:
            paths = nx.all_simple_paths(self._graph, source_id, target_id, cutoff=max_depth)
            return [list(path) for path in paths]
        except nx.NetworkXNoPath:
            return []
        except nx.NodeNotFound:
            return []

    def extract_subgraph(self, source_ids: list[int], target_ids: list[int], max_depth: int = 5) -> "HyperGraph":
        forward_reachable = set()
        for source_id in source_ids:
            if source_id in self._node_id_map:
                # BFS with depth limit
                forward_reachable.add(source_id)
                for target_id in self._node_id_map.keys():
                    paths = self.find_paths(source_id, target_id, max_depth)
                    if paths:
                        for path in paths:
                            forward_reachable.update(path)

        # Compute backward reachable nodes from all targets
        backward_reachable = set()
        for target_id in target_ids:
            if target_id in self._node_id_map:
                # Reverse BFS with depth limit
                backward_reachable.add(target_id)
                for source_id in self._node_id_map.keys():
                    paths = self.find_paths(source_id, target_id, max_depth)
                    if paths:
                        for path in paths:
                            backward_reachable.update(path)

        # Keep intersection: nodes reachable in both directions
        relevant_nodes = forward_reachable & backward_reachable

        # Build new subgraph
        subgraph = HyperGraph()
        subgraph.data = self.data.copy()

        # Add relevant nodes
        for node_id in relevant_nodes:
            node = self._node_id_map[node_id]
            subgraph.add_node(node.model_copy(deep=True), strict=False)

        # Add edges where both endpoints are in relevant_nodes
        for edge in self._edge_id_map.values():
            if edge.src_id in relevant_nodes and edge.dst_id in relevant_nodes:
                subgraph.add_edge(edge.model_copy(deep=True), strict=False)

        return subgraph


class GraphEditType(StrEnum):
    add_node = auto()
    remove_node = auto()
    add_edge = auto()
    remove_edge = auto()


class GraphEdit(BaseModel):
    edit_type: GraphEditType = Field(..., description="type of edit operation")
    timestamp: datetime | None = Field(default=None, description="when the edit occurred")
    node: Node | None = Field(default=None, description="node for add_node")
    edge: Edge | None = Field(default=None, description="edge for add_edge")
    node_id: int | None = Field(default=None, description="node_id for remove_node")
    edge_id: int | None = Field(default=None, description="edge_id for remove_edge")
    _backup: dict[str, Any] = {}

    def apply(self, graph: HyperGraph) -> bool:
        try:
            if self.edit_type == GraphEditType.add_node:
                assert self.node is not None
                graph.add_node(self.node, strict=False)
            elif self.edit_type == GraphEditType.remove_node:
                assert self.node_id is not None
                node = graph.get_node_by_id(self.node_id)
                if not node:
                    return False
                self._backup["node"] = node.model_dump()
                del graph._node_id_map[self.node_id]
                del graph._node_name_map[node.uniq_name]
                graph._node_kind_map[node.kind].discard(self.node_id)
                graph._graph.remove_node(self.node_id)
            elif self.edit_type == GraphEditType.add_edge:
                assert self.edge is not None
                graph.add_edge(self.edge, strict=False)
            elif self.edit_type == GraphEditType.remove_edge:
                assert self.edge_id is not None
                edge = graph.get_edge_by_id(self.edge_id)
                if not edge:
                    return False
                self._backup["edge"] = edge.model_dump()
                del graph._edge_id_map[self.edge_id]
                graph._edge_kind_map[edge.kind].discard(self.edge_id)
                if graph._graph.has_edge(edge.src_id, edge.dst_id, edge.kind):
                    graph._graph.remove_edge(edge.src_id, edge.dst_id, edge.kind)
            return True
        except Exception as e:
            logger.error(f"failed to apply {self.edit_type}: {e}")
            return False

    def inverse(self) -> "GraphEdit":
        if self.edit_type == GraphEditType.add_node:
            assert self.node is not None
            return GraphEdit(
                edit_type=GraphEditType.remove_node,
                node_id=self.node.id,
                timestamp=self.timestamp,
            )
        elif self.edit_type == GraphEditType.remove_node:
            return GraphEdit(
                edit_type=GraphEditType.add_node,
                node=Node(**self._backup["node"]),
                timestamp=self.timestamp,
            )
        elif self.edit_type == GraphEditType.add_edge:
            assert self.edge is not None
            return GraphEdit(
                edit_type=GraphEditType.remove_edge,
                edge_id=self.edge.id,
                timestamp=self.timestamp,
            )
        elif self.edit_type == GraphEditType.remove_edge:
            return GraphEdit(
                edit_type=GraphEditType.add_edge,
                edge=Edge(**self._backup["edge"]),
                timestamp=self.timestamp,
            )
        raise ValueError(f"unknown edit type: {self.edit_type}")


class GraphEditSequence(BaseModel):
    edits: list[GraphEdit] = Field(default_factory=list, description="ordered edit operations")
    start_timestamp: datetime | None = Field(default=None, description="sequence start time")
    end_timestamp: datetime | None = Field(default=None, description="sequence end time")

    def add(self, edit: GraphEdit) -> None:
        self.edits.append(edit)
        if edit.timestamp:
            if not self.start_timestamp or edit.timestamp < self.start_timestamp:
                self.start_timestamp = edit.timestamp
            if not self.end_timestamp or edit.timestamp > self.end_timestamp:
                self.end_timestamp = edit.timestamp

    def apply(self, graph: HyperGraph) -> list[bool]:
        return [edit.apply(graph) for edit in self.edits]

    def apply_until(self, graph: HyperGraph, timestamp: datetime) -> list[bool]:
        results = []
        for edit in self.edits:
            if edit.timestamp and edit.timestamp > timestamp:
                break
            results.append(edit.apply(graph))
        return results

    def inverse(self) -> "GraphEditSequence":
        return GraphEditSequence(
            edits=[edit.inverse() for edit in reversed(self.edits)],
            start_timestamp=self.end_timestamp,
            end_timestamp=self.start_timestamp,
        )

    def __len__(self) -> int:
        return len(self.edits)

    def __getitem__(self, idx: int) -> GraphEdit:
        return self.edits[idx]

    def __iter__(self):  # type: ignore[override]
        return iter(self.edits)
