#!/usr/bin/env -S uv run -s
"""Convert AIOps21 dataset to standardized format.

single path overview
    nn@debian ~/w/r/d/a/a/A/aiops2021-2> pwd
    /home/nn/workspace/rcabench-platform/data/aiops/aiops2021-main/AIOps2021/aiops2021-2
    nn@debian ~/w/r/d/a/a/A/aiops2021-2> ls
    0304/  0305/  0306/ 0307/  0309/  0310/  0312/  0323/  0324/  0325/
"""

import datetime
import functools
from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import DatapackLoader, DatasetLoader, Label
from rcabench_platform.v3.sdk.datasets.spec import (
    get_datapack_folder,
    get_datapack_list,
    get_dataset_meta_file,
)
from rcabench_platform.v3.sdk.utils.fmap import fmap_threadpool
from rcabench_platform.v3.sdk.utils.serde import save_parquet


def convert_traces(src: Path) -> pl.LazyFrame:
    """Convert AIOps21 trace data to standard format.

    head trace_0304.csv
    timestamp,cmdb_id,parent_id,span_id,trace_id,duration
    1614787199628,dockerA2,369-bcou-dle-way1-c514cf30-43410@0824-2f0e47a816-17492,21030300016145905763,gw0120210304000517192504,19
    1614787199635,dockerA2,21030300016145905763,21030300016145905768,gw0120210304000517192504,1
    """
    assert src.exists(), f"Source file does not exist: {src}"
    sample_df = pl.read_csv(src, n_rows=10)

    # Get the first timestamp to determine unit
    first_timestamp = sample_df["timestamp"][0]

    # Heuristic: if timestamp > 1e12, it's likely milliseconds; otherwise seconds
    # 1e12 corresponds to ~2001-09-09 in milliseconds or ~33658 years in seconds
    if first_timestamp > 1e12:
        time_unit = "ms"
        logger.info(f"Detected millisecond timestamps in {src}")
    else:
        time_unit = "s"
        logger.info(f"Detected second timestamps in {src}")
    lf = pl.scan_csv(src, infer_schema_length=50000)

    # Convert to standard format
    lf = lf.select(
        # Convert timestamp from milliseconds to datetime UTC
        pl.from_epoch("timestamp", time_unit=time_unit).dt.offset_by("8h").alias("time"),
        pl.col("trace_id").cast(pl.String).alias("trace_id"),
        pl.col("span_id").cast(pl.String).alias("span_id"),
        pl.col("parent_id").cast(pl.String).alias("parent_span_id"),
        pl.col("cmdb_id").cast(pl.String).alias("service_name"),
        # Generate span_name from service_name for now, as we don't have operation names
        pl.col("cmdb_id").cast(pl.String).alias("span_name"),
        # Convert duration from milliseconds to nanoseconds
        pl.col("duration").cast(pl.UInt64).mul(1_000_000).alias("duration"),
    )

    # Fill empty parent_span_id with empty string (root spans)
    lf = lf.with_columns(pl.col("parent_span_id").fill_null(""))

    # Set parent_span_id to empty if it equals span_id (self-referencing spans)
    lf = lf.with_columns(
        pl.when(pl.col("span_id") == pl.col("parent_span_id"))
        .then(pl.lit(""))
        .otherwise(pl.col("parent_span_id"))
        .alias("parent_span_id")
    )

    lf = lf.sort("time")

    return lf


def convert_metrics(src: Path) -> pl.LazyFrame:
    """Convert AIOps21 metric data to standard format.

    head metric_0304.csv
    timestamp,cmdb_id,kpi_name,value
    1614787200,Tomcat04,OSLinux-CPU_CPU_CPUCpuUtil,26.2957
    1614787200,Mysql02,Mysql-MySQL_3306_Innodb data pending writes,0.0
    1614787200,Mysql02,Mysql-MySQL_3306_Innodb data pending reads,0.0
    """
    assert src.exists(), f"Source file does not exist: {src}"

    lf = pl.scan_csv(src, infer_schema_length=50000)

    # Convert to standard format
    lf = lf.select(
        # Convert timestamp from seconds to datetime UTC
        pl.from_epoch("timestamp", time_unit="s").dt.offset_by("8h").alias("time"),
        pl.col("kpi_name").cast(pl.String).alias("metric"),
        pl.col("value").cast(pl.Float64).alias("value"),
        pl.col("cmdb_id").cast(pl.String).alias("service_name"),
    )

    lf = lf.sort("time")

    return lf


