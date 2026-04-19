import json
from typing import Any

import networkx as nx
import polars as pl

from .spec import DatasetAnalyzer, build_service_graph, get_datapack_folder


class RCAEvalAnalyzerLoader(DatasetAnalyzer):
    def __init__(self, dataset: str, datapack: str):
        super().__init__(datapack)
        self.dataset = dataset

    def get_service_dependency_graph(self) -> nx.DiGraph:
        folder = get_datapack_folder(self.dataset, self.datapack)
        assert folder is not None, "datapack folder must exist"
        traces = pl.scan_parquet(folder / "traces.parquet")
        g = build_service_graph(traces)

        root_nodes = [node for node in g.nodes() if g.in_degree(node) == 0]
        g.add_node("dummyservice")
        for root_node in root_nodes:
            g.add_edge("dummyservice", root_node)

        return g

    def get_all_services(self) -> list[str]:
        services = set()
        for key in [
            "traces",
            "simple_metrics",
        ]:
            lf = pl.scan_parquet(get_datapack_folder(self.dataset, self.datapack) / f"{key}.parquet")
            if lf is not None and "service_name" in lf.collect_schema():
                services.update(lf.select("service_name").unique().collect()["service_name"].to_list())

        services.discard("")
        return list(services)

    def get_service_metrics(self, service_name: str, abnormal: bool = False) -> dict[str, list[float]]:
        metrics_lf = pl.scan_parquet(get_datapack_folder(self.dataset, self.datapack) / "simple_metrics.parquet")
        if metrics_lf is None:
            return {}
        with open(get_datapack_folder(self.dataset, self.datapack) / "inject_time.txt") as f:
            inject_time = f.read().strip()
        # Convert Unix timestamp to datetime
        inject_time_dt = pl.from_epoch(pl.lit(int(inject_time)), time_unit="s").dt.replace_time_zone("UTC")
        if abnormal:
            metrics_lf = metrics_lf.filter(pl.col("time") > inject_time_dt)
        else:
            metrics_lf = metrics_lf.filter(pl.col("time") < inject_time_dt)
        return self._extract_service_metrics(metrics_lf, service_name)

    def get_root_services(self) -> list[str]:
        return [self.datapack.split("_")[0]]

    def get_entry_service(self) -> str | None:
        return "dummyservice"

    def _extract_service_metrics(self, metrics_lf: pl.LazyFrame, service_name: str) -> dict[str, list[float]]:
        assert isinstance(metrics_lf, pl.LazyFrame), "metrics_lf must be a polars LazyFrame"
        assert isinstance(service_name, str) and service_name.strip(), "service_name must be a non-empty string"

        schema = metrics_lf.collect_schema()
        assert "service_name" in schema, "metrics_lf must have service_name column"
        assert "metric" in schema, "metrics_lf must have metric column"
        assert "value" in schema, "metrics_lf must have value column"

        service_metrics = (
            metrics_lf.filter(pl.col("service_name") == service_name)
            .group_by("metric")
            .agg(pl.col("value").alias("values"))
            .collect()
        )

        metrics_dict = {}
        for row in service_metrics.iter_rows(named=True):
            metric_name = row["metric"]
            values = row["values"]
            filtered_values = []
            for value in values:
                if value is not None and isinstance(value, (int, float)):
                    filtered_values.append(value)
            metrics_dict[metric_name] = filtered_values
        return metrics_dict
