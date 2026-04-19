#!/usr/bin/env -S uv run -s
import datetime
import functools
import json
import shutil
from pathlib import Path
from typing import Any

import dateutil.tz
import polars as pl
from tqdm.auto import tqdm

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import (
    convert_datapack,
    convert_dataset,
    link_subset,
)
from rcabench_platform.v3.internal.sources.rcabench import (
    RCABenchDatapackLoader,
    RcabenchDatasetLoader,
)
from rcabench_platform.v3.sdk.datasets.rcabench import FAULT_TYPES
from rcabench_platform.v3.sdk.datasets.spec import (
    get_datapack_folder,
    get_datapack_list,
    get_dataset_meta_file,
)
from rcabench_platform.v3.sdk.utils.dataframe import print_dataframe
from rcabench_platform.v3.sdk.utils.dict_ import flatten_dict
from rcabench_platform.v3.sdk.utils.fmap import fmap_threadpool
from rcabench_platform.v3.sdk.utils.serde import load_json, save_parquet


@app.command()
@timeit()
def run(
    skip_finished: bool = True,
    parallel: int = 4,
    scan: bool = True,
):
    src_root = Path("data") / "rcabench_dataset"

    loader = RcabenchDatasetLoader(src_root, dataset="rcabench")

    convert_dataset(
        loader,
        skip_finished=skip_finished,
        parallel=parallel,
        ignore_exceptions=True,
    )

    if scan:
        scan_datapack_attributes()


@app.command()
@timeit()
def build_template():
    """Build log templates for all datapacks using Drain3."""
    src_root = Path("data") / "rcabench_dataset"

    # Get all datapacks using the same logic as the dataset loader
    from rcabench_platform.v3.internal.sources.rcabench import (
        create_template_miner,
        extract_unique_log_messages,
        scan_datapacks,
    )

    datapacks = scan_datapacks(src_root)
    logger.info(f"Found {len(datapacks)} datapacks for template building")

    # Extract unique messages from all datapacks
    unique_messages = extract_unique_log_messages(src_root, datapacks)
    logger.info(f"Extracted {unique_messages.height} unique log messages")

    if unique_messages.height == 0:
        logger.warning("No log messages found for template processing")
        return

    # Process all messages with Drain3
    config_path = Path("data/rcabench_dataset/drain_template/drain_ts.ini")
    persistence_path = Path("data/rcabench_dataset/drain_template/drain_ts.bin")

    # Ensure template directory exists
    persistence_path.parent.mkdir(parents=True, exist_ok=True)

    # Remove existing persistence file to rebuild from scratch
    if persistence_path.exists():
        persistence_path.unlink()
        logger.info("Removed existing drain_ts.bin file")

    template_miner = create_template_miner(config_path, persistence_path)

    logger.info("Processing all unique log messages with Drain3...")
    processed_count = 0

    for message in tqdm(unique_messages["Body"].to_list(), desc="Processing messages"):
        if message:  # Skip empty messages
            template_miner.add_log_message(message)
            processed_count += 1

    logger.info(f"Processed {processed_count} messages and built {len(template_miner.drain.clusters)} templates")
    logger.info(f"Template state saved to {persistence_path}")


@app.command()
@timeit()
def test_build_template():
    """Test function to build log templates for a specific datapack."""
    src_root = Path("data") / "rcabench_dataset"
    datapack = "ts0-mysql-bandwidth-5p8bkc"

    # Extract unique messages from the specified datapack
    from rcabench_platform.v3.internal.sources.rcabench import (
        create_template_miner,
        extract_unique_log_messages,
    )

    unique_messages = extract_unique_log_messages(src_root, [datapack])
    logger.info(f"Extracted {unique_messages.height} unique log messages from {datapack}")

    if unique_messages.height == 0:
        logger.warning(f"No log messages found in datapack {datapack} for template processing")
        return

    # Process all messages with Drain3
    config_path = Path("data/rcabench_dataset/drain_template/drain_ts.ini")
    persistence_path = Path("temp/drain_ts_test.bin")

    # Ensure template directory exists
    persistence_path.parent.mkdir(parents=True, exist_ok=True)

    # Remove existing persistence file to rebuild from scratch
    if persistence_path.exists():
        persistence_path.unlink()
        logger.info("Removed existing drain_ts_test.bin file")

    template_miner = create_template_miner(config_path, persistence_path)

    logger.info(f"Processing unique log messages from {datapack} with Drain3...")
    processed_count = 0

    for message in tqdm(unique_messages["Body"].to_list(), desc="Processing messages"):
        if message:
            template_miner.add_log_message(message)
            processed_count += 1
    logger.info(f"Processed {processed_count} messages and built {len(template_miner.drain.clusters)} templates")
    logger.info(f"Template state saved to {persistence_path}")


