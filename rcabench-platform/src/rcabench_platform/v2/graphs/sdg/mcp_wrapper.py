from collections.abc import Callable, Iterable
from itertools import chain
from typing import Any, Literal

from .defintion import SDG, DepEdge, DepKind, Indicator, PlaceKind, PlaceNode
from .statistics import STAT_PREFIX


class MCPWrapper:
    def __init__(self, sdg: SDG):
        self._sdg = sdg

    def __getattr__(self, name):
        return getattr(self._sdg, name)

    def __dir__(self):
        return list(set(dir(self.__class__)) | set(dir(self._sdg)))

    def mcp_get_node_stat(self, node_id: int):
        node = self._sdg.get_node_by_id(node_id)
        ans = {}
        ans["name"] = node.self_name
        ans["type"] = node.kind
        for k, v in node.data.items():
            for prefix in STAT_PREFIX:
                if k.startswith(prefix):
                    ans[k] = v
                    break

        return ans

    def mcp_get_edge_stat(self, edge_id: int):
        edge = self._sdg.get_edge_by_id(edge_id)

        ans = {}
        ans["type"] = edge.kind
        for k, v in edge.data.items():
            for prefix in STAT_PREFIX:
                if k.startswith(prefix):
                    ans[k] = v
                    break

        return ans

    def mcp_get_node_edges(self, node_id: int, direction: Literal["in", "out", "both"] = "both"):
        iterables: list[Iterable[DepEdge]] = []
        if direction == "in" or direction == "both":
            iterables.append(self._sdg.in_edges(node_id))
        if direction == "out" or direction == "both":
            iterables.append(self._sdg.out_edges(node_id))

        ans: list[dict[str, Any]] = []
        for edge in chain(*iterables):
            src = self._sdg.get_node_by_id(edge.src_id)
            dst = self._sdg.get_node_by_id(edge.dst_id)
            ans.append(
                {
                    "id": edge.id,
                    "src_id": edge.src_id,
                    "src_name": src.self_name,
                    "src_type": src.kind,
                    "dst_id": edge.dst_id,
                    "dst_name": dst.self_name,
                    "dst_type": dst.kind,
                    "kind": edge.kind,
                }
            )

        return ans

    def mcp_get_paths(self, src_id: int, dst_id: int, directed: bool):
        return self._sdg.all_simple_paths(src_id, dst_id, directed=directed)

    def mcp_get_suspicious_nodes(self, attribute: str, fn: Callable[[float, float], bool]):
        """
        fn is a function, left side is normal node value, right side is abnormal node value. Values are all numbers,
        llm can customize thresholds to define what is abnormal
        """
        ans = []
        for node in self._sdg.iter_nodes():
            values = [node.data.get(f"{prefix}.{attribute}") for prefix in STAT_PREFIX]
            if values[0] is None or values[1] is None:
                continue
            normal_value = float(values[0])
            anomal_value = float(values[1])
            if fn(normal_value, anomal_value):
                ans.append(node.id)
        info = [self.mcp_get_node_stat(i) for i in ans]
        return info

    def mcp_get_avail_attributes(self):
        return self._sdg.data["node_stat_names"]
