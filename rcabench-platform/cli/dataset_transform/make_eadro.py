#!/usr/bin/env -S uv run -s
import os
import re
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any

import numpy as np
import pandas as pd
import polars as pl
import typer

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import (
    DatapackLoader,
    DatasetLoader,
    Label,
    convert_dataset,
)
from rcabench_platform.v3.sdk.utils.serde import load_json


def trace_process(filepath: Path) -> pd.DataFrame:
    spans_json = load_json(path=filepath)
    all_span_list = []
    for trace in spans_json:
        process_dict = trace["processes"]
        service_name_dict = {key: process_dict[key]["serviceName"] for key in process_dict}

        for span in trace["spans"]:
            span_data = {
                "trace_id": str(span["traceID"]),
                "span_id": str(span["spanID"]),
                "time": span["startTime"],
                "duration": np.uint64(span["duration"]),
                "span_name": str(span["operationName"]),
                "parent_span_id": str(span["references"][0]["spanID"]) if span["references"] else "",
                "service_name": str(service_name_dict[span["processID"]]),
            }
            all_span_list.append(span_data)

    df = pd.DataFrame(all_span_list)
    df["time"] = pd.to_datetime(df["time"], unit="us", utc=True)
    df["time"] = df["time"] - pd.Timedelta(hours=8)

    for col in df.columns:
        if col not in ["time", "duration"]:
            df[col] = df[col].astype("string")

    df = df.sort_values(by="time", ascending=True)

    return df


def log_process(filepath: Path) -> pd.DataFrame:
    log_json = load_json(path=filepath)
    log_entries = []

    time_pattern = r"\[(.*?)\]"
    level_pattern = r"<(.*?)>"
    message_pattern = r">:\s*(.*)"

    for service_name, logs in log_json.items():
        for log_line in logs:
            try:
                time_match = re.search(time_pattern, log_line)
                if not time_match:
                    raise ValueError("Time pattern not found")
                time_str = time_match.group(1)
                dt = datetime.strptime(time_str, "%Y-%b-%d %H:%M:%S.%f")
                time_utc = pd.to_datetime(dt, utc=True)

                level_match = re.search(level_pattern, log_line)
                if not level_match:
                    raise ValueError("Level pattern not found")
                level = level_match.group(1)
                message_match = re.search(message_pattern, log_line)
                if not message_match:
                    raise ValueError("Message pattern not found")
                message = message_match.group(1)

                log_entries.append(
                    {
                        "time": time_utc,
                        "service_name": service_name,
                        "level": level,
                        "message": message,
                        "trace_id": "",
                        "span_id": "",
                    }
                )
            except Exception as e:
                logger.warning("Warning: Failed to parse line: {}\nError: {}", log_line, e)

    df = pd.DataFrame(log_entries)
    df["time"] = df["time"] - pd.Timedelta(hours=8)

    for col in df.columns:
        if col not in ["time"]:
            df[col] = df[col].astype("string")

    return df


def log_process_TT(filepath: Path) -> pd.DataFrame:
    log_json = load_json(path=filepath)
    log_entries = []

    time_pattern = r"^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}.\d+)"
    message_pattern = r"---\s*(.*)$"

    for service_name, logs in log_json.items():
        for log_line in logs:
            try:
                time_match = re.search(time_pattern, log_line)
                if not time_match:
                    raise ValueError("Time pattern not found")
                time_str = time_match.group(1)
                dt = datetime.strptime(time_str, "%Y-%m-%d %H:%M:%S.%f")
                time_utc = pd.to_datetime(dt, utc=True)  # 强制转成 pandas UTC 类型
                temp = log_line.split(time_str)[-1]
                level = temp[2:].split(" ")[0]
                message_match = re.search(message_pattern, log_line)
                if not message_match:
                    raise ValueError("Message pattern not found")
                message = message_match.group(1)

                log_entries.append(
                    {
                        "time": time_utc,
                        "service_name": service_name,
                        "level": level,
                        "message": message,
                        "trace_id": "",
                        "span_id": "",
                    }
                )
            except Exception as e:
                logger.warning("Warning: Failed to parse line: {}\nError: {}", log_line, e)

    df = pd.DataFrame(log_entries)

    df["time"] = df["time"] - pd.Timedelta(hours=8)

    for col in df.columns:
        if col not in ["time"]:
            df[col] = df[col].astype("string")

    return df


