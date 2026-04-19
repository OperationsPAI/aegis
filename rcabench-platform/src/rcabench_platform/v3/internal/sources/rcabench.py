from collections.abc import Callable
from pathlib import Path
from typing import Any

import pandas as pd
import polars as pl
from drain3 import TemplateMiner
from drain3.drain import LogCluster
from drain3.file_persistence import FilePersistence
from drain3.template_miner_config import TemplateMinerConfig

from ...sdk.datasets.rcabench import get_service_names
from ...sdk.logging import logger, timeit
from ...sdk.pedestals import get_pedestal
from ...sdk.utils.serde import load_json
from .convert import DatapackLoader, DatasetLoader, Label


def replace_time_col(lf: pl.LazyFrame, col_name: str) -> pl.LazyFrame:
    """Rename timestamp column to 'time'"""
    return lf.with_columns(pl.col(col_name)).rename({col_name: "time"})


def unnest_json_col(lf: pl.LazyFrame, col_name: str, dtype: pl.Struct) -> pl.LazyFrame:
    """Parse JSON column and unnest struct fields"""
    return lf.with_columns(pl.col(col_name).str.json_decode(dtype).struct.unnest()).drop(col_name)


class Converter:
    """RCABench data format converter"""

    def __init__(self, src_folder: Path, prefix: str, system: str = "ts") -> None:
        self._src_folder = src_folder
        self._prefix = prefix
        self._pedestal = get_pedestal(system)

    def metrics(self, src: Path) -> pl.LazyFrame:
        """Convert metrics parquet to standardized format"""
        lf = pl.scan_parquet(src)
        original_columns: list[str] = lf.collect_schema().names()

        selected_columns = ["TimeUnix", "MetricName", "Value", "ResourceAttributes"]
        additional_columns = ["ServiceName", "Attributes"]
        for col in additional_columns:
            if col in original_columns:
                selected_columns.append(col)

        lf = lf.select(selected_columns)
        lf = replace_time_col(lf, "TimeUnix")
        lf = lf.rename({"MetricName": "metric", "Value": "value"})

        if "ServiceName" in original_columns:
            lf = lf.rename({"ServiceName": "service_name"})
        else:
            lf = lf.with_columns(pl.lit(None, dtype=pl.String).alias("service_name"))

        attr_cols = []

        resource_attributes = pl.Struct(
            [
                pl.Field("k8s.node.name", pl.String),
                pl.Field("k8s.namespace.name", pl.String),
                pl.Field("k8s.statefulset.name", pl.String),
                pl.Field("k8s.deployment.name", pl.String),
                pl.Field("k8s.replicaset.name", pl.String),
                pl.Field("k8s.pod.name", pl.String),
                pl.Field("k8s.container.name", pl.String),
            ]
        )
        lf = unnest_json_col(lf, "ResourceAttributes", resource_attributes)
        attr_cols += [field.name for field in resource_attributes.fields]

        if "Attributes" in original_columns:
            attributes = pl.Struct(
                [
                    pl.Field("destination_workload", pl.String),
                    pl.Field("source_workload", pl.String),
                    pl.Field("destination", pl.String),
                    pl.Field("source", pl.String),
                ]
            )
            lf = unnest_json_col(lf, "Attributes", attributes)
            attr_cols += [field.name for field in attributes.fields]

        lf = lf.rename({col: "attr." + col for col in attr_cols})
        return lf.sort("time")

    def metrics_histogram(self, src: Path) -> pl.LazyFrame:
        """Convert histogram metrics to standardized format"""
        lf = pl.scan_parquet(src).select(
            "TimeUnix",
            "MetricName",
            "ServiceName",
            "ResourceAttributes",
            "Attributes",
            "Count",
            "Sum",
            "Min",
            "Max",
        )

        lf = replace_time_col(lf, "TimeUnix")
        lf = lf.rename(
            {
                "MetricName": "metric",
                "ServiceName": "service_name",
                "Count": "count",
                "Sum": "sum",
                "Min": "min",
                "Max": "max",
            }
        )

        lf = lf.with_columns(
            pl.col("count").cast(pl.Float64),
            pl.col("sum").cast(pl.Float64),
            pl.col("min").cast(pl.Float64),
            pl.col("max").cast(pl.Float64),
        )

        resource_attributes = pl.Struct(
            [
                pl.Field("pod.name", pl.String),
                pl.Field("service.name", pl.String),
                pl.Field("service.namespace", pl.String),
            ]
        )
        lf = unnest_json_col(lf, "ResourceAttributes", resource_attributes)
        lf = lf.rename(
            {
                "pod.name": "attr.k8s.pod.name",
                "service.name": "attr.k8s.service.name",
                "service.namespace": "attr.k8s.namespace.name",
            }
        )

        attributes = pl.Struct(
            [
                pl.Field("jvm.gc.action", pl.String),
                pl.Field("jvm.gc.name", pl.String),
                pl.Field("destination", pl.String),
                pl.Field("source", pl.String),
            ]
        )
        lf = unnest_json_col(lf, "Attributes", attributes)
        lf = lf.rename({field.name: "attr." + field.name for field in attributes.fields})

        return lf.sort("time")

    def traces(self, src: Path, filter_long: bool = False) -> pl.DataFrame:
        """Convert trace parquet to standardized format"""
        lf = pl.scan_parquet(src).select(
            "Timestamp",
            "TraceId",
            "SpanId",
            "ParentSpanId",
            "SpanName",
            "SpanKind",
            "ServiceName",
            "ResourceAttributes",
            "SpanAttributes",
            "Duration",
            "StatusCode",
        )

        lf = replace_time_col(lf, "Timestamp")
        lf = lf.rename(
            {
                "TraceId": "trace_id",
                "SpanId": "span_id",
                "ParentSpanId": "parent_span_id",
                "SpanName": "span_name",
                "ServiceName": "service_name",
                "Duration": "duration",
                "SpanKind": "attr.span_kind",
                "StatusCode": "attr.status_code",
            }
        )

        resource_attributes = pl.Struct(
            [
                pl.Field("pod.name", pl.String),
                pl.Field("service.name", pl.String),
                pl.Field("service.namespace", pl.String),
            ]
        )
        lf = unnest_json_col(lf, "ResourceAttributes", resource_attributes)
        lf = lf.rename(
            {
                "pod.name": "attr.k8s.pod.name",
                "service.name": "attr.k8s.service.name",
                "service.namespace": "attr.k8s.namespace.name",
            }
        )

        span_attributes = pl.Struct(
            [
                pl.Field("http.method", pl.String),
                pl.Field("http.request_content_length", pl.String),
                pl.Field("http.response_content_length", pl.String),
                pl.Field("http.status_code", pl.String),
                pl.Field("http.request.method", pl.String),
                pl.Field("http.response.status_code", pl.String),
            ]
        )
        lf = unnest_json_col(lf, "SpanAttributes", span_attributes)

        coalesce_columns = [
            ("http.request.method", "http.method"),
            ("http.response.status_code", "http.status_code"),
        ]
        lf = lf.with_columns([pl.coalesce(*cols).alias(cols[0]) for cols in coalesce_columns])
        lf = lf.drop([cols[1] for cols in coalesce_columns])

        lf = lf.with_columns(
            pl.col("http.request_content_length").cast(pl.UInt64),
            pl.col("http.response_content_length").cast(pl.UInt64),
            pl.col("http.response.status_code").cast(pl.UInt16),
        )

        lf = lf.rename(
            {
                "http.request.method": "attr.http.request.method",
                "http.response.status_code": "attr.http.response.status_code",
                "http.request_content_length": "attr.http.request.content_length",
                "http.response_content_length": "attr.http.response.content_length",
            }
        )

        if filter_long:
            traces_with_long_spans = lf.filter(pl.col("duration") > 2_000_000_000).select("trace_id").unique()
            lf = lf.join(traces_with_long_spans, on="trace_id", how="anti")

        lf = lf.sort("time")
        df = lf.collect()

        # Normalize paths for loadgenerator and entrance service
        df = df.with_columns(
            [
                pl.when(pl.col("service_name").is_in(["loadgenerator", self._pedestal.entrance_service]))
                .then(pl.col("span_name").map_elements(self._pedestal.normalize_path, return_dtype=pl.String))
                .otherwise(pl.col("span_name"))
                .alias("span_name")
            ]
        )

        return df

    def logs(self, src: Path) -> pl.DataFrame:
        """Convert log parquet to standardized format with template extraction"""
        lf = pl.scan_parquet(src).select(
            "Timestamp",
            "TraceId",
            "SpanId",
            "SeverityText",
            "ServiceName",
            "Body",
            "ResourceAttributes",
        )

        lf = replace_time_col(lf, "Timestamp")
        lf = lf.rename(
            {
                "TraceId": "trace_id",
                "SpanId": "span_id",
                "ServiceName": "service_name",
                "SeverityText": "level",
                "Body": "message",
            }
        )

        lf = lf.with_columns(pl.col("level").str.to_uppercase())
        lf = lf.filter(pl.col("service_name") != "ts-ui-dashboard")

        resource_attributes = pl.Struct(
            [
                pl.Field("pod.name", pl.String),
                pl.Field("service.name", pl.String),
                pl.Field("service.namespace", pl.String),
            ]
        )
        lf = unnest_json_col(lf, "ResourceAttributes", resource_attributes)
        lf = lf.rename(
            {
                "pod.name": "attr.k8s.pod.name",
                "service.name": "attr.k8s.service.name",
                "service.namespace": "attr.k8s.namespace.name",
            }
        )

        lf = lf.sort("time")
        df = lf.collect()

        # Extract templates
        unique_messages = df.select("message").unique()

        if unique_messages.height > 0:
            logger.info(f"Processing {unique_messages.height} unique log messages for template extraction")

            import os

            input_path = os.getenv("INPUT_PATH")
            template_base = (
                Path(input_path).parent / "drain_template" if input_path else src.parent.parent / "drain_template"
            )

            config_path = template_base / "drain_ts.ini"
            persistence_path = template_base / "drain_ts.bin"

            logger.info(f"Using template paths: config={config_path}, persistence={persistence_path}")

            template_miner = create_template_miner(config_path, persistence_path)

            message_mappings = []
            for message in unique_messages["message"].to_list():
                if message:
                    result = template_miner.add_log_message(message)
                    template_id = result["cluster_id"]
                    cluster = template_miner.drain.id_to_cluster.get(template_id)
                    log_template = cluster.get_template() if isinstance(cluster, LogCluster) else ""

                    message_mappings.append(
                        {
                            "message": message,
                            "template_id": template_id,
                            "log_template": log_template,
                        }
                    )

            del template_miner

            template_mapping_df = pl.DataFrame(
                message_mappings,
                schema={
                    "message": pl.String,
                    "template_id": pl.UInt16,
                    "log_template": pl.String,
                },
            )

            df = (
                df.join(template_mapping_df, on="message", how="left")
                .with_columns(
                    [
                        pl.col("template_id").alias("attr.template_id"),
                        pl.col("log_template").alias("attr.log_template"),
                    ]
                )
                .drop(["template_id", "log_template"])
            )

            del template_mapping_df, unique_messages

        return df

    def data(self) -> dict[str, Any]:
        mapping: dict[str, Callable] = {
            f"{self._prefix}_traces.parquet": self.traces,
            f"{self._prefix}_logs.parquet": self.logs,
            f"{self._prefix}_metrics.parquet": self.metrics,
            f"{self._prefix}_metrics_sum.parquet": self.metrics,
            f"{self._prefix}_metrics_histogram.parquet": self.metrics_histogram,
        }

        result: dict[str, Any] = {}
        for file_path, func in mapping.items():
            result[file_path] = func(self._src_folder / file_path)

        return result


