import functools
import os
from collections import Counter
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from pathlib import Path
from typing import Literal

import polars as pl
from rcabench.openapi import (
    BatchEvaluateDatasetReq,
    ChaosGroundtruth,
    ChaosNode,
    ChaosPair,
    ChaosResourceField,
    ChaosResources,
    ContainerRef,
    DatasetRef,
    EvaluateDatasetSpec,
    EvaluationsApi,
    GranularityResultItem,
    InjectionDetailResp,
    InjectionsApi,
    LabelItem,
    SearchInjectionReq,
)

from ..clients.rcabench_ import get_rcabench_client
from ..datasets.spec import calculate_trace_length, calculate_trace_service_count
from ..metrics.algo_metrics import AlgoMetricItem, calculate_metrics_for_level
from ..utils.env import debug, getenv_int
from ..utils.fmap import fmap_processpool
from ..utils.profiler import global_profiler, print_profiler_stats
from ..utils.serde import save_pickle

if debug():
    _DEFAULT_ITEMS_CACHE_TIME = 600
else:
    _DEFAULT_ITEMS_CACHE_TIME = 0

ITEMS_CACHE_TIME = getenv_int("ITEMS_CACHE_TIME", default=_DEFAULT_ITEMS_CACHE_TIME)


@dataclass
class InputItem:
    injection: InjectionDetailResp
    algo_durations: dict[str, float]  # algorithm -> execution_duration
    algo_evals: dict[str, tuple[list[ChaosGroundtruth], list[GranularityResultItem]]] | None = None


@dataclass
class Item:
    # Required fields (no default values)
    _injection: InjectionDetailResp
    _node: ChaosNode

    # Optional fields with default values
    fault_type: str = ""
    injected_service: str = ""
    is_pair: bool = False
    anomaly_degree: Literal["absolute", "may", "no"] = "no"
    workload: Literal["trainticket"] = "trainticket"

    # Algo Metric statistics
    _algo_evals: dict[str, tuple[list[ChaosGroundtruth], list[GranularityResultItem]]] | None = None
    _algo_durations: dict[str, float] = field(default_factory=dict)
    algo_metrics: dict[str, AlgoMetricItem] = field(default_factory=dict)

    # Data statistics
    duration: timedelta = timedelta(seconds=0)  # duration in seconds
    trace_count: int = 0  # number of traces
    service_names: set[str] = field(default_factory=set)
    service_names_by_trace: set[str] = field(default_factory=set)  # trace

    # Datapack Metric statistics
    datapack_metric_values: dict[str, int] = field(default_factory=dict)  # metric_name -> value

    # Injection Metric statistics
    injection_metric_counts: dict[str, int] = field(default_factory=dict)  # metric_name -> count

    # Log statistics
    log_lines: dict[str, int] = field(default_factory=dict)  # service_name -> log_lines

    # Trace depth statistics
    trace_length: Counter[int] = field(default_factory=Counter)

    service_length: Counter[int] = field(default_factory=Counter)

    def __post_init__(self):
        if self._algo_evals is None:
            self._algo_evals = {}
            return

        self.algo_metrics = {}
        for algo, (groundtruths, predictions) in self._algo_evals.items():
            # Collect all groundtruth services from all groundtruth items
            # This handles multiple pod injections correctly
            groundtruth_services = []
            for groundtruth in groundtruths:
                assert groundtruth.service is not None
                # Add all services from this groundtruth (usually just one, but could be multiple)
                groundtruth_services.extend(groundtruth.service)

            # Remove duplicates while preserving order
            seen = set()
            unique_groundtruth_services = []
            for service in groundtruth_services:
                if service not in seen:
                    seen.add(service)
                    unique_groundtruth_services.append(service)

            # Calculate metrics with all groundtruth services
            metric_item = calculate_metrics_for_level(
                groundtruth_items=unique_groundtruth_services,
                predictions=predictions,
                level="service",
            )

            if algo in self._algo_durations:
                metric_item.time = self._algo_durations[algo]
            self.algo_metrics[algo] = metric_item

    @property
    def node(self) -> ChaosNode:
        return self._node

    @property
    def qps(self) -> float:
        if self.duration > timedelta(seconds=0):
            return self.trace_count / self.duration.total_seconds()
        return 0.0

    @property
    def qpm(self) -> float:
        if self.duration > timedelta(seconds=0):
            return self.trace_count / self.duration.total_seconds() * 60
        return 0.0

    @property
    def service_coverage(self) -> float:
        if not self.service_names or len(self.service_names) == 0:
            return 0.0
        return len(self.service_names_by_trace) / len(self.service_names)