def metrics_process(filepath: Path) -> pd.DataFrame:
    df = pd.read_csv(filepath)
    df["timestamp"] = pd.to_datetime(df["timestamp"], unit="s", utc=True)
    df.rename(columns={"timestamp": "time"}, inplace=True)

    return df


def load_fault_list(file_path: Path) -> list[dict[str, Any]]:
    fault_list = load_json(path=file_path)
    fault_list["start"] = pd.to_datetime(fault_list["start"], unit="s", utc=True)
    fault_list["end"] = pd.to_datetime(fault_list["end"], unit="s", utc=True)

    converted_faults = []
    if len(fault_list["faults"]) == 0:
        start_time = fault_list["start"]
        end_time = fault_list["end"]
        interval = pd.Timedelta(seconds=120) if "SN" in file_path.name else pd.Timedelta(seconds=600)
        current_time = start_time
        while current_time + interval <= end_time:
            normal_case = {
                "injection_name": "normal",
                "fault_type": "normal",
                "fault_start_time": "",
                "fault_end_time": "",
                "normal_start_time": current_time.isoformat(),
                "normal_end_time": (current_time + interval).isoformat(),
            }
            converted_faults.append(normal_case)
            current_time += interval
    else:
        for fault in fault_list["faults"]:
            start_time = pd.to_datetime(fault["start"], unit="s", utc=True)
            end_time = start_time + pd.Timedelta(seconds=fault["duration"])

            fault_service = ""
            if "SN.fault" in file_path.name:
                fault_service = "-".join(fault["name"].split("-")[1:-1])
            elif "TT.fault" in file_path.name:
                fault_service = fault["name"].split("_")[1]

            converted_fault = {
                "injection_name": fault_service,
                "fault_type": fault["fault"],
                "fault_start_time": start_time.isoformat(),
                "fault_end_time": end_time.isoformat(),
                "normal_start_time": "",
                "normal_end_time": "",
            }
            converted_faults.append(converted_fault)

    return converted_faults