def convert_logs(src_folder: Path) -> pl.LazyFrame:
    """Convert AIOps21 log data to standard format.

    head log_apache_access_log_0304.csv
    log_id,timestamp,cmdb_id,log_name,value
    ac189145b045a6f0228c873b7476066a,1614831458,apache02,apache_access_log,"IPAddress ..."
    c44044aec240d5cf69e65f0cd8935ee5,1614831459,apache02,apache_access_log,"IPAddress ..."
    """
    # AIOps21 has multiple log files in logs/ directory
    log_files = list((src_folder / "logs").glob("*.csv"))

    if not log_files:
        # Return empty dataframe with correct schema if no log files found
        return pl.LazyFrame(
            schema={
                "time": pl.Datetime,
                "trace_id": pl.String,
                "span_id": pl.String,
                "service_name": pl.String,
                "level": pl.String,
                "message": pl.String,
            }
        )

    dfs = []
    for log_file in log_files:
        lf = pl.scan_csv(log_file, infer_schema_length=50000)

        # Convert to standard format
        lf = lf.select(
            # Convert timestamp from seconds to datetime UTC
            pl.from_epoch("timestamp", time_unit="s").dt.offset_by("8h").alias("time"),
            pl.lit("").alias("trace_id"),  # AIOps21 logs don't have trace_id
            pl.lit("").alias("span_id"),  # AIOps21 logs don't have span_id
            pl.col("cmdb_id").cast(pl.String).alias("service_name"),
            pl.lit("").alias("level"),  # not provided
            pl.col("value").cast(pl.String).alias("message"),
        )

        dfs.append(lf)

    # Concatenate all log files
    combined_lf = pl.concat(dfs)
    combined_lf = combined_lf.sort("time")

    return combined_lf


