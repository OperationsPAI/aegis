import itertools
from collections import defaultdict
from collections.abc import Iterable
from dataclasses import dataclass, field
from enum import auto
from typing import Any, TypeAlias

import networkx as nx
import polars as pl
from typing_extensions import Self

from ....compat import StrEnum
from ...logging import logger


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

    function = auto()
    """function"""


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
    """function -> function"""

    includes = auto()
    """service -> function"""

    related_to = auto()
    """service -> (stateful_set | deployment)"""


@dataclass(kw_only=True, slots=True)
class Indicator:
    name: str

    df: pl.DataFrame

    data: dict[str, Any] = field(default_factory=dict)


@dataclass(kw_only=True, slots=True)
class PlaceNode:
    id: int = 0

    uniq_name: str = ""

    kind: PlaceKind

    self_name: str

    indicators: dict[str, Indicator] = field(default_factory=dict)

    data: dict[str, Any] = field(default_factory=dict)

    def add_indicator(self, indicator: Indicator, strict: bool = True) -> None:
        if strict:
            assert indicator.name not in self.indicators
        self.indicators[indicator.name] = indicator


@dataclass(kw_only=True, slots=True)
class DepEdge:
    id: int = 0

    src_id: int

    dst_id: int

    kind: DepKind

    data: dict[str, Any] = field(default_factory=dict)


class SDG:
    def __init__(self) -> None:
        self._graph = nx.MultiDiGraph()

        # node id generator
        self._node_id_gen = itertools.count(1)

        # edge id generator
        self._edge_id_gen = itertools.count(1)

        # node.id -> node
        self._node_id_map: dict[int, PlaceNode] = {}

        # edge.id -> edge
        self._edge_id_map: dict[int, DepEdge] = {}

        # node.uniq_name -> node
        self._node_name_map: dict[str, PlaceNode] = {}

        # node.kind -> set of node ids
        self._node_kind_map: defaultdict[PlaceKind, set[int]] = defaultdict(set)

        # edge.kind -> set of edge ids
        self._edge_kind_map: defaultdict[DepKind, set[int]] = defaultdict(set)

        # custom data
        self.data: dict[str, Any] = {}

    def add_node(self, node: PlaceNode, *, strict: bool = True) -> PlaceNode:
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

        logger.debug(f"add node: id=`{node.id}` kind=`{node.kind}` self_name=`{node.self_name}`")

        self._graph.add_node(node.id, ref=node)
        self._node_id_map[node.id] = node
        self._node_name_map[node.uniq_name] = node
        self._node_kind_map[node.kind].add(node.id)

        return node

    def add_edge(self, edge: DepEdge, *, strict: bool = True) -> DepEdge:
        assert edge.src_id in self._node_id_map
        assert edge.dst_id in self._node_id_map

        edge_data = self._graph.edges.get((edge.src_id, edge.dst_id, edge.kind))
        if strict:
            assert edge_data is None
        else:
            if edge_data:
                return edge_data["ref"]

        if not edge.id:
            edge.id = next(self._edge_id_gen)
        assert edge.id not in self._edge_id_map

        logger.debug(
            f"add edge: id=`{edge.id}` "
            f"`{self.get_node_by_id(edge.src_id).uniq_name}` "
            f"--({edge.kind})->"
            f"`{self.get_node_by_id(edge.dst_id).uniq_name}`"
        )

        self._graph.add_edge(edge.src_id, edge.dst_id, edge.kind, ref=edge)
        self._edge_id_map[edge.id] = edge
        self._edge_kind_map[edge.kind].add(edge.id)

        return edge

    def num_nodes(self) -> int:
        return self._graph.number_of_nodes()

    def num_edges(self) -> int:
        return self._graph.number_of_edges()

    def has_node(self, node_id: int) -> bool:
        return self._graph.has_node(node_id)

    def has_edge(self, src_id: int, dst_id: int, kind: DepKind | None = None) -> bool:
        return self._graph.has_edge(src_id, dst_id, kind)

    def iter_nodes(self) -> Iterable[PlaceNode]:
        return self._node_id_map.values()

    def iter_edges(self) -> Iterable[DepEdge]:
        return self._edge_id_map.values()

    def get_node_by_id(self, node_id: int) -> PlaceNode:
        return self._node_id_map[node_id]

    def get_edge_by_id(self, edge_id: int) -> DepEdge:
        return self._edge_id_map[edge_id]

    def get_node_kind_by_id(self, node_id: int) -> PlaceKind:
        return self.get_node_by_id(node_id).kind

    def query_node_by_kind(self, kind: PlaceKind, self_name: str) -> PlaceNode | None:
        uniq_name = f"{kind}|{self_name}"
        return self._node_name_map.get(uniq_name)

    def get_all_nodes_by_kind(self, kind: PlaceKind) -> Iterable[PlaceNode]:
        s = self._node_kind_map[kind]
        return (self.get_node_by_id(node_id) for node_id in s)

    def out_edges(self, node_id: int) -> Iterable[DepEdge]:
        for u, v, d in self._graph.out_edges(node_id, data=True):
            yield d["ref"]

    def in_edges(self, node_id: int) -> Iterable[DepEdge]:
        for u, v, d in self._graph.in_edges(node_id, data=True):  # type: ignore
            yield d["ref"]

    def query_node_by_uniq_name(self, uniq_name: str) -> PlaceNode | None:
        return self._node_name_map.get(uniq_name)

    def iter_edges_between(self, src: PlaceNode, dst: PlaceNode) -> Iterable[DepEdge]:
        for u, v, d in self._graph.out_edges(src.id, data=True):
            if v == dst.id:
                yield d["ref"]

    def all_simple_paths(self, src_id: int, dst_id: int, directed: bool = True) -> list[list[int]]:
        if directed:
            g = self._graph
        else:
            g = self._graph.to_undirected(as_view=True)

        ans = nx.all_simple_paths(g, source=src_id, target=dst_id, cutoff=self._graph.number_of_nodes())
        return list(ans)


ExpandedGraphPath: TypeAlias = list[PlaceNode | DepEdge]


@dataclass(kw_only=True, slots=True, frozen=True)
class GraphPath:
    node: PlaceNode
    prev: tuple[DepEdge, Self] | None

    @classmethod
    def from_single_node(cls, node: PlaceNode) -> Self:
        return cls(node=node, prev=None)

    def move(self, edge: DepEdge, node: PlaceNode) -> Self:
        assert edge.src_id == self.node.id or edge.dst_id == self.node.id
        return self.__class__(node=node, prev=(edge, self))

    def expand(self) -> ExpandedGraphPath:
        this = self
        ans: ExpandedGraphPath = [self.node]

        while this.prev:
            edge, prev = this.prev
            ans.append(edge)
            ans.append(prev.node)
            this = prev

        ans.reverse()

        return ans