class EadroDatapackLoader(DatapackLoader):
    def __init__(self, source_dir: Path, datapack_info: dict[str, Any]):
        self.source_dir = source_dir
        self.datapack_info = datapack_info
        self.fault_info = datapack_info["fault_info"]
        self.start_time = pd.to_datetime(datapack_info["start_time"])
        self.end_time = pd.to_datetime(datapack_info["end_time"])
        self.dataset_name = datapack_info["dataset_name"]
        self.folder_path = datapack_info["folder_path"]

    @property
    def name(self) -> str:
        prefix = "SN" if self.dataset_name.startswith("SN") else "TT"
        return f"{prefix}-case-{self.start_time.strftime('%Y-%m-%dT%H-%M-%S')}"

    def labels(self) -> list[Label]:
        labels = []
        event = self.fault_info
        labels.append(Label(level="service", name=event.get("injection_name", "unknown")))
        return labels

    def data(self) -> dict[str, Any]:
        data = {}
        metrics_folder = os.path.join(self.folder_path, "metrics")
        logs_file = os.path.join(self.folder_path, "logs.json")
        spans_file = os.path.join(self.folder_path, "spans.json")

        if os.path.exists(metrics_folder):
            metric_dfs = []
            for metric_file in os.listdir(metrics_folder):
                metric_path = os.path.join(metrics_folder, metric_file)
                try:
                    df = metrics_process(Path(metric_path))
                    if "time" in df.columns:
                        filtered_df = df[(df["time"] >= self.start_time) & (df["time"] < self.end_time)]
                        service_name = metric_file.split(".")[0]

                        pl_df = pl.from_pandas(filtered_df)
                        if isinstance(pl_df, pl.Series):
                            pl_df = pl_df.to_frame()

                        pl_df = pl_df.with_columns(pl.lit(f"{service_name}").alias("service_name"))

                        exclude_columns = ["time", "service_name"]
                        metric_columns = [col for col in pl_df.columns if col not in exclude_columns]

                        df_long = pl_df.unpivot(
                            index=["time", "service_name"],
                            on=metric_columns,
                            variable_name="metric",
                            value_name="value",
                        )

                        df_long = df_long.with_columns(
                            pl.col("time").cast(pl.Datetime(time_unit="us", time_zone="UTC")),
                            pl.col("value").cast(pl.Float64, strict=False),
                            pl.col("metric").cast(pl.String),
                            pl.col("service_name").cast(pl.String),
                        )

                        metric_dfs.append(df_long)
                except Exception as e:
                    logger.warning("Failed to load metric data {} \nError: {}", metric_file, e)

            if metric_dfs:
                try:
                    data["metric.parquet"] = pl.concat(metric_dfs)
                except Exception as e:
                    logger.warning("Failed to concat metric data \nError: {}", e)

        if os.path.exists(spans_file):
            try:
                df = trace_process(Path(spans_file))
                if "time" in df.columns:
                    filtered_df = df[(df["time"] >= self.start_time) & (df["time"] < self.end_time)]

                    pl_df = pl.from_pandas(filtered_df)
                    if isinstance(pl_df, pl.Series):
                        pl_df = pl_df.to_frame()

                    if "time" in pl_df.columns:
                        pl_df = pl_df.with_columns(pl.col("time").cast(pl.Datetime(time_unit="us", time_zone="UTC")))

                    if "duration" in pl_df.columns:
                        pl_df = pl_df.with_columns(
                            pl.when(pl.col("duration") < 0)
                            .then(0)
                            .otherwise(pl.col("duration").cast(pl.Float64).round())
                            .cast(pl.UInt64)
                            .alias("duration")
                        )

                    required_columns = [
                        "time",
                        "trace_id",
                        "span_id",
                        "parent_span_id",
                        "service_name",
                        "span_name",
                        "duration",
                    ]
                    for col in pl_df.columns:
                        if col not in required_columns:
                            pl_df = pl_df.with_columns(pl.col(col).alias(f"attr.{col}"))

                    selected_columns = required_columns
                    for col in pl_df.columns:
                        if col not in required_columns and col.startswith("attr."):
                            selected_columns.append(col)

                    pl_df = pl_df.select(selected_columns)
                    data["trace.parquet"] = pl_df
            except Exception as e:
                logger.warning("Failed to load trace data {} \nError: {}", spans_file, e)

        if os.path.exists(logs_file):
            try:
                if "TT" in self.dataset_name:
                    df = log_process_TT(Path(logs_file))
                else:
                    df = log_process(Path(logs_file))

                if "time" in df.columns:
                    filtered_df = df[(df["time"] >= self.start_time) & (df["time"] < self.end_time)]
                    pl_df = pl.from_pandas(filtered_df)

                    if isinstance(pl_df, pl.Series):
                        pl_df = pl_df.to_frame()

                    if "time" in pl_df.columns:
                        pl_df = pl_df.with_columns(pl.col("time").cast(pl.Datetime(time_unit="us", time_zone="UTC")))

                    data["log.parquet"] = pl_df
            except Exception as e:
                logger.warning("Failed to load log data {} \nError: {}", logs_file, e)

        data["fault_info.json"] = self.fault_info
        return data