class AIops21DatapackLoader(DatapackLoader):
    def __init__(
        self,
        src_folder: Path,
        dataset: str,
        datapack: str,
        fault_case: dict,
        date_str: str,
    ) -> None:
        self._src_folder = src_folder
        self._dataset = dataset
        self._datapack = datapack
        self._fault_case = fault_case
        self._date_str = date_str

    @property
    def name(self) -> str:
        return self._datapack

    def labels(self) -> list[Label]:
        return [Label(level="service", name=self._fault_case["service"])]

    def _get_data_time_range(self) -> tuple[pl.Datetime, pl.Datetime]:
        """Get the full time range of available data for the day."""
        # Check traces file first
        trace_file = self._src_folder / "trace" / f"trace_{self._date_str}.csv"
        if trace_file.exists():
            trace_lf = convert_traces(trace_file)
            trace_times = trace_lf.select(
                [
                    pl.col("time").min().alias("min_time"),
                    pl.col("time").max().alias("max_time"),
                ]
            ).collect()
            min_time = trace_times["min_time"][0]
            max_time = trace_times["max_time"][0]
            return min_time, max_time

        # Fallback to metrics file
        metric_file = self._src_folder / "metric" / f"metric_{self._date_str}.csv"
        if metric_file.exists():
            metric_lf = convert_metrics(metric_file)
            metric_times = metric_lf.select(
                [
                    pl.col("time").min().alias("min_time"),
                    pl.col("time").max().alias("max_time"),
                ]
            ).collect()
            min_time = metric_times["min_time"][0]
            max_time = metric_times["max_time"][0]
            return min_time, max_time

        # If no data files available, use fault injection times as fallback
        return self._fault_case["start_time"], self._fault_case["end_time"]

    def _to_iso_string(self, time_obj) -> str:
        """Convert time object to ISO format string."""
        if hasattr(time_obj, "isoformat"):
            return time_obj.isoformat()
        else:
            # For Polars datetime objects, convert to string
            return str(time_obj)

    def _calculate_time_windows(self) -> tuple[Any, Any, Any, Any]:
        """Get pre-calculated time windows from fault case data.

        Returns:
            tuple: (normal_start_time, normal_end_time, fault_start_time, fault_end_time)
        """
        # Use pre-calculated time windows from groundtruth data
        return (
            self._fault_case["normal_start_time"],
            self._fault_case["normal_end_time"],
            self._fault_case["fault_start_time"],
            self._fault_case["fault_end_time"],
        )

    def _validate_dataframes(self, data_dict: dict[str, Any]) -> None:
        """Validate that critical dataframes are not empty before saving."""
        # Check traces
        traces_df = data_dict["traces.parquet"].collect()
        if traces_df.is_empty():
            raise ValueError(f"Traces dataframe is empty for datapack {self.name}")

        # Check metrics
        metrics_df = data_dict["metrics.parquet"].collect()
        if metrics_df.is_empty():
            raise ValueError(f"Metrics dataframe is empty for datapack {self.name}")

        # Logs can be empty, so we'll just log a warning
        logs_df = data_dict["logs.parquet"].collect()
        if logs_df.is_empty():
            logger.warning(f"Logs dataframe is empty for datapack {self.name}")

    def data(self) -> dict[str, Any]:
        # Calculate time windows: [normal_start, normal_end, fault_start, fault_end]
        (
            normal_start_time,
            normal_end_time,
            fault_start_time,
            fault_end_time,
        ) = self._calculate_time_windows()

        # Create data dict with filtered data for the specific time periods
        # Data range covers from normal start to fault end
        overall_start_time = normal_start_time
        overall_end_time = fault_end_time

        data_dict: dict[str, Any] = {
            "traces.parquet": self._filter_traces_by_time(overall_start_time, overall_end_time),
            "metrics.parquet": self._filter_metrics_by_time(overall_start_time, overall_end_time),
            "logs.parquet": self._filter_logs_by_time(overall_start_time, overall_end_time),
        }

        # Validate dataframes before proceeding
        self._validate_dataframes(data_dict)

        # Add fault case metadata with both normal and fault time periods
        metadata = {
            "injection_name": self._fault_case["service"],
            "fault_type": self._fault_case["fault_type"],
            "fault_start_time": self._to_iso_string(fault_start_time),
            "fault_end_time": self._to_iso_string(fault_end_time),
            "normal_start_time": self._to_iso_string(normal_start_time),
            "normal_end_time": self._to_iso_string(normal_end_time),
            # Additional metadata for compatibility
            "fault_id": self._fault_case["id"],
            "fault_category": self._fault_case["fault_category"],
            "fault_content": self._fault_case["fault_content"],
            "fault_time": self._to_iso_string(self._fault_case["fault_time"]),
            "data_type": self._fault_case["data_type"],
        }
        data_dict["metadata.json"] = metadata

        return data_dict

    def _filter_traces_by_time(self, start_time, end_time) -> pl.LazyFrame:
        """Filter trace data by specified time window."""
        trace_file = self._src_folder / "trace" / f"trace_{self._date_str}.csv"
        if not trace_file.exists():
            # Return empty dataframe with correct schema
            return pl.LazyFrame(
                schema={
                    "time": pl.Datetime,
                    "trace_id": pl.String,
                    "span_id": pl.String,
                    "parent_span_id": pl.String,
                    "service_name": pl.String,
                    "span_name": pl.String,
                    "duration": pl.UInt64,
                }
            )

        lf = convert_traces(trace_file)

        # Filter by the specified time window (no buffer)
        return lf.filter((pl.col("time") >= start_time) & (pl.col("time") <= end_time))

    def _filter_metrics_by_time(self, start_time, end_time) -> pl.LazyFrame:
        """Filter metric data by specified time window."""
        metric_file = self._src_folder / "metric" / f"metric_{self._date_str}.csv"
        if not metric_file.exists():
            # Return empty dataframe with correct schema
            return pl.LazyFrame(
                schema={
                    "time": pl.Datetime,
                    "metric": pl.String,
                    "value": pl.Float64,
                    "service_name": pl.String,
                }
            )

        lf = convert_metrics(metric_file)

        # Filter by the specified time window (no buffer)
        return lf.filter((pl.col("time") >= start_time) & (pl.col("time") <= end_time))

    def _filter_logs_by_time(self, start_time, end_time) -> pl.LazyFrame:
        """Filter log data by specified time window."""
        lf = convert_logs(self._src_folder)

        # Filter by the specified time window (no buffer)
        return lf.filter((pl.col("time") >= start_time) & (pl.col("time") <= end_time))


