#!/usr/bin/env -S uv run -s

import json
import os
import re
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Optional

import polars as pl
import typer

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import (
    DatapackLoader,
    DatasetLoader,
    Label,
    convert_dataset,
)


def to_utc_time(time_str: str) -> datetime:
    assert time_str is not None, "time_str is None"
    assert isinstance(time_str, str), f"time_str is not a string: {type(time_str)}"
    assert len(time_str) > 0, "time_str is empty"

    dt = datetime.strptime(time_str, "%Y-%m-%d %H:%M:%S")
    assert dt is not None, f"Failed to parse datetime from: {time_str}"
    return dt.replace(tzinfo=timezone.utc)


def parse_time_utc(time_str: str) -> datetime | None:
    assert time_str is not None, "time_str is None"
    assert isinstance(time_str, str), f"time_str is not a string: {type(time_str)}"
    assert len(time_str) > 0, "time_str is empty"

    pattern = re.compile(r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}")
    match = pattern.search(time_str)
    if match:
        return to_utc_time(match.group())
    else:
        logger.warning("Time string '{}' does not match expected pattern", time_str)
        return None


class NezhaDatapackLoader(DatapackLoader):
    def __init__(
        self,
        case_data: dict[str, Any],
        source_dir: Path,
        inject_time: datetime,
        end_time: datetime,
    ):
        assert case_data is not None, "case_data is None"
        assert isinstance(case_data, dict), f"case_data is not a dict: {type(case_data)}"
        assert source_dir is not None, "source_dir is None"
        assert isinstance(source_dir, Path), f"source_dir is not a Path: {type(source_dir)}"
        assert source_dir.exists(), f"source_dir does not exist: {source_dir}"
        assert inject_time is not None, "inject_time is None"
        assert isinstance(inject_time, datetime), f"inject_time is not a datetime: {type(inject_time)}"
        assert end_time is not None, "end_time is None"
        assert isinstance(end_time, datetime), f"end_time is not a datetime: {type(end_time)}"
        assert end_time > inject_time, f"end_time {end_time} is not after inject_time {inject_time}"

        # Validate required fields in case_data
        assert "fault_type" in case_data, "case_data missing 'fault_type' field"
        assert case_data["fault_type"], "fault_type is empty"

        self.case_data = case_data
        self.source_dir = source_dir
        self.inject_time = inject_time
        self.end_time = end_time
        self.case_id = self.case_id = (
            f"{self.inject_time.strftime('%Y-%m-%d_%H-%M')}_{self.case_data['fault_type']}"
            if self.case_data["fault_type"] != "normal"
            else f"{self.inject_time.strftime('%Y-%m-%d_%H-%M')}_normal_case"
        )

    @property
    def name(self) -> str:
        return self.case_id

    def labels(self) -> list[Label]:
        return [
            Label(level="service", name=self.case_data.get("injection_name", "unknown")),
        ]

    def data(self) -> dict[str, Any]:
        data = {}

        log_dir = self.source_dir / "log"
        if log_dir.exists():
            log_dfs = self._process_logs(log_dir)
            if log_dfs:
                concatenated_logs = pl.concat(log_dfs)
                assert concatenated_logs is not None, "Log concatenation resulted in None"
                assert len(concatenated_logs) > 0, "Log concatenation resulted in empty DataFrame"
                data["log.parquet"] = concatenated_logs

        metric_dir = self.source_dir / "metric"
        if metric_dir.exists():
            metric_dfs = self._process_metrics(metric_dir)
            assert metric_dfs is not None, "Metric DataFrames are None"
            concatenated_metrics = pl.concat(metric_dfs)
            assert concatenated_metrics is not None, "Metric concatenation resulted in None"
            assert len(concatenated_metrics) > 0, "Metric concatenation resulted in empty DataFrame"
            data["metric.parquet"] = concatenated_metrics

        trace_dir = self.source_dir / "trace"
        if trace_dir.exists():
            trace_dfs = self._process_traces(trace_dir)
            if trace_dfs:
                concatenated_traces = pl.concat(trace_dfs)
                assert concatenated_traces is not None, "Trace concatenation resulted in None"
                assert len(concatenated_traces) > 0, "Trace concatenation resulted in empty DataFrame"
                data["trace.parquet"] = concatenated_traces

        assert self.case_data is not None, "Case data is None"
        data["fault_info.json"] = self.case_data
        return data

    def _process_metrics(self, metric_dir: Path) -> list[pl.DataFrame]:
        metric_dfs = []
        metric_files = list(metric_dir.glob("*metric*.csv"))

        for metric_file in metric_files:
            schema = {
                "Time": pl.String,
                "TimeStamp": pl.String,
                "PodName": pl.String,
                "CpuUsage(m)": pl.Float64,
                "CpuUsageRate(%)": pl.Float64,
                "MemoryUsage(Mi)": pl.Float64,
                "MemoryUsageRate(%)": pl.Float64,
                "SyscallRead": pl.Float64,
                "SyscallWrite": pl.Float64,
                "NetworkReceiveBytes": pl.Float64,
                "NetworkTransmitBytes": pl.Float64,
                "PodClientLatencyP90(s)": pl.Float64,
                "PodServerLatencyP90(s)": pl.Float64,
                "PodClientLatencyP95(s)": pl.Float64,
                "PodServerLatencyP95(s)": pl.Float64,
                "PodClientLatencyP99(s)": pl.Float64,
                "PodServerLatencyP99(s)": pl.Float64,
                "PodWorkload(Ops)": pl.Float64,
                "PodSuccessRate(%)": pl.Float64,
                "NodeCpuUsageRate(%)": pl.Float64,
                "NodeMemoryUsageRate(%)": pl.Float64,
                "NodeNetworkReceiveBytes": pl.Float64,
            }

            df = pl.read_csv(metric_file, schema=schema)

            # drop Time column
            df = df.drop("Time")

            metric_columns = [col for col in df.columns if col not in ["Time", "TimeStamp", "PodName"]]

            df_long = (
                df.unpivot(
                    index=["TimeStamp", "PodName"],
                    on=metric_columns,
                    variable_name="metric",
                    value_name="value",
                )
                .rename({"PodName": "service_name"})
                .with_columns(
                    pl.col("TimeStamp")
                    .cast(pl.Int64)
                    .mul(1_000_000_000)  # Convert seconds to nanoseconds
                    .cast(pl.Datetime(time_unit="ns", time_zone="UTC"))
                    .alias("time")
                )
                .drop("TimeStamp")
                .filter(
                    pl.col("time").is_not_null()
                    & (pl.col("time") >= self.inject_time)
                    & (pl.col("time") <= self.end_time)
                )
            )

            if len(df_long) > 0:
                metric_dfs.append(df_long)
        assert len(metric_dfs) > 0
        return metric_dfs

    def _process_traces(self, trace_dir: Path) -> list[pl.DataFrame]:
        assert trace_dir.exists(), f"Trace directory does not exist: {trace_dir}"
        trace_dfs = []
        trace_files = list(trace_dir.glob("*.csv"))
        assert len(trace_files) > 0, f"No CSV files found in trace directory: {trace_dir}"

        for trace_file in trace_files:
            parts = trace_file.name.split("_")
            if len(parts) < 2:
                logger.warning("Skipping file with invalid name format: {}", trace_file.name)
                continue

            hour = int(parts[0])
            minute = int(parts[1])
            file_time = self.inject_time.replace(hour=hour, minute=minute, second=0, microsecond=0)

            if not (self.inject_time <= file_time <= self.end_time):
                continue

            schema = {
                "TraceID": pl.Utf8,
                "SpanID": pl.Utf8,
                "ParentID": pl.Utf8,
                "PodName": pl.Utf8,
                "OperationName": pl.Utf8,
                "StartTimeUnixNano": pl.UInt64,
                "EndTimeUnixNano": pl.UInt64,
                "Duration": pl.UInt64,
            }

            df = pl.read_csv(trace_file, schema=schema)

            df_processed = (
                df.rename(
                    {
                        "TraceID": "trace_id",
                        "SpanID": "span_id",
                        "ParentID": "parent_span_id",
                        "OperationName": "span_name",
                        "Duration": "duration",
                        "PodName": "service_name",
                    }
                )
                .with_columns(
                    pl.col("StartTimeUnixNano").cast(pl.Datetime(time_unit="ns", time_zone="UTC")).alias("time")
                )
                .drop(["StartTimeUnixNano", "EndTimeUnixNano"])
                .filter(
                    pl.col("time").is_not_null()
                    & (pl.col("time") >= self.inject_time)
                    & (pl.col("time") <= self.end_time)
                )
                .with_columns(
                    pl.when(pl.col("parent_span_id") == "root")
                    .then(pl.lit(""))
                    .otherwise(pl.col("parent_span_id"))
                    .alias("parent_span_id")
                )
            )

            base_columns = [
                "time",
                "trace_id",
                "span_id",
                "parent_span_id",
                "span_name",
                "service_name",
                "duration",
            ]
            attr_columns = [col for col in df_processed.columns if col.startswith("attr.")]
            final_columns = base_columns + attr_columns

            df_processed = df_processed.select(final_columns)
            df_processed.with_columns()

            if len(df_processed) > 0:
                trace_dfs.append(df_processed)

        assert len(trace_dfs) > 0, f"No valid trace data processed from directory: {trace_dir}"
        return trace_dfs

    def _process_logs(self, log_dir: Path) -> list[pl.DataFrame]:
        assert log_dir.exists(), f"Log directory does not exist: {log_dir}"
        log_dfs = []
        log_files = list(log_dir.glob("*.csv"))
        assert len(log_files) > 0, f"No CSV files found in log directory: {log_dir}"

        for log_file in log_files:
            assert log_file.exists(), f"Log file does not exist: {log_file}"
            parts = log_file.name.split("_")
            if len(parts) < 2:
                logger.warning("Skipping file with invalid name format: {}", log_file.name)
                continue

            hour = int(parts[0])
            minute = int(parts[1])

            assert 0 <= hour <= 23, f"Invalid hour value {hour} in filename: {log_file.name}"
            assert 0 <= minute <= 59, f"Invalid minute value {minute} in filename: {log_file.name}"
            assert self.inject_time is not None, "Injection time is None"

            file_time = self.inject_time.replace(hour=hour, minute=minute, second=0, microsecond=0)

            if not (self.inject_time <= file_time <= self.end_time):
                continue

            schema = {
                "Timestamp": pl.Utf8,
                "TimeUnixNano": pl.Int64,
                "Node": pl.Utf8,
                "PodName": pl.Utf8,
                "Container": pl.Utf8,
                "TraceID": pl.Utf8,
                "SpanID": pl.Utf8,
                "Log": pl.Utf8,
            }

            columns = [
                "Timestamp",
                "TimeUnixNano",
                "Node",
                "PodName",
                "Container",
                "TraceID",
                "SpanID",
                "Log",
            ]

            try:
                df = pl.read_csv(log_file, schema=schema, columns=columns, truncate_ragged_lines=True)
            except Exception as e:
                print("processing log file:", log_file.name, e)
                raise
            assert df is not None, f"Failed to read CSV file: {log_file}"
            assert len(df) > 0, f"Empty CSV file: {log_file}"

            df_processed = df.rename(
                {
                    "TraceID": "trace_id",
                    "SpanID": "span_id",
                    "PodName": "service_name",
                }
            ).drop(["Timestamp"])

            df_processed = self._fix_abnormal_timestamps(df_processed, "TimeUnixNano")

            df_processed = df_processed.with_columns(
                pl.col("TimeUnixNano").cast(pl.Datetime(time_unit="ns", time_zone="UTC")).alias("time")
            ).drop("TimeUnixNano")

            df_processed = df_processed.filter(
                pl.col("time").is_not_null() & (pl.col("time") >= self.inject_time) & (pl.col("time") <= self.end_time)
            )

            df_processed = df_processed.with_columns(
                pl.col("Log")
                .map_elements(lambda x: self._extract_log_message(x), return_dtype=pl.String)
                .alias("message")
            )

            base_columns = [
                "time",
                "trace_id",
                "span_id",
                "service_name",
                "level",
                "message",
            ]
            attr_columns = []

            for col in df_processed.columns:
                if col not in base_columns:
                    attr_columns.append(col)
                    df_processed = df_processed.rename({col: f"attr.{col}"})

            final_columns = base_columns + [f"attr.{col}" for col in attr_columns]
            existing_columns = [col for col in final_columns if col in df_processed.columns]

            if len(existing_columns) > 0:
                df_processed = df_processed.select(existing_columns)

                if len(df_processed) > 0:
                    log_dfs.append(df_processed)

        assert len(log_dfs) > 0, f"No valid log data processed from directory: {log_dir}"
        return log_dfs

    def _fix_abnormal_timestamps(self, df: pl.DataFrame, time_col: str) -> pl.DataFrame:
        assert df is not None, "DataFrame is None"
        assert time_col in df.columns, f"Column '{time_col}' not found in DataFrame"

        # Define abnormal timestamp threshold (FILETIME minimum and other clearly invalid values)
        # FILETIME minimum: -6795364578871345152 (nanoseconds)
        # We'll consider any timestamp before year 1900 (approximately -2208988800000000000 ns) as abnormal
        abnormal_threshold = -2208988800000000000  # 1900-01-01 in nanoseconds since epoch

        # Count abnormal values for logging
        abnormal_count = df.filter(pl.col(time_col) < abnormal_threshold).shape[0]
        if abnormal_count > 0:
            logger.warning(
                "Found {} abnormal timestamp values in log data, will interpolate them",
                abnormal_count,
            )

            # Simple approach: replace abnormal values with inject_time + row_index * 1ms
            # This provides sequential timestamps that maintain order
            baseline_ns = int(self.inject_time.timestamp() * 1_000_000_000)

            df_fixed = (
                df.with_row_index("row_idx")
                .with_columns(
                    pl.when(pl.col(time_col) < abnormal_threshold)
                    .then(baseline_ns + pl.col("row_idx") * 1_000_000)  # 1ms intervals
                    .otherwise(pl.col(time_col))
                    .alias(time_col)
                )
                .drop("row_idx")
            )

            return df_fixed

        return df

    def _extract_log_message(self, log_str: str) -> str:
        try:
            log_data = json.loads(log_str)
            return str(log_data["log"])
        except Exception as e:
            logger.error("Failed to parse log string '{}': {}", log_str, e)
            return log_str


