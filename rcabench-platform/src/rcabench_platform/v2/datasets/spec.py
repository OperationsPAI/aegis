import shutil
from abc import ABC, abstractmethod
from dataclasses import dataclass
from pathlib import Path

import networkx as nx
import polars as pl

from ..config import get_config
from ..logging import timeit


@dataclass(kw_only=True, slots=True, frozen=True)
class Label:
    level: str
    name: str


def get_dataset_meta_folder(dataset: str) -> Path:
    config = get_config()
    return config.data / "meta" / dataset


def get_dataset_meta_file(dataset: str, filename: str) -> Path:
    return get_dataset_meta_folder(dataset) / filename


def get_dataset_index_path(dataset: str) -> Path:
    return get_dataset_meta_file(dataset, "index.parquet")


def get_dataset_labels_path(dataset: str) -> Path:
    return get_dataset_meta_file(dataset, "labels.parquet")


def get_dataset_folder(dataset: str) -> Path:
    config = get_config()
    return config.data / "data" / dataset


def get_datapack_folder(dataset: str, datapack: str) -> Path:
    return get_dataset_folder(dataset) / datapack


def predefined_dependency() -> nx.DiGraph:
    graph = nx.DiGraph()
    predefined_edges = {
        "ts-ui-dashboard": [
            "ts-voucher-service",
            "ts-travel-plan-service",
            "ts-execute-service",
            "ts-news-service",
            "ts-ticket-office-service",
            "ts-gateway-service",
        ],
        "ts-admin-basic-info-service": [
            "ts-config-service",
            "ts-contacts-service",
            "ts-price-service",
            "ts-station-service",
            "ts-train-service",
        ],
        "ts-admin-order-service": ["ts-order-other-service", "ts-order-service"],
        "ts-admin-route-service": ["ts-route-service", "ts-station-service"],
        "ts-admin-travel-service": [
            "ts-route-service",
            "ts-station-service",
            "ts-train-service",
            "ts-travel2-service",
            "ts-travel-service",
        ],
        "ts-admin-user-service": ["ts-user-service"],
        "ts-auth-service": ["ts-verification-code-service"],
        "ts-basic-service": [
            "ts-price-service",
            "ts-route-service",
            "ts-station-service",
            "ts-train-service",
        ],
        "ts-cancel-service": [
            "ts-inside-payment-service",
            "ts-notification-service",
            "ts-order-other-service",
            "ts-order-service",
            "ts-user-service",
        ],
        "ts-consign-service": ["ts-consign-price-service"],
        "ts-execute-service": ["ts-order-other-service", "ts-order-service"],
        "ts-food-delivery-service": ["ts-station-food-service", "ts-rabbitmq"],
        "ts-food-service": [
            "ts-station-food-service",
            "ts-train-food-service",
            "ts-travel-service",
            "ts-rabbitmq",
        ],
        "ts-inside-payment-service": [
            "ts-order-other-service",
            "ts-order-service",
            "ts-payment-service",
        ],
        "ts-order-other-service": ["ts-station-service"],
        "ts-order-service": ["ts-station-service"],
        "ts-preserve-other-service": [
            "ts-assurance-service",
            "ts-basic-service",
            "ts-consign-service",
            "ts-contacts-service",
            "ts-food-service",
            "ts-order-other-service",
            "ts-seat-service",
            "ts-security-service",
            "ts-station-service",
            "ts-travel2-service",
            "ts-user-service",
        ],
        "ts-preserve-service": [
            "ts-assurance-service",
            "ts-basic-service",
            "ts-consign-service",
            "ts-contacts-service",
            "ts-food-service",
            "ts-order-service",
            "ts-seat-service",
            "ts-security-service",
            "ts-station-service",
            "ts-travel-service",
            "ts-user-service",
            "ts-rabbitmq",
        ],
        "ts-rebook-service": [
            "ts-inside-payment-service",
            "ts-order-other-service",
            "ts-order-service",
            "ts-route-service",
            "ts-seat-service",
            "ts-train-service",
            "ts-travel2-service",
            "ts-travel-service",
        ],
        "ts-route-plan-service": [
            "ts-route-service",
            "ts-travel2-service",
            "ts-travel-service",
        ],
        "ts-seat-service": [
            "ts-config-service",
            "ts-order-other-service",
            "ts-order-service",
        ],
        "ts-security-service": ["ts-order-other-service", "ts-order-service"],
        "ts-travel2-service": [
            "ts-basic-service",
            "ts-route-service",
            "ts-seat-service",
            "ts-train-service",
        ],
        "ts-travel-plan-service": [
            "ts-route-plan-service",
            "ts-seat-service",
            "ts-train-service",
            "ts-travel2-service",
            "ts-travel-service",
        ],
        "ts-travel-service": [
            "ts-basic-service",
            "ts-route-service",
            "ts-seat-service",
            "ts-train-service",
        ],
        "ts-user-service": ["ts-auth-service"],
        "loadgenerator": ["ts-ui-dashboard"],
    }

    # Add predefined edges to the graph
    for source_service, target_services in predefined_edges.items():
        for target_service in target_services:
            graph.add_edge(source_service, target_service)

    mysql_connected_services = [
        "ts-assurance-service",
        "ts-auth-service",
        "ts-config-service",
        "ts-consign-price-service",
        "ts-consign-service",
        "ts-contacts-service",
        "ts-delivery-service",
        "ts-food-delivery-service",
        "ts-food-service",
        "ts-inside-payment-service",
        "ts-notification-service",
        "ts-order-other-service",
        "ts-order-service",
        "ts-payment-service",
        "ts-price-service",
        "ts-route-service",
        "ts-security-service",
        "ts-station-food-service",
        "ts-station-service",
        "ts-train-food-service",
        "ts-train-service",
        "ts-travel2-service",
        "ts-travel-service",
        "ts-user-service",
        "ts-wait-order-service",
    ]

    for service in mysql_connected_services:
        graph.add_edge(service, "mysql")
    return graph