class AIops21DatasetLoader(DatasetLoader):
    def __init__(self, src_folder: Path, dataset: str):
        self._src_folder = src_folder
        self._dataset = dataset

        datapack_loaders = []

        # Load groundtruth data and group by date
        gt_df = load_groundtruth()

        # Group fault cases by date_str
        for date_str in gt_df["date_str"].unique():
            date_path = src_folder / date_str

            if not date_path.exists() or not date_path.is_dir():
                logger.warning(f"Skipping {date_str}: date folder does not exist")
                continue

            # Check if required files exist
            trace_file = date_path / "trace" / f"trace_{date_str}.csv"
            metric_file = date_path / "metric" / f"metric_{date_str}.csv"

            if not (trace_file.exists() and metric_file.exists()):
                logger.warning(f"Skipping {date_str}: missing required files")
                continue

            # Get all fault cases for this date
            date_cases = gt_df.filter(pl.col("date_str") == date_str)

            # Create a datapack for each fault case in this date
            for row in date_cases.iter_rows(named=True):
                fault_case = {
                    "id": row["id"],
                    "service": row["service"],
                    "fault_type": row["fault_type_combined"],
                    "fault_category": row["故障类别"],
                    "fault_content": row["故障内容"],
                    "fault_time": row["fault_time"],
                    "start_time": row["start_time"],
                    "end_time": row["end_time"],
                    "data_type": row["data_type"],
                    "datapack": row["datapack"],
                    # Add pre-calculated time windows
                    "normal_start_time": row["normal_start_time"],
                    "normal_end_time": row["normal_end_time"],
                    "fault_start_time": row["fault_start_time"],
                    "fault_end_time": row["fault_end_time"],
                }

                loader = AIops21DatapackLoader(
                    src_folder=date_path,
                    dataset=dataset,
                    datapack=fault_case["datapack"],
                    fault_case=fault_case,
                    date_str=date_str,
                )

                datapack_loaders.append(loader)

        self._datapack_loaders = datapack_loaders

    def name(self) -> str:
        return self._dataset

    def __len__(self) -> int:
        return len(self._datapack_loaders)

    def __getitem__(self, index: int) -> DatapackLoader:
        return self._datapack_loaders[index]


def load_groundtruth():
    """
    Load and process groundtruth data for fault injection times.
    Returns a DataFrame with all case information and generated datapack names.
    """
    df = pl.read_csv("data/aiops_challenge/aiops2021-main/AIOps2021/aiops21_groundtruth.csv")

    # Convert time from milliseconds to datetime and sort by time
    df = df.with_columns(
        [
            pl.from_epoch("time", time_unit="ms").dt.offset_by("8h").alias("fault_time"),
            pl.col("st_time").str.to_datetime("%Y-%m-%d %H:%M:%S%.f").alias("start_time"),
            pl.col("ed_time").str.to_datetime("%Y-%m-%d %H:%M:%S%.f").alias("end_time"),
            # Clean up anomaly_type
            pl.col("anomaly_type").str.replace(";", "").str.replace("\n", "").alias("anomaly_type"),
            # Map fault content to standard names
            pl.col("故障内容")
            .replace_strict(
                {
                    "网络延迟": "delay",
                    "内存使用率过高": "stress",
                    "JVM CPU负载高": "stress",
                    "JVM OOM Heap": "OOM",
                    "磁盘IO读使用率过高": "payload",
                    "CPU使用率高": "stress",
                    "网络丢包": "loss",
                    "磁盘空间使用率过高": "usage",
                },
                default=pl.col("故障内容"),
            )
            .alias("fault_content_mapped"),
        ]
    ).sort("fault_time")

    # Generate additional columns
    df = df.with_columns(
        [
            # Extract date string (MMDD format)
            pl.col("fault_time").dt.strftime("%m%d").alias("date_str"),
            # Extract time string (HHMM format)
            pl.col("fault_time").dt.strftime("%H%M").alias("time_str"),
            # Generate combined fault type using mapped content
            (pl.col("anomaly_type") + "_" + pl.col("fault_content_mapped")).alias("fault_type_combined"),
        ]
    )

    # Generate datapack names
    df = df.with_columns(
        [
            (
                pl.lit("aiops21_")
                + pl.col("fault_type_combined")
                + "_"
                + pl.col("service")
                + "_"
                + pl.col("date_str")
                + "_"
                + pl.col("time_str")
            ).alias("datapack")
        ]
    )

    # Calculate time windows for each date
    df = _calculate_time_windows_for_groundtruth(df)

    return df