class NezhaDatasetLoader(DatasetLoader):
    def __init__(self, source_dir: str, date: str):
        assert source_dir is not None, "source_dir is None"
        assert isinstance(source_dir, str), f"source_dir is not a string: {type(source_dir)}"
        assert len(source_dir) > 0, "source_dir is empty"
        assert date is not None, "date is None"
        assert isinstance(date, str), f"date is not a string: {type(date)}"
        assert len(date) > 0, "date is empty"

        self.source_dir = Path(source_dir) / "rca_data" / date
        assert self.source_dir.exists(), f"Source directory does not exist: {self.source_dir}"

        self.date = date
        self.fault_list = self._load_fault_list()

    def _load_fault_list(self) -> list[dict[str, Any]]:
        fault_list_path = self.source_dir / f"{self.date}-fault_list.json"
        assert fault_list_path.exists(), f"Fault list file not found: {fault_list_path}"

        with open(fault_list_path, encoding="utf-8") as f:
            fault_list = json.load(f)

        assert fault_list is not None, f"Failed to load fault list from: {fault_list_path}"
        assert isinstance(fault_list, dict), f"Fault list is not a dict: {type(fault_list)}"
        assert len(fault_list) > 0, f"Empty fault list in file: {fault_list_path}"

        flattened_list = [fault for hour in fault_list.values() for fault in hour]
        assert len(flattened_list) > 0, f"No faults found in fault list: {fault_list_path}"

        # Validate each fault entry
        for i, fault in enumerate(flattened_list):
            assert isinstance(fault, dict), f"Fault {i} is not a dict: {type(fault)}"
            assert "inject_time" in fault, f"Fault {i} missing 'inject_time' field"
            assert "inject_type" in fault, f"Fault {i} missing 'inject_type' field"
            assert "inject_pod" in fault, f"Fault {i} missing 'inject_pod' field"

        return flattened_list

    def name(self) -> str:
        return "nezha"

    def __len__(self) -> int:
        return len(self.fault_list)

    def __getitem__(self, index: int) -> NezhaDatapackLoader:
        assert 0 <= index < len(self.fault_list), (
            f"Index {index} out of range for fault_list of length {len(self.fault_list)}"
        )

        case_data = self.fault_list[index]
        assert case_data is not None, f"Case data at index {index} is None"
        assert isinstance(case_data, dict), f"Case data at index {index} is not a dict: {type(case_data)}"

        # Validate required fields
        assert "inject_time" in case_data, f"Case {index} missing 'inject_time' field"
        assert "inject_type" in case_data, f"Case {index} missing 'inject_type' field"
        assert "inject_pod" in case_data, f"Case {index} missing 'inject_pod' field"

        inject_time = parse_time_utc(case_data["inject_time"])
        assert inject_time is not None, f"Failed to parse inject_time '{case_data['inject_time']}' for case {index}"

        # Calculate end time
        if index < len(self.fault_list) - 1:
            next_case = self.fault_list[index + 1]
            assert next_case is not None, f"Next case at index {index + 1} is None"
            assert "inject_time" in next_case, f"Next case {index + 1} missing 'inject_time' field"

            next_inject_time = parse_time_utc(next_case["inject_time"])
            assert next_inject_time is not None, (
                f"Failed to parse next inject_time '{next_case['inject_time']}' for case {index + 1}"
            )
            assert next_inject_time > inject_time, (
                f"Next inject time {next_inject_time} is not after current inject time {inject_time}"
            )

            end_time = next_inject_time - timedelta(minutes=1)
        else:
            end_time = inject_time + timedelta(minutes=10)

        assert end_time > inject_time, f"End time {end_time} is not after inject time {inject_time}"

        # Parse service name
        inject_pod = case_data["inject_pod"]
        assert isinstance(inject_pod, str), f"inject_pod is not a string: {type(inject_pod)}"
        assert "-" in inject_pod, f"inject_pod '{inject_pod}' does not contain '-'"

        service_name = inject_pod.split("-")[0]
        assert service_name != "", f"Service name extracted from inject_pod '{inject_pod}' is empty"

        if "frontend" in inject_pod:
            service_name = inject_pod.split("-")[0]
        else:
            service_name = inject_pod.split("service")[0] + "service"

        new_case_data = {
            "injection_name": service_name,
            "fault_type": case_data["inject_type"],
            "fault_start_time": inject_time,
            "fault_end_time": end_time,
            "normal_start_time": "",
            "normal_end_time": "",
        }

        # Validate new case data
        assert new_case_data["injection_name"], "injection_name is empty"
        assert new_case_data["fault_type"], "fault_type is empty"
        assert isinstance(new_case_data["fault_start_time"], datetime), "fault_start_time is not a datetime"
        assert isinstance(new_case_data["fault_end_time"], datetime), "fault_end_time is not a datetime"

        return NezhaDatapackLoader(new_case_data, self.source_dir, inject_time, end_time)


