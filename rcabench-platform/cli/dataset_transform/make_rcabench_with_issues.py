#!/usr/bin/env -S uv run -s
import shutil
from fractions import Fraction

import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import link_subset
from rcabench_platform.v3.sdk.datasets.rcabench import FAULT_TYPES
from rcabench_platform.v3.sdk.datasets.spec import (
    get_dataset_folder,
    get_dataset_meta_file,
    read_dataset_index,
)
from rcabench_platform.v3.sdk.utils.serde import save_parquet


def get_analysis_with_issues():
    from rcabench.openapi import InjectionsApi

    from rcabench_platform.v3.internal.clients.rcabench_ import get_rcabench_client

    api = InjectionsApi(get_rcabench_client())
    resp = api.list_successful_injections()
    assert resp.data is not None

    return resp.data


@app.command()
@timeit()
def run(db_only: bool = False, require_filtered: bool = False):
    with_issues_resp = get_analysis_with_issues()

    rows: list[dict[str, str | int]] = []
    for item in with_issues_resp:
        assert item.datapack_id
        assert item.datapack_name
        assert item.fault_type

        row: dict[str, str | int] = {
            "datapack_id": item.datapack_id,
            "datapack_name": item.datapack_name,
            "fault_type": item.fault_type,
        }
        rows.append(row)

    df = pl.DataFrame(rows).unique().sort(by="dataset_id", descending=True)

    save_parquet(df, path=get_dataset_meta_file("rcabench", "with_issues.db.parquet"))

    if db_only:
        return

    full_df = read_dataset_index("rcabench").select("datapack").rename({"datapack": "injection_name"})
    df = df.join(full_df, on="injection_name", how="inner")

    if require_filtered:
        filtered_df = read_dataset_index("rcabench_filtered").select("datapack").rename({"datapack": "injection_name"})
        df = df.join(filtered_df, on="injection_name", how="inner")

    datapacks = df["injection_name"].to_list()

    dataset = "rcabench_with_issues"

    dataset_folder = get_dataset_folder(dataset)
    shutil.rmtree(dataset_folder, ignore_errors=True)

    link_subset(src_dataset="rcabench", dst_dataset=dataset, datapacks=datapacks)

    query_ratio()


@app.command()
@timeit()
def query_ratio():
    with_issues = read_dataset_index("rcabench_with_issues").select("datapack")
    filtered = read_dataset_index("rcabench_filtered").select("datapack")

    joint_df = with_issues.join(filtered, on="datapack", how="inner")

    ratio = Fraction(len(joint_df), len(with_issues))
    logger.info(f"rcabench_with_issues filtered ratio: {len(joint_df)}/{len(with_issues)} {float(ratio):.2%}")


if __name__ == "__main__":
    app()