def get_dataset_list() -> list[str]:
    config = get_config()
    meta_folder = config.data / "meta"
    datasets = [d.name for d in meta_folder.iterdir() if d.is_dir()]
    return datasets


def get_datapack_list(dataset: str) -> list[str]:
    index_df = read_dataset_index(dataset)

    ans = []
    for row in index_df.iter_rows(named=True):
        assert row["dataset"] == dataset
        assert isinstance(row["datapack"], str)
        datapack = row["datapack"]
        ans.append(datapack)

    return ans


def get_datapack_labels(dataset: str, datapack: str) -> list[Label]:
    labels_path = get_dataset_labels_path(dataset)

    labels_df = (
        pl.scan_parquet(labels_path)
        .filter(
            pl.col("dataset") == dataset,
            pl.col("datapack") == datapack,
        )
        .collect()
    )

    assert len(labels_df) >= 1, f"Labels for datapack `{datapack}` not found in dataset `{dataset}`"

    labels_set = set()
    labels_list = []
    for level, name in labels_df.select("gt.level", "gt.name").iter_rows():
        assert isinstance(level, str)
        assert isinstance(name, str)
        labels_set.add((level, name))
        labels_list.append(Label(level=level, name=name))

    assert len(labels_set) == len(labels_list), f"Duplicate labels found in `{dataset}/{datapack}`"

    return labels_list


def read_dataset_index(dataset: str) -> pl.DataFrame:
    index_path = get_dataset_index_path(dataset)
    index_df = pl.read_parquet(index_path)
    return index_df


def read_dataset_labels(dataset: str) -> pl.DataFrame:
    labels_path = get_dataset_labels_path(dataset)
    labels_df = pl.read_parquet(labels_path)
    return labels_df


@timeit()
def delete_dataset(dataset: str):
    meta_folder = get_dataset_meta_folder(dataset)
    data_folder = get_dataset_folder(dataset)

    shutil.rmtree(meta_folder, ignore_errors=True)
    shutil.rmtree(data_folder, ignore_errors=True)


def build_service_graph(trace_lf: pl.LazyFrame) -> nx.DiGraph:
    lf = trace_lf.select(
        "span_id",
        "parent_span_id",
        "service_name",
    ).filter(pl.col("parent_span_id").is_not_null())

    lf = lf.join(
        lf.select("span_id", pl.col("service_name").alias("parent_service_name")),
        left_on="parent_span_id",
        right_on="span_id",
        how="inner",
    )

    edges_df = (
        lf.select("parent_service_name", "service_name")
        .filter(
            pl.col("parent_service_name") != pl.col("service_name")  # Exclude self-calls
        )
        .unique()
        .collect()
    )

    graph = predefined_dependency()

    for parent_service, child_service in edges_df.iter_rows():
        graph.add_edge(parent_service, child_service)

    return graph