def _calculate_time_windows_for_groundtruth(df: pl.DataFrame) -> pl.DataFrame:
    """Calculate normal and fault time windows for all cases, grouped by date."""
    src_folder = Path("data/aiops_challenge/aiops2021-main/AIOps2021/aiops2021-2")

    # Get data time ranges for each date
    date_time_ranges = {}
    for date_str in df["date_str"].unique():
        date_path = src_folder / date_str
        if not date_path.exists():
            continue

        # Check traces file first
        trace_file = date_path / "trace" / f"trace_{date_str}.csv"
        if trace_file.exists():
            sample_df = pl.read_csv(trace_file, n_rows=10)
            first_timestamp = sample_df["timestamp"][0]
            time_unit = "ms" if first_timestamp > 1e12 else "s"

            times_df = (
                pl.scan_csv(trace_file)
                .select(
                    [
                        pl.from_epoch(pl.col("timestamp").min(), time_unit=time_unit)
                        .dt.offset_by("8h")
                        .alias("min_time"),
                        pl.from_epoch(pl.col("timestamp").max(), time_unit=time_unit)
                        .dt.offset_by("8h")
                        .alias("max_time"),
                    ]
                )
                .collect()
            )
            date_time_ranges[date_str] = (
                times_df["min_time"][0],
                times_df["max_time"][0],
            )
            continue

        # Fallback to metrics file
        metric_file = date_path / "metric" / f"metric_{date_str}.csv"
        if metric_file.exists():
            times_df = (
                pl.scan_csv(metric_file)
                .select(
                    [
                        pl.from_epoch(pl.col("timestamp").min(), time_unit="s").dt.offset_by("8h").alias("min_time"),
                        pl.from_epoch(pl.col("timestamp").max(), time_unit="s").dt.offset_by("8h").alias("max_time"),
                    ]
                )
                .collect()
            )
            date_time_ranges[date_str] = (
                times_df["min_time"][0],
                times_df["max_time"][0],
            )

    # Calculate time windows for each row
    result_rows = []
    for row in df.iter_rows(named=True):
        date_str = row["date_str"]
        fault_start_time = row["start_time"]
        fault_end_time = row["end_time"]

        data_start_time, data_end_time = date_time_ranges[date_str]

        # Calculate normal time window: before fault injection with 15-minute limit
        normal_end_time = fault_start_time
        max_normal_duration = datetime.timedelta(minutes=15)
        earliest_normal_start = fault_start_time - max_normal_duration

        # Take the later time: either data starts or 15 minutes before fault
        if data_start_time > earliest_normal_start:
            normal_start_time = data_start_time
        else:
            normal_start_time = earliest_normal_start

        # Add calculated time windows to row
        row_dict = dict(row)
        row_dict["normal_start_time"] = normal_start_time
        row_dict["normal_end_time"] = normal_end_time
        row_dict["fault_start_time"] = fault_start_time
        row_dict["fault_end_time"] = fault_end_time

        result_rows.append(row_dict)

    return pl.DataFrame(result_rows)