def convert_conclusion(csv_path: Path) -> pl.LazyFrame:
    """Convert detector conclusion CSV to parquet format"""
    schema = {
        "SpanName": pl.String,
        "Issues": pl.String,
        "AbnormalAvgDuration": pl.Float64,
        "NormalAvgDuration": pl.Float64,
        "AbnormalSuccRate": pl.Float64,
        "NormalSuccRate": pl.Float64,
        "AbnormalP90": pl.Float64,
        "NormalP90": pl.Float64,
        "AbnormalP95": pl.Float64,
        "NormalP95": pl.Float64,
        "AbnormalP99": pl.Float64,
        "NormalP99": pl.Float64,
    }

    if not csv_path.exists():
        logger.warning(f"Conclusion CSV does not exist: {csv_path}")
        return pl.LazyFrame(schema=schema)

    try:
        return pl.scan_csv(csv_path, schema=schema)
    except Exception as e:
        logger.warning(f"Error reading conclusion CSV {csv_path}: {e}")
        return pl.LazyFrame(schema=schema)


class RCABenchDatapackLoader(DatapackLoader):
    def __init__(self, src_folder: Path, datapack: str, system: str = "ts") -> None:
        self._src_folder = src_folder
        self._datapack = datapack
        self._system = system

        self.validate_datapack()

    @property
    def name(self) -> str:
        return self._datapack

    def labels(self) -> list[Label]:
        injection: dict[str, Any] = load_json(path=self._src_folder / "injection.json")
        service_names = get_service_names(injection)

        labels: list[Label] = []
        for service in service_names:
            label = Label(level="service", name=service)
            labels.append(label)

        return labels

    def data(self) -> dict[str, Any]:
        ans: dict[str, Any] = {
            "env.json": self._src_folder / "env.json",
            "injection.json": self._src_folder / "injection.json",
            "conclusion.parquet": convert_conclusion(self._src_folder / "conclusion.csv"),
        }

        # Convert traces, logs, metrics for both normal and abnormal
        for prefix in ("normal", "abnormal"):
            converter = Converter(self._src_folder, prefix=prefix, system=self._system)
            result = converter.data()
            ans.update(result)

        return ans


