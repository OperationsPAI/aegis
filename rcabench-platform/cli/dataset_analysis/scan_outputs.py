#!/usr/bin/env -S uv run -s
from fractions import Fraction

import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.sdk.datasets.spec import get_datapack_list
from rcabench_platform.v3.sdk.experiments.report import get_output_meta_folder
from rcabench_platform.v3.sdk.experiments.single import get_output_folder
from rcabench_platform.v3.sdk.utils.serde import save_parquet


@app.command()
@timeit()
def scan_rcaeval_ranks(dataset: str, algorithm: str):
    datapacks = get_datapack_list(dataset)

    lf_list: list[pl.LazyFrame] = []
    for datapack in datapacks:
        output_folder = get_output_folder(dataset, datapack, algorithm)

        perf = pl.read_parquet(output_folder / "perf.parquet")
        ac5 = perf["AC@5.count"].item()
        if ac5 is None:
            continue
        assert isinstance(ac5, (int, float))
        if not ac5:
            continue

        lf = pl.scan_parquet(output_folder / "ranks.parquet")
        lf = lf.select(
            pl.lit(dataset).alias("dataset"),
            pl.lit(datapack).alias("datapack"),
            pl.all(),
        )
        lf_list.append(lf)

    ranks = pl.concat(lf_list).collect()

    ranks = ranks.with_columns(pl.col("node_name").str.split("_").list.get(1).alias("metric"))

    save_parquet(ranks, path=get_output_meta_folder(dataset) / f"{algorithm}.ranks.parquet")

    df_list: list[pl.DataFrame] = []
    for rank in range(1, 5 + 1):
        df = ranks.filter(pl.col("rank") <= rank)
        total = len(df)
        df = df.select(pl.col("metric").value_counts()).unnest("metric")
        df = df.with_columns(
            pl.lit(rank).alias("rank"),
            pl.col("count").truediv(total).round(6).alias("proportion"),
        )
        df_list.append(df)

    df = pl.concat(df_list).sort(by=["rank", "proportion"], descending=[False, True])
    save_parquet(df, path=get_output_meta_folder(dataset) / f"{algorithm}.ranks.summary.parquet")


@app.command()
@timeit()
def compare_traceback():
    meta_folder = get_output_meta_folder("rcabench_filtered")
    df = pl.read_parquet(meta_folder / "fault_types.perf.parquet")

    index = "injection.fault_type"

    df = df.filter(pl.col("algorithm").str.contains("traceback"))

    total = df.select(index, "total").unique().sort(by=index)

    df = df.pivot(on="algorithm", index=index, values=["MRR", "AC@1", "AC@3", "AC@5"])

    df = total.join(df, on=index, how="left")

    save_parquet(df, path=meta_folder / "compare.parquet")


if __name__ == "__main__":
    app()