@app.command()
@timeit()
def local_test_1():
    datapack = "ts0-mysql-bandwidth-5p8bkc"
    loader = RCABenchDatapackLoader(
        src_folder=Path("data") / "rcabench_dataset" / datapack,
        datapack=datapack,
    )
    convert_datapack(
        loader,
        dst_folder=Path("temp") / "rcabench" / datapack,
        skip_finished=False,
    )


@app.command()
@timeit()
def scan_datapack_attributes():
    dataset = "rcabench"
    datapacks = get_datapack_list(dataset)

    tasks = []
    for datapack in datapacks:
        input_folder = get_datapack_folder(dataset, datapack)
        tasks.append(functools.partial(_task_scan_datapack_attributes, dataset, datapack, input_folder))

    results = fmap_threadpool(tasks, parallel=32)

    # Explicitly specify schema to avoid type inference issues
    schema = {
        "dataset": pl.String,
        "datapack": pl.String,
        "inject_time": pl.Datetime,
        "injection.fault_type": pl.String,
        "injection.display_config": pl.String,
        "injection.duration": pl.Int64,
        "injection.injection_point.class_name": pl.String,
        "injection.rate": pl.Int64,
        "injection.mem_worker": pl.Int64,
        "injection.memory_size": pl.Int64,
        "env.normal_start": pl.Datetime,
        "env.normal_end": pl.Datetime,
        "env.abnormal_start": pl.Datetime,
        "env.abnormal_end": pl.Datetime,
        "files.total_size:MiB": pl.Float64,
        "detector.conclusion.rows": pl.Int64,
        "detector.issues.rows": pl.Int64,
    }

    df = pl.DataFrame(results, schema=schema).sort("inject_time", descending=True)

    save_parquet(df, path=get_dataset_meta_file(dataset, "attributes.parquet"))


def _convert_time(ts: int) -> datetime.datetime:
    return datetime.datetime.fromtimestamp(ts, tz=datetime.timezone.utc)


def _task_scan_datapack_attributes(dataset: str, datapack: str, input_folder: Path) -> dict[str, Any]:
    attrs: dict[str, Any] = {"dataset": dataset, "datapack": datapack}

    env = load_json(path=input_folder / "env.json")
    injection = load_json(path=input_folder / "injection.json")

    # tz = dateutil.tz.gettz(env["TIMEZONE"])
    normal_start = _convert_time(int(env["NORMAL_START"]))
    normal_end = _convert_time(int(env["NORMAL_END"]))
    abnormal_start = _convert_time(int(env["ABNORMAL_START"]))
    abnormal_end = _convert_time(int(env["ABNORMAL_END"]))

    attrs["inject_time"] = abnormal_start

    display_config = flatten_dict(json.loads(injection["display_config"]))
    attrs["injection.fault_type"] = FAULT_TYPES[injection["fault_type"]]
    attrs["injection.display_config"] = injection["display_config"]
    attrs["injection.duration"] = display_config["duration"]

    # Ensure data type consistency, avoid type conflicts when creating Polars DataFrame
    configs = [
        ("injection_point.class_name", str, None),
        ("rate", int, None),
        ("mem_worker", int, None),
        ("memory_size", int, None),
    ]
    for config_name, expected_type, default_value in configs:
        value = display_config.get(config_name)
        if value is None:
            attrs[f"injection.{config_name}"] = default_value
        else:
            try:
                # Try to convert to expected type
                attrs[f"injection.{config_name}"] = expected_type(value) if expected_type is not str else str(value)
            except (ValueError, TypeError):
                # If conversion fails, use default value
                attrs[f"injection.{config_name}"] = default_value

    attrs["env.normal_start"] = normal_start
    attrs["env.normal_end"] = normal_end
    attrs["env.abnormal_start"] = abnormal_start
    attrs["env.abnormal_end"] = abnormal_end

    total_size = 0
    for file in input_folder.iterdir():
        if not file.is_file():
            continue
        total_size += file.stat().st_size
    attrs["files.total_size:MiB"] = round(total_size / (1024 * 1024), 6)

    conclusion_path = input_folder / "conclusion.parquet"
    if conclusion_path.exists():
        conclusion_df = pl.read_parquet(conclusion_path)
        attrs["detector.conclusion.rows"] = len(conclusion_df)
        attrs["detector.issues.rows"] = len(conclusion_df.filter(pl.col("Issues") != "{}"))

    return attrs


