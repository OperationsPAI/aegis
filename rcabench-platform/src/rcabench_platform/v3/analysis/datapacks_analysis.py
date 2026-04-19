from dataclasses import asdict, dataclass, field
from typing import Any

import polars as pl
from rcabench.openapi import ChaosNode

from ..sdk.logging import logger
from .data_prepare import Item

# Constants
MIN_DEPTH_FOR_RANGE = 2
DATAPACK_METRIC_PREFIX = "datapack_metric_"

# Column names used throughout the module
FAULT_TYPE_COL = "fault_type"
INJECTED_SERVICE_COL = "injected_service"
IS_PAIR_COL = "is_pair"


@dataclass
class PairStats:
    in_degree: int = 0
    out_degree: int = 0


@dataclass
class Distribution:
    faults: dict[str, int] = field(default_factory=dict)
    services: dict[str, int] = field(default_factory=dict)
    pairs: dict[str, PairStats] = field(default_factory=dict)

    fault_services: dict[str, dict[str, int]] = field(default_factory=dict)
    fault_service_attribute_coverages: dict[str, dict[str, float]] = field(default_factory=dict)
    fault_service_metrics: dict[str, dict[str, dict[str, dict[str, int]]]] = field(default_factory=dict)

    fault_pairs: dict[str, dict[str, int]] = field(default_factory=dict)
    fault_pair_attribute_coverages: dict[str, dict[str, float]] = field(default_factory=dict)
    fault_pair_metrics: dict[str, dict[str, dict[str, dict[str, int]]]] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return {
            "faults": self.faults,
            "services": self.services,
            "pairs": {k: asdict(v) for k, v in self.pairs.items()},
            "fault_services": self.fault_services,
            "fault_service_attribute_coverages": self.fault_service_attribute_coverages,
            "fault_service_metrics": {
                k: {sk: sv for sk, sv in v.items()} for k, v in self.fault_service_metrics.items()
            },
            "fault_pairs": self.fault_pairs,
            "fault_pair_attribute_coverages": self.fault_pair_attribute_coverages,
            "fault_pair_metrics": {k: {sk: sv for sk, sv in v.items()} for k, v in self.fault_pair_metrics.items()},
        }


def get_datapacks_distribution(df: pl.DataFrame, metrics: list[str]) -> Distribution:
    if df.height == 0:
        return Distribution()

    distribution = Distribution()

    # Basic distributions using DataFrame operations
    distribution.faults = _get_single_column_distribution(df, FAULT_TYPE_COL)
    distribution.services = _get_single_column_distribution(df, INJECTED_SERVICE_COL)
    distribution.pairs = get_pairs_distribution(df)

    # Composite distributions
    distribution.fault_services = _get_fault_target_distribution(df, is_pair=False)
    distribution.fault_pairs = _get_fault_target_distribution(df, is_pair=True)

    # Datapack metric distributions
    distribution.fault_service_metrics = get_fault_service_metrics_distribution(df, metrics)

    return distribution


def _validate_dataframe_columns(df: pl.DataFrame, required_columns: list[str]) -> bool:
    return df.height > 0 and all(col in df.columns for col in required_columns)


def _get_single_column_distribution(df: pl.DataFrame, column_name: str) -> dict[str, int]:
    if not _validate_dataframe_columns(df, [column_name]):
        return {}

    counts = df.group_by(column_name).agg(pl.len().alias("count"))
    return {row[column_name]: row["count"] for row in counts.iter_rows(named=True)}


def get_pairs_distribution(df: pl.DataFrame) -> dict[str, PairStats]:
    if not _validate_dataframe_columns(df, [INJECTED_SERVICE_COL, IS_PAIR_COL]):
        return {}

    # Filter for pairs only
    pairs_df = df.filter(pl.col(IS_PAIR_COL))

    if pairs_df.height == 0:
        return {}

    # Split pairs and calculate degrees
    pairs_with_split = pairs_df.with_columns(
        [
            pl.col(INJECTED_SERVICE_COL).str.split("->").list.get(0).alias("source_service"),
            pl.col(INJECTED_SERVICE_COL).str.split("->").list.get(1).alias("target_service"),
        ]
    ).filter((pl.col("source_service").is_not_null()) & (pl.col("target_service").is_not_null()))

    if pairs_with_split.height == 0:
        return {}

    # Calculate out degrees (source services)
    out_degrees = (
        pairs_with_split.group_by("source_service")
        .agg(pl.len().alias("out_degree"))
        .rename({"source_service": "service"})
    )

    # Calculate in degrees (target services)
    in_degrees = (
        pairs_with_split.group_by("target_service")
        .agg(pl.len().alias("in_degree"))
        .rename({"target_service": "service"})
    )

    # Merge degrees
    all_services = out_degrees.join(in_degrees, on="service", how="full").fill_null(0)

    pairs_stats = {}
    for row in all_services.iter_rows(named=True):
        pairs_stats[row["service"]] = PairStats(in_degree=row.get("in_degree", 0), out_degree=row.get("out_degree", 0))

    return pairs_stats