class NezhaNormalDatasetLoader(DatasetLoader):
    def __init__(self, source_dir: str, date: str):
        self.source_dir = Path(os.path.join(source_dir, date))
        self.date = date
        self.log_file = self._find_log_file()
        self.normal_time = self._extract_time_from_filename()

    def _find_log_file(self) -> Path:
        log_dir = self.source_dir / "log"
        if not log_dir.exists():
            raise FileNotFoundError(f"Log directory not found: {log_dir}")

        log_files = list(log_dir.glob("*_log.csv"))
        return log_files[0]

    def _extract_time_from_filename(self) -> datetime:
        filename = self.log_file.name
        try:
            hour, minute = filename.split("_log.csv")[0].split("_")

            time_str = f"{self.date} {hour}:{minute}:00"

            return to_utc_time(time_str)
        except Exception as e:
            raise ValueError(f"Error parsing time from filename {filename}: {e}")

    def name(self) -> str:
        return "nezha_normal"

    def __len__(self) -> int:
        return 1

    def __getitem__(self, index: int) -> NezhaDatapackLoader:
        if index != 0:
            raise IndexError("Index out of range. NezhaNormalDatasetLoader has only one item.")

        normal_end_time = self.normal_time + timedelta(minutes=1)
        start_time_str = self.normal_time.strftime("%Y-%m-%d %H:%M:%S")
        end_time_str = normal_end_time.strftime("%Y-%m-%d %H:%M:%S")

        new_case_data = {
            "injection_name": "normal",
            "fault_type": "normal",
            "fault_start_time": "",
            "fault_end_time": "",
            "normal_start_time": start_time_str,
            "normal_end_time": end_time_str,
        }

        return NezhaDatapackLoader(
            new_case_data,
            self.source_dir,
            self.normal_time,
            normal_end_time,
        )


