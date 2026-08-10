from pathlib import Path
from typing import Any

import polars as pl

from .convert import DatapackLoader, DatasetLoader, Label


def convert_traces(src: Path):
    assert src.exists(), src

    lf = pl.scan_csv(src, infer_schema_length=50000)

    lf = lf.select(
        pl.from_epoch("startTime", time_unit="us").dt.replace_time_zone("UTC").alias("time"),
        pl.col("traceID").alias("trace_id"),
        pl.col("spanID").alias("span_id"),
        pl.col("serviceName").alias("service_name"),
        pl.col("operationName").alias("span_name"),
        pl.col("parentSpanID").alias("parent_span_id"),
        pl.col("duration").cast(pl.UInt64).mul(1000).alias("duration"),
        pl.col("statusCode").cast(pl.Int16, strict=False).alias("attr.status_code"),
    )

    return lf


def convert_metrics(src: Path):
    assert src.exists(), src

    lf = pl.scan_csv(src, infer_schema_length=50000)

    lf = lf.with_columns(pl.from_epoch("time", time_unit="s").dt.replace_time_zone("UTC").alias("time"))

    lf = lf.unpivot(
        on=None,
        index="time",
        variable_name="metric",
        value_name="value",
    )

    lf = lf.with_columns(
        pl.col("metric").str.split("_").alias("_split"),
    )

    lf = lf.with_columns(
        pl.col("_split").list.get(0).alias("service_name"),
        pl.col("_split").list.get(1).alias("metric"),
    )

    lf = lf.drop("_split")

    return lf


def convert_logs(src: Path):
    assert src.exists(), src

    lf = pl.scan_csv(src, infer_schema_length=50000)

    # Get column names to check for optional columns
    columns = lf.collect_schema().names()

    # Error keywords to search for in messages
    ERROR_KWS = ["error", "fail", "exception", "timeout", "refused"]

    # Base columns that should always exist
    base_select = [
        pl.from_epoch("timestamp", time_unit="ns").dt.replace_time_zone("UTC").alias("time"),
        pl.col("container_name").cast(pl.String).alias("service_name"),
        # Add empty trace_id and span_id columns
        pl.lit(None).cast(pl.String).alias("trace_id"),
        pl.lit(None).cast(pl.String).alias("span_id"),
    ]

    # Handle message column with optional error concatenation
    if "error" in columns:
        message_expr = (
            pl.when(pl.col("error").is_not_null())
            .then(pl.concat_str([pl.col("message"), pl.lit(" "), pl.col("error")]))
            .otherwise(pl.col("message"))
            .alias("message")
        )
    else:
        message_expr = pl.col("message")

    base_select.append(message_expr)

    # Handle level column - either use existing or parse from message
    if "level" in columns:
        base_select.append(pl.col("level").cast(pl.String))
    else:
        # Parse level from message - look for common log levels anywhere in the message
        # Keep null when extraction fails
        level_expr = pl.col("message").str.extract(r"\b(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\b").alias("level")
        base_select.append(level_expr)

    # Add req_path with attr prefix if it exists
    if "req_path" in columns:
        base_select.append(pl.col("req_path").cast(pl.String).alias("attr.req_path"))

    # Add optional columns if they exist
    if "cluster_id" in columns:
        base_select.append(pl.col("cluster_id").cast(pl.UInt8).alias("attr.cluster_id"))

    if "log_template" in columns:
        base_select.append(pl.col("log_template").cast(pl.String).alias("attr.log_template"))

    # First apply the select to get the transformed columns
    lf = lf.select(base_select)

    # Then add attr.has_error based on the transformed message column
    # This ensures we're checking the complete message including any concatenated error info
    lf = lf.with_columns(
        pl.col("message").str.contains(f"(?i){'|'.join(ERROR_KWS)}").fill_null(False).alias("attr.has_error")
    )

    return lf


class RcaevalDatapackLoader(DatapackLoader):
    def __init__(self, src_folder: Path, dataset: str, datapack: str, service: str) -> None:
        self._src_folder = src_folder
        self._dataset = dataset
        self._datapack = datapack
        self._service = service

    @property
    def name(self) -> str:
        return self._datapack

    def labels(self) -> list[Label]:
        return [Label(level="service", name=self._service)]

    def data(self) -> dict[str, Any]:
        data = {
            "inject_time.txt": self._src_folder / "inject_time.txt",
            "simple_metrics.parquet": convert_metrics(self._src_folder / "simple_metrics.csv"),
            "logs.parquet": convert_logs(self._src_folder / "logs.csv"),
        }
        if (self._src_folder / "traces.csv").exists():
            data["traces.parquet"] = convert_traces(self._src_folder / "traces.csv")

        return data


class RcaevalDatasetLoader(DatasetLoader):
    def __init__(self, src_folder: Path, dataset: str):
        self._src_folder = src_folder
        self._dataset = dataset

        datapack_loaders = []

        for service_path in src_folder.iterdir():
            if not service_path.is_dir():
                continue

            for num_path in service_path.iterdir():
                if not num_path.is_dir():
                    continue

                service = service_path.name
                num = num_path.name
                datapack = f"{service}_{num}"

                if num == "multi-source-data":
                    continue

                loader = RcaevalDatapackLoader(
                    src_folder=num_path,
                    dataset=dataset,
                    datapack=datapack,
                    service=service.split("_")[0],
                )

                datapack_loaders.append(loader)

        self._datapack_loaders = datapack_loaders

    def name(self) -> str:
        return self._dataset

    def __len__(self) -> int:
        return len(self._datapack_loaders)

    def __getitem__(self, index: int) -> DatapackLoader:
        return self._datapack_loaders[index]