def get_execution_item(
    algorithms: list[str],
    dataset: str,
    algorithm_versions: list[str] | None = None,
    dataset_version: str | None = None,
    execution_tag: str | None = None,
) -> tuple[list[InputItem], dict[str, list[str]]]:
    """
    Retrieves and structures the latest execution and evaluation results for multiple
    algorithms on a specified dataset.

    Fetches evaluation data, identifies the latest execution for each algorithm on
    each datapack, collects execution metadata (duration, groundtruth, predictions),
    and maps this data to Injection metadata to produce a list of InputItem objects
    for analysis.

    Args:
        algorithms: List of algorithm names.
        dataset: Target dataset name.
        algorithm_versions: Optional, corresponding list of algorithm versions.
        dataset_version: Optional, target dataset version.
        execution_tag: Optional, tag for filtering execution results.

    Returns:
        Tuple of (input_items, unrunned_algos):
        - input_items: Structured list of InputItem objects, one per datapack/injection.
        - unrunned_algos: Dictionary mapping algorithm names to lists of datapack names that failed to execute.
    """
    client = get_rcabench_client()
    evaluations_api = EvaluationsApi(client)
    injections_api = InjectionsApi(client)

    if algorithm_versions is None:
        av = [None] * len(algorithms)
    else:
        # Ensure algorithms and algorithm_versions have the same length
        if len(algorithms) != len(algorithm_versions):
            raise ValueError("The number of algorithms and algorithm versions must be the same")
        av = algorithm_versions

    resp = evaluations_api.evaluate_algorithm_on_datasets(
        request=BatchEvaluateDatasetReq(
            specs=[
                EvaluateDatasetSpec(
                    algorithm=ContainerRef(name=algorithms[i], version=av[i]),
                    dataset=DatasetRef(name=dataset, version=dataset_version),
                    filter_labels=[LabelItem(key="tag", value=execution_tag)] if execution_tag else None,
                )
                for i in range(len(algorithms))
            ]
        )
    )

    assert resp.code is not None and resp.code < 300 and resp.data is not None
    items = resp.data.success_items
    assert items is not None and len(items) > 0

    unrunned_algos: dict[str, list[str]] = {}

    # Collect data grouped by datapack_name
    # datapack_name -> { algorithm -> (execution_duration, groundtruth, predictions) }
    datapack_algo_data: dict[
        str,
        dict[str, tuple[float, list[ChaosGroundtruth], list[GranularityResultItem]]],
    ] = {}

    for item in items:
        assert item.algorithm is not None
        if item.not_executed_datapacks:
            if item.algorithm not in unrunned_algos:
                unrunned_algos[item.algorithm] = []
            else:
                unrunned_algos[item.algorithm].extend(item.not_executed_datapacks)

        if item.evalaute_refs is None or len(item.evalaute_refs) == 0:
            continue

        for eval_ref in item.evalaute_refs:
            assert eval_ref.datapack is not None
            assert eval_ref.groundtruths is not None
            assert eval_ref.execution_refs is not None and len(eval_ref.execution_refs) > 0

            execution_refs = sorted(
                eval_ref.execution_refs,
                key=lambda r: datetime.fromisoformat(r.executed_at) if r.executed_at else datetime.min,
                reverse=True,
            )
            execution_ref = execution_refs[0]

            assert execution_ref.execution_duration is not None
            assert execution_ref.predictions is not None

            datapack_name = eval_ref.datapack
            if datapack_name not in datapack_algo_data:
                datapack_algo_data[datapack_name] = {}

            datapack_algo_data[datapack_name][item.algorithm] = (
                execution_ref.execution_duration,
                eval_ref.groundtruths,
                execution_ref.predictions,
            )

    datapack_names = list(datapack_algo_data.keys())
    resp = injections_api.search_injections(search=SearchInjectionReq(names=datapack_names))
    assert resp.code is not None and resp.code < 300 and resp.data is not None and resp.data.items is not None
    datapacks = resp.data.items

    assert len(datapacks) == len(datapack_names), "Mismatch in number of datapacks retrieved"
    datapack_mapping = {dp.name: dp for dp in datapacks if dp.name is not None}

    # Build InputItem list
    input_items: list[InputItem] = []
    for datapack_name, algo_data in datapack_algo_data.items():
        algo_durations: dict[str, float] = {}
        algo_evals: dict[str, tuple[list[ChaosGroundtruth], list[GranularityResultItem]]] = {}

        for algo, (duration, groundtruths, predictions) in algo_data.items():
            algo_durations[algo] = duration
            algo_evals[algo] = (groundtruths, predictions)

        input_items.append(
            InputItem(
                injection=datapack_mapping[datapack_name],
                algo_durations=algo_durations,
                algo_evals=algo_evals,
            )
        )

    return input_items, unrunned_algos


