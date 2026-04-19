import random
from pathlib import Path

import polars as pl

from ..logging import logger, timeit
from .spec import Algorithm, AlgorithmAnswer, AlgorithmArgs


def find_service_names(dataset: str, input_folder: Path) -> list[str]:
    if dataset.startswith("rcaeval"):
        return rcaeval_load_service_names(input_folder)
    elif dataset.startswith("rcabench"):
        return rcabench_load_service_names(input_folder)
    else:
        raise NotImplementedError


@timeit()
def rcaeval_load_service_names(input_folder: Path) -> list[str]:
    metrics = pl.scan_parquet(input_folder / "simple_metrics.parquet").select(pl.col("service_name")).unique()
    traces = pl.scan_parquet(input_folder / "traces.parquet").select(pl.col("service_name")).unique()
    metrics, traces = pl.collect_all([metrics, traces])

    metric_service_names = set(metrics["service_name"].to_list())
    trace_service_names = set(traces["service_name"].to_list())

    service_names = metric_service_names | trace_service_names

    # Filter out None and empty strings
    service_names = {name for name in service_names if name and isinstance(name, str) and name.strip()}

    return sorted(service_names)


@timeit()
def rcabench_load_service_names(input_folder: Path) -> list[str]:
    traces = pl.scan_parquet(input_folder / "abnormal_traces.parquet").select(pl.col("service_name")).unique()
    logs = pl.scan_parquet(input_folder / "abnormal_logs.parquet").select(pl.col("service_name")).unique()
    traces, logs = pl.collect_all([traces, logs])

    trace_service_names = set(traces["service_name"].to_list())
    log_service_names = set(logs["service_name"].to_list())

    service_names = trace_service_names | log_service_names

    # Filter out None and empty strings
    service_names = {name for name in service_names if name and isinstance(name, str) and name.strip()}

    return sorted(service_names)


class Random(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 4

    @timeit()
    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        service_names = find_service_names(args.dataset, args.input_folder)

        logger.debug(f"found {len(service_names)} service names")

        random.shuffle(service_names)

        answers = [
            AlgorithmAnswer(level="service", name=name, rank=rank)  #
            for rank, name in enumerate(service_names, start=1)
        ]

        return answers