def generate_attributes_from_groundtruth(dataset: str, gt_df: pl.DataFrame):
    """Generate attributes dataframe directly from groundtruth data."""
    # Get list of actually converted datapacks
    datapacks = get_datapack_list(dataset)
    datapack_set = set(datapacks)

    # Filter groundtruth to only include converted datapacks
    converted_gt = gt_df.filter(pl.col("datapack").is_in(datapack_set))

    if converted_gt.is_empty():
        logger.warning(f"No converted datapacks found for dataset {dataset}")
        return

    # Convert to attributes format using calculated time windows
    attrs_df = converted_gt.select(
        [
            pl.lit(dataset).alias("dataset"),
            pl.col("datapack"),
            pl.col("fault_time").alias("inject_time"),
            pl.col("fault_type_combined").alias("injection.fault_type"),
            pl.col("service").alias("injection.service"),
            pl.col("故障类别").alias("injection.fault_category"),
            pl.col("故障内容").alias("injection.fault_content"),
            # Use calculated time windows
            pl.col("normal_start_time").alias("env.normal_start"),
            pl.col("normal_end_time").alias("env.normal_end"),
            pl.col("fault_start_time").alias("env.abnormal_start"),
            pl.col("fault_end_time").alias("env.abnormal_end"),
        ]
    )

    # Add file sizes by scanning converted datapacks
    attrs_list = []
    for row in attrs_df.iter_rows(named=True):
        attrs = dict(row)

        # Calculate file size for this datapack
        input_folder = get_datapack_folder(dataset, row["datapack"])
        if input_folder.exists():
            total_size = 0
            for file in input_folder.iterdir():
                if file.is_file() and not file.name.startswith("."):
                    total_size += file.stat().st_size
            attrs["files.total_size:MiB"] = round(total_size / (1024 * 1024), 6)
        else:
            attrs["files.total_size:MiB"] = 0.0

        attrs_list.append(attrs)

    # Convert back to DataFrame and sort
    result_df = pl.DataFrame(attrs_list).sort("inject_time", descending=True)

    # Save to parquet
    save_parquet(result_df, path=get_dataset_meta_file(dataset, "attributes.parquet"))
    logger.info(f"Generated attributes for {len(result_df)} datapacks")


@app.command()
def local_test():
    src = Path("data/aiops/aiops2021-main/AIOps2021/aiops2021-2/0306")
    # Test the conversion functions
    logger.info(f"Testing conversion functions with {src}")

    # Test traces conversion
    traces_file = src / "trace" / "trace_0306.csv"
    if traces_file.exists():
        traces_lf = convert_traces(traces_file)
        logger.info(f"Traces converted: {traces_lf.collect().shape}")

    # Test metrics conversion
    metrics_file = src / "metric" / "metric_0306.csv"
    if metrics_file.exists():
        metrics_lf = convert_metrics(metrics_file)
        logger.info(f"Metrics converted: {metrics_lf.collect().shape}")

    # Test logs conversion
    logs_lf = convert_logs(src)
    logger.info(f"Logs converted: {logs_lf.collect().shape}")


@app.command()
@timeit()
def run():
    """Convert AIOps21 dataset to RCABench format."""
    from rcabench_platform.v3.internal.sources.convert import convert_dataset

    src_folder = Path("data/aiops_challenge/aiops2021-main/AIOps2021/aiops2021-2")
    dataset_name = "aiops21"

    if not src_folder.exists():
        logger.error(f"Source folder not found: {src_folder}")
        return

    gt_df = load_groundtruth()

    loader = AIops21DatasetLoader(src_folder, dataset_name)
    logger.info(f"Found {len(loader)} datapacks in {src_folder}")

    convert_dataset(loader, parallel=4, skip_finished=True)
    logger.info(f"Conversion completed for dataset: {dataset_name}")

    logger.info("Generating attributes from groundtruth data...")
    generate_attributes_from_groundtruth(dataset_name, gt_df)
    logger.info("Attributes generation completed")


if __name__ == "__main__":
    app()