def get_individual_service(
    individual: ChaosNode, fault_type: str, fault_resource: list[str] | list[ChaosPair]
) -> tuple[str, bool]:
    fault_type_index = str(individual.value)

    assert individual.children is not None
    child_node = individual.children[fault_type_index]

    assert child_node.children is not None
    service_index = child_node.children["2"].value

    assert fault_resource is not None
    assert service_index is not None
    assert service_index < len(fault_resource), (
        f"Service index {service_index} out of bounds for fault resource {len(fault_resource)}"
    )
    service = fault_resource[service_index]
    if isinstance(service, str):
        return service, False

    assert service.source is not None or service.target is not None, (
        f"Service source or target is None for fault {fault_type} with index {service_index}"
    )
    return f"{service.source}->{service.target}", True


def get_resources(
    namespace: str,
) -> tuple[dict[str, ChaosResourceField], ChaosResources]:
    client = get_rcabench_client()
    api = InjectionsApi(client)

    resp = api.get_injection_metadata(namespace)
    assert resp.code is not None and resp.code < 300 and resp.data is not None
    meta_data = resp.data

    fault_resource_mapping = meta_data.fault_resource_map
    assert fault_resource_mapping is not None

    ns_resources = meta_data.ns_resources
    assert ns_resources is not None

    return fault_resource_mapping, ns_resources


