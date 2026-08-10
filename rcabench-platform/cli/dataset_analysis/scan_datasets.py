#!/usr/bin/env -S uv run -s
import functools
from typing import Any

import matplotlib.pyplot as plt
import polars as pl
from matplotlib.axes import Axes
from tqdm.auto import tqdm

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.sdk.datasets.spec import (
    get_datapack_folder,
    get_datapack_labels,
    get_datapack_list,
    get_dataset_meta_file,
)
from rcabench_platform.v3.sdk.utils.fmap import fmap_threadpool
from rcabench_platform.v3.sdk.utils.serde import save_parquet


@app.command()
@timeit()
def scan_rows_count(dataset: str):
    datapacks = get_datapack_list(dataset)

    tasks = [functools.partial(_scan_rows_count, dataset, datapack) for datapack in datapacks]
    results = fmap_threadpool(tasks, parallel=32)

    df = pl.DataFrame(results)
    save_parquet(df, path=get_dataset_meta_file(dataset, "rows_count.parquet"))


@timeit()
def _scan_rows_count(dataset: str, datapack: str) -> dict[str, Any]:
    datapack_folder = get_datapack_folder(dataset, datapack)

    lf_map: dict[str, pl.LazyFrame] = {}

    if dataset.startswith("rcaeval"):
        lf_map["traces"] = pl.scan_parquet(datapack_folder / "traces.parquet")
        lf_map["metrics"] = pl.scan_parquet(datapack_folder / "simple_metrics.parquet")
    elif dataset.startswith("rcabench"):
        normal_traces = pl.scan_parquet(datapack_folder / "normal_traces.parquet")
        abnormal_traces = pl.scan_parquet(datapack_folder / "abnormal_traces.parquet")

        normal_metrics = pl.scan_parquet(datapack_folder / "normal_metrics.parquet")
        abnormal_metrics = pl.scan_parquet(datapack_folder / "abnormal_metrics.parquet")

        normal_logs = pl.scan_parquet(datapack_folder / "normal_logs.parquet")
        abnormal_logs = pl.scan_parquet(datapack_folder / "abnormal_logs.parquet")

        lf_map["traces"] = pl.concat([normal_traces, abnormal_traces])
        lf_map["metrics"] = pl.concat([normal_metrics, abnormal_metrics])
        lf_map["logs"] = pl.concat([normal_logs, abnormal_logs])
    else:
        raise NotImplementedError

    ans = {
        "dataset": dataset,
        "datapack": datapack,
    }

    names = list(lf_map.keys())
    for name in names:
        lf = lf_map[name]
        del lf_map[name]

        count = lf.select(pl.len()).collect().item()
        ans[name + ".rows"] = count

    return ans


@app.command()
@timeit()
def plot_rows_count(show: bool = True):
    datasets = ["rcaeval_re2_tt", "rcabench_filtered"]

    df_list = []
    for dataset in datasets:
        df = pl.read_parquet(get_dataset_meta_file(dataset, "rows_count.parquet"))
        df_list.append(df)

    columns = ["traces.rows", "metrics.rows"]

    fig, axes = plt.subplots(1, len(columns), figsize=(20, 10))
    for i, column in enumerate(columns):
        ax: Axes = axes[i]
        for j, df in enumerate(df_list):
            ax.boxplot(df[column], positions=[j], widths=0.6, vert=True)
        ax.set_title(column)
        ax.set_xticks(range(len(df_list)))
        ax.set_xticklabels(datasets)
    plt.tight_layout()
    plt.savefig("temp/rows_count.png")

    if show:
        plt.show()


@app.command()
@timeit()
def scan_metric_names(dataset: str):
    datapacks = get_datapack_list(dataset)

    tasks = [functools.partial(_scan_metric_names, dataset, datapack) for datapack in datapacks]
    results = fmap_threadpool(tasks, parallel=32)

    metric_names = set()
    for result in results:
        metric_names.update(result)

    df = pl.DataFrame({"dataset": dataset, "metric": sorted(metric_names)})
    save_parquet(df, path=get_dataset_meta_file(dataset, "metric_names.parquet"))


def _scan_metric_names(dataset: str, datapack: str) -> set[str]:
    datapack_folder = get_datapack_folder(dataset, datapack)

    if dataset.startswith("rcaeval"):
        metrics = pl.scan_parquet(datapack_folder / "simple_metrics.parquet").select("metric")
    elif dataset.startswith("rcabench"):
        files = [
            "normal_metrics.parquet",
            "abnormal_metrics.parquet",
            "normal_metrics_sum.parquet",
            "abnormal_metrics_sum.parquet",
            "normal_metrics_histogram.parquet",
            "abnormal_metrics_histogram.parquet",
        ]
        metrics = pl.concat([pl.scan_parquet(datapack_folder / file).select("metric") for file in files])
    else:
        raise NotImplementedError

    metric_names = metrics.select(pl.col("metric").unique().sort()).collect().to_series().to_list()
    return set(metric_names)


@app.command()
@timeit()
def validate_datapacks(dataset: str):
    datapacks = get_datapack_list(dataset)

    count = 0
    for datapack in tqdm(datapacks):
        try:
            _ = get_datapack_labels(dataset, datapack)
        except Exception as e:
            logger.error(f"Error validating datapack {datapack} in dataset {dataset}: {repr(e)}")
            count += 1
            continue
    if count > 0:
        # print proportion of failed datapacks
        logger.error(f"Validation failed for {count} out of {len(datapacks)} datapacks in dataset {dataset}.")


@app.command()
@timeit()
def scan_normal_traces_duration(dataset: str):
    assert dataset.startswith("rcabench") or dataset.startswith("rcaeval")
    datapacks = get_datapack_list(dataset)

    tasks = [functools.partial(_scan_normal_duration, dataset, datapack) for datapack in datapacks]
    results = fmap_threadpool(tasks, parallel=32)

    df = pl.DataFrame(results)

    attributes = pl.read_parquet(get_dataset_meta_file(dataset, "attributes.parquet"))
    df = df.join(attributes, on="datapack", how="left")

    save_parquet(df, path=get_dataset_meta_file(dataset, "normal_traces_duration.parquet"))


def _scan_normal_duration(dataset: str, datapack: str) -> dict[str, Any]:
    datapack_folder = get_datapack_folder(dataset, datapack)

    if dataset.startswith("rcabench"):
        lf = pl.scan_parquet(datapack_folder / "normal_traces.parquet")
    elif dataset.startswith("rcaeval"):
        from rcabench_platform.v3.sdk.graphs.sdg.build_.rcaeval import load_inject_time

        inject_time = load_inject_time(datapack_folder)
        lf = pl.scan_parquet(datapack_folder / "traces.parquet")
        lf = lf.filter(pl.col("time") <= inject_time)
    else:
        raise NotImplementedError

    lf = lf.select(
        pl.col("duration").max().alias("max"),
        pl.col("duration").min().alias("min"),
        pl.col("duration").mean().alias("mean"),
        pl.col("duration").median().alias("median"),
    )
    df = lf.collect()
    row = df.row(0, named=True)

    ans = {"datapack": datapack}
    for k, v in row.items():
        ans["normal_traces_duration:" + k] = v

    return ans


if __name__ == "__main__":
    app()
