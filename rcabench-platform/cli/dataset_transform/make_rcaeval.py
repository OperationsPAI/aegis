#!/usr/bin/env -S uv run -s
from pathlib import Path

import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import convert_datapack, convert_dataset
from rcabench_platform.v3.internal.sources.rcaeval import (
    RcaevalDatapackLoader,
    RcaevalDatasetLoader,
)
from rcabench_platform.v3.sdk.datasets.spec import get_dataset_meta_file, read_dataset_index
from rcabench_platform.v3.sdk.utils.serde import save_parquet


@app.command()
@timeit()
def run(skip_finished: bool = True, parallel: int = 4):
    src_root = Path("data") / "RCAEval"
    src_datasets = ["RE2-TT", "RE2-OB", "RE2-SS", "RE3-TT", "RE3-OB", "RE3-SS"]

    for src_dataset in src_datasets:
        dst_dataset = "rcaeval_" + src_dataset.lower().replace("-", "_")
        loader = RcaevalDatasetLoader(src_folder=src_root / src_dataset, dataset=dst_dataset)
        convert_dataset(loader, skip_finished=skip_finished, parallel=parallel)


@app.command()
@timeit()
def local_test_1():
    loader = RcaevalDatapackLoader(
        src_folder=Path("data/RCAEval/RE2-TT/ts-auth-service_cpu/1"),
        dataset="rcaeval_re2_tt",
        datapack="ts-auth-service_cpu_1",
        service="ts-auth-service",
    )
    convert_datapack(
        loader,
        dst_folder=Path("temp/rcaeval_re2_tt/ts-auth-service_cpu_1"),
        skip_finished=False,
    )


@app.command()
@timeit()
def local_test_2():
    loader = RcaevalDatapackLoader(
        src_folder=Path("data/RCAEval/RE2-OB/checkoutservice_cpu/1"),
        dataset="rcaeval_re2_ob",
        datapack="checkoutservice_cpu_1",
        service="checkoutservice",
    )
    convert_datapack(
        loader,
        dst_folder=Path("temp/rcaeval_re2_ob/checkoutservice_cpu_1"),
        skip_finished=False,
    )


@app.command()
@timeit()
def collect_fault_types():
    for dataset in [
        "rcaeval_re2_tt",
        "rcaeval_re2_ob",
        "rcaeval_re2_ss",
        "rcaeval_re3_tt",
        "rcaeval_re3_ob",
        "rcaeval_re3_ss",
    ]:
        df = read_dataset_index(dataset)
        df = df.with_columns(pl.col("datapack").str.split("_").list.get(1).alias("fault_type"))
        save_parquet(df, path=get_dataset_meta_file(dataset, "attributes.parquet"))


if __name__ == "__main__":
    app()