def create_template_miner(config_path: Path, persistence_path: Path) -> TemplateMiner:
    """Create Drain3 template miner with file persistence"""
    persistence = FilePersistence(str(persistence_path))
    miner_config = TemplateMinerConfig()
    miner_config.load(str(config_path))
    return TemplateMiner(persistence, config=miner_config)


def extract_unique_log_messages(src_root: Path, datapacks: list[str]) -> pl.DataFrame:
    """Extract unique log messages from all datapacks (excluding ts-ui-dashboard)"""
    all_logs = []

    for datapack in datapacks:
        datapack_folder = src_root / datapack
        for prefix in ("normal", "abnormal"):
            log_file = datapack_folder / f"{prefix}_logs.parquet"
            if log_file.exists():
                lf = pl.scan_parquet(log_file).select("Body", "ServiceName")
                all_logs.append(lf)

    if not all_logs:
        return pl.DataFrame(schema={"Body": pl.String})

    combined_lf = pl.concat(all_logs)
    unique_messages = combined_lf.filter(pl.col("ServiceName") != "ts-ui-dashboard").select("Body").unique().collect()

    return unique_messages


@timeit()
def scan_datapacks(src_root: Path) -> list[str]:
    """Scan source folder for valid datapacks"""
    datapacks = []
    for path in src_root.iterdir():
        if not path.is_dir():
            continue

        if not (path / "injection.json").exists():
            continue

        if not (path / "conclusion.csv").exists():
            continue

        try:
            df = pd.read_csv(path / "conclusion.csv")

            if "Issues" in df.columns and (df["Issues"] == "{}").all():
                logger.warning(f"Skipping datapack `{path}` - all Issues are empty")
                continue
        except Exception as e:
            logger.warning(f"Error reading conclusion CSV {path / 'conclusion.csv'}: {e}")
            continue

        total_size = sum(file.stat().st_size for file in path.iterdir() if file.is_file())
        total_size_mib = total_size / (1024 * 1024)

        if total_size_mib > 500:
            logger.warning(f"Skipping large datapack `{path.name}` with size {total_size_mib:.2f} MiB")
            continue

        mtime = path.stat().st_mtime
        datapacks.append((path.name, mtime))

    datapacks.sort(key=lambda x: x[1])
    return [name for name, _ in datapacks]


class RcabenchDatasetLoader(DatasetLoader):
    def __init__(self, src_root: Path, dataset: str) -> None:
        self._src_root = src_root
        self._dataset = dataset
        self._datapacks = scan_datapacks(src_root)

    def name(self) -> str:
        return self._dataset

    def __len__(self) -> int:
        return len(self._datapacks)

    def __getitem__(self, index: int) -> DatapackLoader:
        datapack = self._datapacks[index]
        return RCABenchDatapackLoader(src_folder=self._src_root / datapack, datapack=datapack)