def process_item(
    algo_evals: dict[str, tuple[list[ChaosGroundtruth], list[GranularityResultItem]]] | None,
    algo_durations: dict[str, float],
    injection: InjectionDetailResp,
    fault_resource_mapping: dict[str, ChaosResourceField],
    ns_resources: ChaosResources,
    metrics: list[str],
    simple: bool = False,
) -> Item | None:
    if not injection.engine_config or not injection.name:
        return None

    datapack_path = Path("data/rcabench_dataset") / injection.name / "converted"

    profiler = global_profiler
    with profiler.profile("prepare"):
        node = ChaosNode.from_json(str(injection.engine_config))
        assert node is not None

        fault_type = injection.fault_type
        assert fault_type is not None

        fault_resource_name = fault_resource_mapping[fault_type].name
        assert fault_resource_name is not None
        fault_resource = ns_resources.to_dict().get(fault_resource_name)
        assert fault_resource is not None and isinstance(fault_resource, list)

        service, is_pair = get_individual_service(node, fault_type, fault_resource)

        service_names: set[str] = set()
        service_names_by_trace: set[str] = set()
        trace_length: Counter[int] = Counter()
        duration: timedelta = timedelta(seconds=0)
        trace_count: int = 0
        log_lines: dict[str, int] = {}
        datapack_metric_values: dict[str, int] = {}
        injection_metric_counts: dict[str, int] = {}
        trace_service_length = Counter()

        assert injection.labels is not None
        tags = [label.value for label in injection.labels if label.key == "tag" and label.value]
        label_mapping = {label.key: label.value for label in injection.labels if label.key and label.value}

        if not simple:
            for metric in metrics:
                value = 0
                value_str = label_mapping.get(metric)
                if value_str is not None:
                    try:
                        value = int(value_str)
                    except ValueError:
                        if value_str.lower() in ("inf", "infinity", "+inf"):
                            value = 999999999
                        elif value_str.lower() in ("-inf", "-infinity"):
                            value = -999999999
                        else:
                            try:
                                float_value = float(value_str)
                                if float_value == float("inf") or float_value == float("-inf"):
                                    value = 999999999
                                else:
                                    value = int(float_value)
                            except ValueError:
                                value = 0

                datapack_metric_values[metric] = value

            metric_df = pl.concat(
                [
                    pl.scan_parquet(datapack_path / "normal_metrics.parquet"),
                    pl.scan_parquet(datapack_path / "abnormal_metrics.parquet"),
                    pl.scan_parquet(datapack_path / "normal_metrics_sum.parquet"),
                    pl.scan_parquet(datapack_path / "abnormal_metrics_sum.parquet"),
                ]
            )
            with profiler.profile("scan_metric"):
                service_names.update(set(metric_df.select("service_name").unique().collect().to_series().to_list()))

                metric_count_df = metric_df.select("metric").collect()
                injection_metric_counts: dict[str, int] = dict(
                    metric_count_df.group_by("metric").agg(pl.len().alias("count")).iter_rows()
                )
            with profiler.profile("scan_trace"):
                trace_df = pl.concat(
                    [
                        pl.scan_parquet(datapack_path / "normal_traces.parquet"),
                        pl.scan_parquet(datapack_path / "abnormal_traces.parquet"),
                    ]
                )

                trace_service_names = set(trace_df.select("service_name").unique().collect().to_series().to_list())
                service_names_by_trace.update(trace_service_names)
                service_names.update(trace_service_names)

                trace_count = (
                    trace_df.filter((pl.col("parent_span_id") == "").or_(pl.col("parent_span_id").is_null()))
                    .select(pl.len())
                    .collect()
                    .item()
                )

                trace_spans = trace_df.select(["trace_id", "span_id", "parent_span_id"]).collect()
                depth_results = calculate_trace_length(trace_spans)
                service_length = calculate_trace_service_count(trace_df)

                trace_service_length = Counter(service_length)
                trace_length = Counter(depth_results)

                min_time = trace_df.select(pl.col("time").min().alias("min_time")).collect().item()
                max_time = trace_df.select(pl.col("time").max().alias("max_time")).collect().item()
                duration = max_time - min_time

            with profiler.profile("scan_log"):
                log_df = pl.concat(
                    [
                        pl.scan_parquet(datapack_path / "normal_logs.parquet"),
                        pl.scan_parquet(datapack_path / "abnormal_logs.parquet"),
                    ]
                )

                log_service_counts = log_df.group_by("service_name").agg(pl.len().alias("count")).collect()
                log_lines: dict[str, int] = {
                    row["service_name"]: row["count"] for row in log_service_counts.iter_rows(named=True)
                }
                log_service_names = set(log_df.select("service_name").unique().collect().to_series().to_list())
                service_names.update(log_service_names)
                service_names.remove("")

        anomaly_degree = "no"
        if "absolute_anomaly" in tags:
            anomaly_degree = "absolute"
        elif "may_anomaly" in tags:
            anomaly_degree = "may"

    return Item(
        _algo_evals=algo_evals,
        _algo_durations=algo_durations,
        _injection=injection,
        _node=node,  # type: ignore
        fault_type=fault_type,
        injected_service=service,
        is_pair=is_pair,
        anomaly_degree=anomaly_degree,
        duration=duration,
        trace_count=trace_count,
        service_names=service_names,
        service_names_by_trace=service_names_by_trace,
        log_lines=log_lines,
        datapack_metric_values=datapack_metric_values,
        injection_metric_counts=injection_metric_counts,
        trace_length=trace_length,
        service_length=trace_service_length,
    )


def batch_process_item(
    input_items: list[InputItem],
    metrics: list[str],
    namespace: str,
    simple: bool = False,
) -> list[Item]:
    fault_resource_mapping, ns_resources = get_resources(namespace)

    tasks = [
        functools.partial(
            process_item,
            input_item.algo_evals,
            input_item.algo_durations,
            input_item.injection,
            fault_resource_mapping,
            ns_resources,
            metrics,
            simple,
        )
        for input_item in input_items
    ]

    cpu = os.cpu_count()
    assert cpu is not None, "CPU count must not be None"
    res = fmap_processpool(tasks, parallel=cpu // 2, cpu_limit_each=2, ignore_exceptions=True)

    filtered_results = [i for i in res if i is not None]

    print_profiler_stats()
    return filtered_results


def build_items_with_cache(
    output_pkl_path: Path,
    input_items: list[InputItem],
    metrics: list[str],
    namespace: str,
    simple: bool = False,
) -> list[Item]:
    if not output_pkl_path.parent.exists():
        output_pkl_path.parent.mkdir(parents=True, exist_ok=True)

    # if has_recent_file(output_pkl_path, seconds=3600):
    #     return load_pickle(path=output_pkl_path)

    items = batch_process_item(input_items, metrics, namespace, simple)

    save_pickle(items, path=output_pkl_path)

    return items
