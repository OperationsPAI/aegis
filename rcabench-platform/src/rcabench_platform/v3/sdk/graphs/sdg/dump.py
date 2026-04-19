from dataclasses import asdict
from typing import Any

import polars as pl

from ...logging import logger
from .defintion import SDG, DepEdge, PlaceKind, PlaceNode


def dump_place_indicators(sdg: SDG) -> pl.DataFrame:
    rows = []
    data_keys: dict[str, Any] = {}

    for node in sdg.iter_nodes():
        row_base = {
            "node.id": node.id,
            "place.kind": node.kind,
            "place.name": node.self_name,
        }

        if len(node.indicators) == 0:
            row = {
                "indicator.name": None,
                "indicator.df.length": None,
                **row_base,
            }
            continue

        for indicator in node.indicators.values():
            row = {
                "indicator.name": indicator.name,
                "indicator.df.length": len(indicator.df),
                **row_base,
            }

            for k, v in indicator.data.items():
                if v is None:
                    continue

                assert isinstance(v, (int, float, str))
                row[f"indicator.data.{k}"] = v

                prev_type = data_keys.get(k)
                if prev_type is None or prev_type is type(None):
                    data_keys[k] = type(v)
                else:
                    assert isinstance(v, prev_type), f"Type mismatch for {k}: {v} ({type(v)}) vs {prev_type}"

            rows.append(row)

    schema = {
        "node.id": pl.UInt32,
        "place.kind": pl.String,
        "place.name": pl.String,
        "indicator.name": pl.String,
        "indicator.df.length": pl.UInt64,
    }

    for k, v in data_keys.items():
        if v is str:
            dt = pl.Utf8
        elif v is int:
            dt = pl.Int64
        elif v is float:
            dt = pl.Float64
        else:
            logger.error(f"Unknown type for {k}: {v}")
            raise NotImplementedError
        schema[f"indicator.data.{k}"] = dt

    df = pl.DataFrame(rows, schema=schema)
    return df


def replace_large_set(data: dict[str, Any]):
    for k, v in data.items():
        if isinstance(v, set) and len(v) >= 16:
            data[k] = {"len": len(v), "content": "<large set>"}
        elif isinstance(v, dict):
            replace_large_set(v)


def replace_large_df(data: dict[str, Any]):
    for k, v in data.items():
        if isinstance(v, pl.DataFrame):
            data[k] = {
                "len": len(v),
                "schema": v.schema,
                "content": "<large DataFrame>",
            }
        elif isinstance(v, dict):
            replace_large_df(v)


def dump_edge_data(edge: DepEdge) -> dict[str, Any]:
    data = asdict(edge)
    replace_large_set(data)
    replace_large_df(data)
    return data


def dump_node_data(node: PlaceNode) -> dict[str, Any]:
    data = asdict(node)
    replace_large_set(data)
    replace_large_df(data)
    return data


def dump_span_tree(sdg: SDG, top: PlaceNode, trace_id) -> dict[str, Any] | None:
    assert top.kind == PlaceKind.function
    indicator = top.indicators["duration"]

    trace = indicator.df.filter(pl.col("trace_id") == trace_id)
    if len(trace) == 0:
        return

    self_duration_sum = trace["value"].sum()
    self_duration_sum /= 1e6  # ns -> ms

    children = []

    children_duration_sum = 0

    for edge in sdg.out_edges(top.id):
        dst = sdg.get_node_by_id(edge.dst_id)
        if dst.kind == PlaceKind.function:
            ans = dump_span_tree(sdg, dst, trace_id)
            if ans:
                children.append(ans)
                children_duration_sum += ans["self.duration.sum"]

    min_time = trace["time"].min()
    max_time = trace["time"].max()

    return {
        "name": top.self_name,
        "min_start_time": min_time,
        "max_start_time": max_time,
        "self.duration.sum": self_duration_sum,
        "children.duration.sum": children_duration_sum,
        "children": children,
    }