@app.command()
@timeit()
def merge_conclusion():
    dataset = "rcabench"
    datapacks = get_datapack_list(dataset)

    df_list = []
    for datapack in tqdm(datapacks):
        datapack_folder = get_datapack_folder(dataset, datapack)
        if not datapack_folder.is_dir():
            continue

        conclusion_path = datapack_folder / "conclusion.parquet"
        if not conclusion_path.exists():
            continue

        df = pl.read_parquet(conclusion_path)

        if df.is_empty():
            continue

        df = df.select(pl.lit(datapack).alias("datapack"), pl.all())
        df_list.append(df)

    df = pl.concat(df_list)
    save_parquet(df, path=get_dataset_meta_file(dataset, "conclusion.parquet"))


@app.command()
@timeit()
def query_fault_types(dataset: str):
    if dataset in ("rcabench", "rcabench_filtered"):
        lf = pl.scan_parquet(get_dataset_meta_file(dataset, "attributes.parquet"))
        col = "injection.fault_type"
        df = lf.select(col).collect()
    elif dataset == "rcabench_with_issues":
        lf = pl.scan_parquet(get_dataset_meta_file("rcabench", "with_issues.db.parquet"))
        col = "fault_type"
        df = lf.select(col).collect()
    else:
        raise NotImplementedError

    fault_types_count = df[col].value_counts().sort("count", descending=True)
    save_parquet(
        fault_types_count,
        path=get_dataset_meta_file(dataset, "fault_types.count.parquet"),
    )

    print_dataframe(fault_types_count)


@app.command()
@timeit()
def reset_after_time(timestamp: str):
    dt = datetime.datetime.fromisoformat(timestamp).replace(tzinfo=datetime.timezone.utc)
    logger.info(f"Resetting datapacks after {dt}")

    to_reset = []

    dataset = "rcabench"
    for datapack in tqdm(get_datapack_list(dataset)):
        src_folder = Path("data") / "rcabench_dataset" / datapack
        mtime = src_folder.stat().st_mtime
        mtime_dt = datetime.datetime.fromtimestamp(mtime, tz=datetime.timezone.utc)

        if mtime_dt >= dt:
            to_reset.append((datapack, mtime_dt))

    to_reset.sort(key=lambda x: x[1])

    logger.info(f"Total datapacks to reset: {len(to_reset)}")

    for datapack, _ in tqdm(to_reset):
        datapack_folder = get_datapack_folder(dataset, datapack)
        finished = datapack_folder / ".finished"
        assert finished.exists()
        finished.unlink()


@app.command()
@timeit()
def dedup_converted():
    datapacks = get_datapack_list("rcabench")

    for datapack in tqdm(datapacks):
        src_folder = get_datapack_folder("rcabench", datapack)
        dst_folder = Path("data") / "rcabench_dataset" / datapack / "converted"

        if not dst_folder.exists():
            continue

        for item in dst_folder.iterdir():
            assert item.is_file()
            item.unlink()

        for item in src_folder.iterdir():
            assert item.is_file()

            dst = dst_folder / item.name
            dst.hardlink_to(item)


if __name__ == "__main__":
    app()