class MultiDateDatasetLoader(DatasetLoader):
    def __init__(self, source_dir: str, dates: list[str], name: str):
        assert source_dir is not None, "source_dir is None"
        assert isinstance(source_dir, str), f"source_dir is not a string: {type(source_dir)}"
        assert dates is not None, "dates is None"
        assert isinstance(dates, list), f"dates is not a list: {type(dates)}"
        assert len(dates) > 0, "dates list is empty"
        assert name is not None, "name is None"
        assert isinstance(name, str), f"name is not a string: {type(name)}"
        assert len(name) > 0, "name is empty"

        self.source_dir = source_dir
        self.dates = dates
        self._name = name
        self.all_cases = []

        abnormal_source_dir = os.path.join(source_dir, "rca_data")
        for date in dates:
            assert isinstance(date, str), f"Date is not a string: {type(date)}"
            assert len(date) > 0, f"Date is empty: {date}"

            loader = NezhaDatasetLoader(abnormal_source_dir, date)
            assert loader is not None, f"Failed to create NezhaDatasetLoader for date: {date}"

            for i in range(len(loader)):
                case = loader[i]
                assert case is not None, f"Case {i} is None for date: {date}"
                self.all_cases.append(case)

        normal_source_dir = os.path.join(source_dir, "construct_data")
        for date in dates:
            assert isinstance(date, str), f"Date is not a string: {type(date)}"
            assert len(date) > 0, f"Date is empty: {date}"

            loader = NezhaNormalDatasetLoader(normal_source_dir, date)
            assert loader is not None, f"Failed to create NezhaNormalDatasetLoader for date: {date}"

            self.all_cases.append(loader[0])

        assert len(self.all_cases) > 0, f"No cases loaded for any dates: {dates}"

    def name(self) -> str:
        return self._name

    def __len__(self) -> int:
        return len(self.all_cases)

    def __getitem__(self, index: int) -> NezhaDatapackLoader:
        assert 0 <= index < len(self.all_cases), (
            f"Index {index} out of range for all_cases of length {len(self.all_cases)}"
        )
        case = self.all_cases[index]
        assert case is not None, f"Case at index {index} is None"
        assert isinstance(case, NezhaDatapackLoader), (
            f"Case at index {index} is not a NezhaDatapackLoader: {type(case)}"
        )
        return case


