#!/usr/bin/env -S uv run -s
from pathlib import Path

import polars as pl
from dotenv import load_dotenv

from rcabench_platform.v3.analysis.aggregation import DuckDBAggregator, aggregate
from rcabench_platform.v3.analysis.algo_perf_vis import (
    algo_perf_by_fault_type,
    algo_perf_by_groups,
    algo_success_by_algo,
    dataset_anomaly_distribution,
)
from rcabench_platform.v3.analysis.data_prepare import (
    build_items_with_cache,
    get_execution_item,
)
from rcabench_platform.v3.analysis.detector_visualization import batch_visualization
from rcabench_platform.v3.cli.main import app, logger
from rcabench_platform.v3.sdk.utils.dataframe import format_dataframe, print_dataframe
from rcabench_platform.v3.sdk.utils.serde import save_parquet

DEFAULT_NAMESPACE = "ts"
ALGORITHMS = [
    "baro",
    "simplerca",
    "microdig",
    "microhecl",
    "microrank",
    "microrca",
    "shapleyiq",
    "eadro",
    "diagfusion",
    "art",
    "nezha",
    "causalrca",
]
METRICS = ["SDD@1", "SDD@3", "SDD@5", "CPL", "RootServiceDegree"]


load_dotenv()


@app.command(name="visualize")
def visualize(
    dataset: str,
    simple: bool,
    dataset_version: str | None = None,
    execution_tag: str | None = None,
) -> None:
    items, _ = get_execution_item(
        ALGORITHMS,
        dataset,
        dataset_version=dataset_version,
        execution_tag=execution_tag,
    )
    logger.info(f"get {len(items)} items for visualization")
    count_items = build_items_with_cache(
        output_pkl_path=Path("temp/dataset_analysis/datapacks") / "injections" / "items.pkl",
        input_items=items,
        metrics=METRICS,
        namespace=DEFAULT_NAMESPACE,
        simple=simple,
    )

    df = aggregate(count_items)
    save_parquet(df, path=f"temp/algo/aggregated_result_{simple}.parquet")


@app.command()
def analysis():
    df = pl.read_parquet("temp/algo/aggregated_result_False.parquet")

    aggregator = DuckDBAggregator(df)

    def vis_hook(df, name, fig=False):
        format_dataframe(df, "html", output_file=f"temp/algo/{name}.html")
        format_dataframe(df, "csv", output_file=f"temp/algo/{name}.csv")
        format_dataframe(df, "latex", output_file=f"temp/algo/{name}.tex")
        if fig:
            algo_perf_by_groups(df, output_file=Path(f"temp/algo/{name}.png"))

    try:
        vis_hook(aggregator.dataset_overall(), "dataset_overall")
        vis_hook(aggregator.dataset_fault_type(), "dataset_fault_type")
        vis_hook(aggregator.perf_overall(), "perf_overall")
        vis_hook(aggregator.perf_common_failures(5, 10), "common_failures")
        vis_hook(aggregator.perf_group_by_fault_type(), "perf_by_fault_type")
        algo_perf_by_fault_type(
            aggregator.perf_group_by_fault_type(),
            Path("temp/algo/rq5_perf_by_fault_type.pdf"),
        )
        algo_success_by_algo(
            aggregator.perf_group_by_fault_type(),
            Path("temp/algo/rq5_perf_by_algo.pdf"),
        )
    finally:
        aggregator.close()


@app.command()
def rq4():
    df = pl.read_parquet("temp/algo/aggregated_result_true.parquet")
    aggregator = DuckDBAggregator(df)
    try:
        format_dataframe(
            aggregator.dataset_fault_type(),
            "csv",
            output_file="temp/algo/rq4_generation_process.csv",
        )

        dataset_anomaly_distribution(
            aggregator.dataset_fault_type(),
            Path("temp/algo/rq4_generation_process.pdf"),
        )
    finally:
        aggregator.close()


if __name__ == "__main__":
    app()