def calculate_trace_length(df: pl.DataFrame) -> list[int]:
    trace_depths = []

    df = df.with_columns(
        pl.when(pl.col("parent_span_id") == "").then(None).otherwise(pl.col("parent_span_id")).alias("parent_span_id")
    )

    for trace_group in df.group_by("trace_id", maintain_order=False):
        trace_data = trace_group[1]

        span_depths = _compute_span_depths(trace_data)
        max_depth = max(span_depths.values()) if span_depths else 1
        trace_depths.append(max_depth)

    return trace_depths


def calculate_trace_service_count(df: pl.LazyFrame) -> list[int]:
    df_processed = df.select(["trace_id", "span_id", "parent_span_id", "service_name"]).with_columns(
        pl.when(pl.col("parent_span_id") == "").then(None).otherwise(pl.col("parent_span_id")).alias("parent_span_id")
    )

    df_collected = df_processed.collect()

    if len(df_collected) == 0:
        return []

    trace_service_counts = []

    for trace_group in df_collected.group_by("trace_id", maintain_order=False):
        trace_id, trace_data = trace_group

        if len(trace_data) == 0:
            trace_service_counts.append(0)
            continue

        spans_info = trace_data.select(["span_id", "parent_span_id", "service_name"])
        if len(spans_info) == 1:
            trace_service_counts.append(1)
            continue

        span_ids = spans_info["span_id"].to_list()
        parent_span_ids = spans_info["parent_span_id"].to_list()
        service_names = spans_info["service_name"].to_list()

        span_to_service = {sid: sname for sid, sname in zip(span_ids, service_names)}
        span_to_parent = {sid: pid for sid, pid in zip(span_ids, parent_span_ids)}

        all_spans = set(span_ids)
        parent_spans = {pid for pid in parent_span_ids if pid is not None}
        leaf_spans = all_spans - parent_spans

        if not leaf_spans:
            root_spans = {sid for sid, pid in span_to_parent.items() if pid is None}
            leaf_spans = root_spans if root_spans else {span_ids[0]}

        max_path_services_count = 0

        for leaf_span in leaf_spans:
            path_services = set()
            current_span = leaf_span
            visited = set()

            while current_span is not None and current_span not in visited:
                visited.add(current_span)
                service = span_to_service.get(current_span)
                if service:
                    path_services.add(service)
                current_span = span_to_parent.get(current_span)

            max_path_services_count = max(max_path_services_count, len(path_services))

        trace_service_counts.append(max_path_services_count)

    return trace_service_counts


def _compute_span_depths(trace_df: pl.DataFrame) -> dict[str, int]:
    spans_data = {
        row["span_id"]: row["parent_span_id"]
        for row in trace_df.select(["span_id", "parent_span_id"]).iter_rows(named=True)
    }

    span_depths = {}

    root_spans = [span_id for span_id, parent_id in spans_data.items() if parent_id is None]

    queue = [(span_id, 1) for span_id in root_spans]  # (span_id, depth)
    processed = set()

    while queue:
        current_span, current_depth = queue.pop(0)

        if current_span in processed:
            continue

        processed.add(current_span)
        span_depths[current_span] = current_depth

        children = [
            span_id
            for span_id, parent_id in spans_data.items()
            if parent_id == current_span and span_id not in processed
        ]

        for child in children:
            queue.append((child, current_depth + 1))

    for span_id in spans_data:
        if span_id not in span_depths:
            span_depths[span_id] = 1

    return span_depths


class DatasetAnalyzer(ABC):
    def __init__(self, datapack: str):
        assert isinstance(datapack, str) and datapack.strip(), "datapack must be a non-empty string"
        self.datapack: str = datapack

    @abstractmethod
    def get_all_services(self) -> list[str]:
        raise NotImplementedError

    @abstractmethod
    def get_service_metrics(self, service_name: str, abnormal: bool = False) -> dict[str, list[float]]:
        raise NotImplementedError

    @abstractmethod
    def get_root_services(self) -> list[str]:
        raise NotImplementedError

    @abstractmethod
    def get_entry_service(self) -> str | None:
        raise NotImplementedError

    @abstractmethod
    def get_service_dependency_graph(self) -> nx.DiGraph:
        raise NotImplementedError

    def get_datapack(self) -> str:
        return self.datapack