@app.command(help="Convert Nezha dataset to RCABench format")
@timeit()
def run(
    source_dir: str = "data/nezha",
    ob_dates: list[str] = typer.Option(
        ["2022-08-22", "2022-08-23"],
        help="All dates for ob system, e.g., 2022-08-22 2022-08-23",
    ),
    tt_dates: list[str] = typer.Option(
        ["2023-01-29", "2023-01-30"],
        help="All dates for tt system, e.g., 2023-01-29 2023-01-30",
    ),
):
    if ob_dates:
        logger.info(f"Starting to process nezha_ob: {ob_dates}")
        loader_ob = MultiDateDatasetLoader(source_dir, ob_dates, name="nezha_ob")
        convert_dataset(loader_ob, skip_finished=False, parallel=4)
        logger.info("nezha_ob dataset processing completed!")
    if tt_dates:
        logger.info(f"Starting to process nezha_tt: {tt_dates}")
        loader_tt = MultiDateDatasetLoader(source_dir, tt_dates, name="nezha_tt")
        convert_dataset(loader_tt, skip_finished=False, parallel=4)
        logger.info("nezha_tt dataset processing completed!")


@app.command(help="Create a symbolic link from source path to target path for Nezha dataset access")
@timeit()
def create_link(
    source_path: str = typer.Option(
        "/mnt/jfs/Nezha/",
        "--source-path",
        "-s",
        help="Source path where the Nezha dataset is located (default: /mnt/jfs/Nezha/)",
    ),
    target_path: str = typer.Option(
        "data/nezha",
        "--target-path",
        "-t",
        help="Target path where the symbolic link will be created (default: data)",
    ),
):
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