def _get_fault_target_distribution(df: pl.DataFrame, is_pair: bool) -> dict[str, dict[str, int]]:
    if not _validate_dataframe_columns(df, [FAULT_TYPE_COL, INJECTED_SERVICE_COL]):
        return {}

    # Filter based on is_pair flag
    filtered_df = df.filter(pl.col(IS_PAIR_COL) == is_pair)

    if filtered_df.height == 0:
        return {}

    fault_target_counts = filtered_df.group_by([FAULT_TYPE_COL, INJECTED_SERVICE_COL]).agg(pl.len().alias("count"))

    result = {}
    for row in fault_target_counts.iter_rows(named=True):
        fault_type = row[FAULT_TYPE_COL]
        target = row[INJECTED_SERVICE_COL]
        count = row["count"]

        if fault_type not in result:
            result[fault_type] = {}
        result[fault_type][target] = count

    return result


class _NodeProcessor:
    @staticmethod
    def __call__(node: ChaosNode, **kwargs) -> Any:
        raise NotImplementedError

    @staticmethod
    def combine(results: list) -> Any:
        raise NotImplementedError


class _RangeProcessor(_NodeProcessor):
    @staticmethod
    def __call__(node: ChaosNode, **kwargs) -> int:
        return node.range[1] - node.range[0] + 1 if node.range else 0

    @staticmethod
    def combine(results: list[int]) -> int:
        return sum(results)


class _CoverageProcessor(_NodeProcessor):
    @staticmethod
    def __call__(node: ChaosNode, **kwargs) -> dict[str, bool]:
        return {f"{kwargs['key']}-{node.value}": True}

    @staticmethod
    def combine(results: list[dict]) -> dict[str, bool]:
        combined = {}
        for result in results:
            combined.update(result)
        return combined


def _traverse_node(node: ChaosNode, key: str, processor: _NodeProcessor) -> Any:
    int_key = int(key)

    if node.children is None:
        if int_key > MIN_DEPTH_FOR_RANGE:
            return processor(node, key=key)
        else:
            # Return appropriate default value based on processor type
            if isinstance(processor, _RangeProcessor):
                return 0
            elif isinstance(processor, _CoverageProcessor):
                return {}
            else:
                return {}

    results = []
    for child_key, child_node in node.children.items():
        results.append(_traverse_node(child_node, child_key, processor))

    return processor.combine(results)


def _get_fault_target_coverages(coverage_df: pl.DataFrame, is_pair: bool) -> dict[str, dict[str, float]]:
    if coverage_df.height == 0:
        return {}

    # Filter based on is_pair flag
    filtered_coverage = coverage_df.filter(pl.col(IS_PAIR_COL) == is_pair)

    result = {}
    for row in filtered_coverage.iter_rows(named=True):
        fault_type = row[FAULT_TYPE_COL]
        target = row[INJECTED_SERVICE_COL]
        coverage = row["coverage"]

        if fault_type not in result:
            result[fault_type] = {}
        result[fault_type][target] = coverage

    return result


def get_fault_service_coverages(
    coverage_df: pl.DataFrame,
) -> dict[str, dict[str, float]]:
    return _get_fault_target_coverages(coverage_df, is_pair=False)


def get_fault_pair_coverages(coverage_df: pl.DataFrame) -> dict[str, dict[str, float]]:
    return _get_fault_target_coverages(coverage_df, is_pair=True)


def get_fault_service_metrics_distribution(
    df: pl.DataFrame, metrics: list[str]
) -> dict[str, dict[str, dict[str, dict[str, int]]]]:
    if df.height == 0:
        return {}

    # Filter for pairs only (as per original logic)
    pairs_df = df.filter(pl.col(IS_PAIR_COL))

    if pairs_df.height == 0:
        return {}

    # Find datapack metric columns
    datapack_metric_cols = [col for col in df.columns if col.startswith(DATAPACK_METRIC_PREFIX)]

    # Filter metrics that actually exist in the DataFrame
    available_metrics = []
    for metric in metrics:
        metric_col = f"{DATAPACK_METRIC_PREFIX}{metric}"
        if metric_col in datapack_metric_cols:
            available_metrics.append(metric)

    if not available_metrics:
        return {}

    result = {}

    # Group by fault type and service to get metric distributions
    for (fault_type_obj, service_obj), group_df in pairs_df.group_by([FAULT_TYPE_COL, INJECTED_SERVICE_COL]):
        fault_type = str(fault_type_obj)
        service = str(service_obj)

        if fault_type not in result:
            result[fault_type] = {}

        if service not in result[fault_type]:
            result[fault_type][service] = {}

        # For each metric, calculate value distribution
        for metric in available_metrics:
            metric_col = f"{DATAPACK_METRIC_PREFIX}{metric}"

            # Get value counts for this metric
            metric_values = group_df.select(metric_col).to_series()
            value_counts = metric_values.value_counts().sort("count", descending=True)

            # Convert to the expected format
            distribution_dict = {}
            for row in value_counts.iter_rows(named=True):
                value = str(row[metric_col])
                count = row["count"]
                distribution_dict[value] = count

            result[fault_type][service][metric] = distribution_dict

    return result