class EadroDatasetLoader(DatasetLoader):
    def __init__(self, source_dir: str, dataset_name: str):
        self.source_dir = Path(source_dir)
        self.dataset_name = dataset_name
        self.datapack_infos = self._discover_datapacks()

    def _discover_datapacks(self) -> list[dict[str, Any]]:
        dataset_path_abnormal = os.path.join(self.source_dir, self.dataset_name, self.dataset_name, "data")
        dataset_path_normal = os.path.join(self.source_dir, self.dataset_name, self.dataset_name, "no_fault")

        datapack_infos = []
        for dataset_path in [dataset_path_abnormal, dataset_path_normal]:
            for item in os.listdir(dataset_path):
                item_path = os.path.join(dataset_path, item)
                if os.path.isdir(item_path) and item.startswith(("SN.", "TT.")):
                    fault_file = f"{'SN' if item.startswith('SN') else 'TT'}.fault-{item[3:]}.json"
                    fault_file_path = os.path.join(dataset_path, fault_file)

                    if os.path.exists(fault_file_path):
                        fault_list = load_fault_list(Path(fault_file_path))
                        for i, fault in enumerate(fault_list):
                            normal_start = fault.get("normal_start_time")
                            normal_end = fault.get("normal_end_time")
                            fault_start = fault.get("fault_start_time")
                            fault_end = fault.get("fault_end_time")
                            datapack_info = {}
                            if fault_start and fault_end:
                                start_time = pd.to_datetime(fault_start)
                                if not start_time:
                                    continue
                                end_time = pd.to_datetime(fault_end)
                                if not end_time:
                                    continue

                                datapack_info = {
                                    "folder_path": item_path,
                                    "fault_info": fault,
                                    "start_time": start_time,
                                    "end_time": end_time,
                                    "dataset_name": self.dataset_name,
                                    "fault_file": fault_file_path,
                                }

                            elif normal_start and normal_end:
                                start_time = pd.to_datetime(normal_start)
                                if not start_time:
                                    continue
                                end_time = pd.to_datetime(normal_end)
                                if not end_time:
                                    continue

                                datapack_info = {
                                    "folder_path": item_path,
                                    "fault_info": fault,
                                    "start_time": start_time,
                                    "end_time": end_time,
                                    "dataset_name": self.dataset_name,
                                    "fault_file": fault_file_path,
                                }
                            datapack_infos.append(datapack_info)
        return datapack_infos

    def name(self) -> str:
        return "eadro_sn" if self.dataset_name.startswith("SN") else "eadro_tt"

    def __len__(self) -> int:
        return len(self.datapack_infos)

    def __getitem__(self, index: int) -> EadroDatapackLoader:
        if index < 0 or index >= len(self):
            raise IndexError("Index out of range")
        return EadroDatapackLoader(self.source_dir, self.datapack_infos[index])


@app.command(help="Convert SN (SocialNetwork) dataset from Eadro format to RCABench format")
@timeit()
def sn():
    loader = EadroDatasetLoader(source_dir=r"data/eadro", dataset_name="SN_Dataset")
    convert_dataset(loader, skip_finished=False, parallel=4)


@app.command(help="Convert TT (TrainTicket) dataset from Eadro format to RCABench format")
@timeit()
def tt():
    loader = EadroDatasetLoader(source_dir=r"data/eadro", dataset_name="TT_Dataset")
    convert_dataset(loader, skip_finished=False, parallel=4)


@app.command(help="Create a symbolic link from source path to target path for Eadro dataset access")
@timeit()
def create_link(
    source_path: str = typer.Option(
        "/mnt/jfs/Eadro/",
        "--source-path",
        "-s",
        help="Source path where the Eadro dataset is located (default: /mnt/jfs/Eadro/)",
    ),
    target_path: str = typer.Option(
        "data/eadro",
        "--target-path",
        "-t",
        help="Target path where the symbolic link will be created (default: data)",
    ),
):
    """We suposse the file layout as:
    $> tree -L 3
    .
    ├── SN_Dataset
    │   └── SN_Dataset
    │       ├── data
    │       └── no_fault
    └── TT_Dataset
        └── TT_Dataset
            ├── data
            ├── no_fault
            └── TT-all.txz
    """
    try:
        if not os.path.exists(source_path):
            logger.error(f"Source path does not exist: {source_path}")
            return

        if os.path.exists(target_path) or os.path.islink(target_path):
            if os.path.islink(target_path):
                os.unlink(target_path)
                logger.info(f"Removed existing symbolic link: {target_path}")
            else:
                logger.error(
                    f"Target path {target_path} already exists and is not a symbolic link. Please handle manually."
                )
                return

        os.symlink(source_path, target_path)
        logger.info(f"Successfully created symbolic link: {target_path} -> {source_path}")

    except OSError as e:
        logger.error(f"Failed to create symbolic link: {e}")


if __name__ == "__main__":
    app()
