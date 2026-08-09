import functools
import json
import os
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

import matplotlib.dates as mdates
import matplotlib.pyplot as plt
import polars as pl
from rcabench.openapi import (
    BatchEvaluateDatapackReq,
    ContainerRef,
    EvaluateDatapackSpec,
    EvaluationsApi,
)

from ..internal.clients.rcabench_ import get_rcabench_client
from ..sdk.datasets.rcabench import valid
from ..sdk.logging import logger
from ..sdk.pedestals import Pedestal, get_pedestal
from ..sdk.utils.display import get_timestamp
from ..sdk.utils.fmap import fmap_processpool

DETECTOR_NAME = "detector"


class VisDetector:
    def __init__(self, datapack: Path, system: str = "ts") -> None:
        self.datapack: Path = datapack
        self.pedestal: Pedestal = get_pedestal(system)
        self.output_path: Path = Path("temp") / "vis_detector" / get_timestamp()
        if not valid(self.datapack):
            raise ValueError(f"Invalid datapack: {self.datapack}")

    @staticmethod
    def _extract_status_code(span_attributes: str) -> str:
        """
        Extract HTTP status code from span attributes.
        """
        try:
            ra = json.loads(span_attributes) if span_attributes else {}
            return ra["http.status_code"]
        except Exception:
            return "-1"

    def _prepare_trace_data(self) -> None:
        """
        Load and prepare trace data for visualization.
        """
        df1: pl.DataFrame = pl.scan_parquet(self.datapack / "normal_traces.parquet").collect()
        df2: pl.DataFrame = pl.scan_parquet(self.datapack / "abnormal_traces.parquet").collect()

        self.normal_df = df1.with_columns(pl.lit("normal").alias("trace_type"))
        self.abnormal_df = df2.with_columns(pl.lit("abnormal").alias("trace_type"))
        self.start_time = df1.select(pl.col("Timestamp").min()).item()
        self.last_normal_time = df1.select(pl.col("Timestamp").max()).item()

    def _prepare_entry_data(self) -> None:
        merged_df: pl.DataFrame = pl.concat([self.normal_df, self.abnormal_df])
        entry_df: pl.DataFrame = merged_df.filter(
            (pl.col("ServiceName") == "loadgenerator")
            & (pl.col("ParentSpanId").is_null() | (pl.col("ParentSpanId") == ""))
        )

        if len(entry_df) == 0:
            logger.error("loadgenerator not found in trace data, using ts-ui-dashboard as fallback")
            entry_df = merged_df.filter(
                (pl.col("ServiceName") == "ts-ui-dashboard")
                & (pl.col("ParentSpanId").is_null() | (pl.col("ParentSpanId") == ""))
            )

        if len(entry_df) == 0:
            logger.error("No valid entrypoint found in trace data")
            self.entry_df = pl.DataFrame()
            return

        entry_df = entry_df.with_columns(
            [
                pl.col("Timestamp").alias("datetime"),
                (pl.col("Duration") / 1e9).alias("duration"),
                pl.struct(["SpanAttributes", "StatusCode"])
                .map_elements(
                    lambda x: self._extract_status_code(x["SpanAttributes"]),
                    return_dtype=pl.Utf8,
                )
                .alias("status_code"),
            ]
        ).sort("Timestamp")

        self.entry_df = entry_df.with_columns(
            pl.col("SpanName").map_elements(self.pedestal.normalize_path, return_dtype=pl.Utf8).alias("api_path")
        )

    def _create_span_visualization(self) -> None:
        problematic_spans = set()
        for record in self.issue_data:
            problematic_spans.add(record.span_name)

        if not problematic_spans:
            logger.info(f"No specific problematic spans found in {self.datapack.name}")
            return

        # Create figure with subplots - 2 columns for each span (latency and status code)
        _, axes = plt.subplots(len(problematic_spans), 2, figsize=(20, 6 * len(problematic_spans)), dpi=300)
        if len(problematic_spans) == 1:
            axes = axes.reshape(1, -1)

        # Plot each problematic span
        for idx, span_name in enumerate(problematic_spans):
            ax_latency = axes[idx, 0]
            ax_status = axes[idx, 1]

            span_data = self.entry_df.filter(pl.col("api_path") == span_name)

            if len(span_data) == 0:
                for ax in [ax_latency, ax_status]:
                    ax.text(
                        0.5,
                        0.5,
                        f"No data found for {span_name}",
                        ha="center",
                        va="center",
                        transform=ax.transAxes,
                    )
                ax_latency.set_title(f"{span_name} - Latency")
                ax_status.set_title(f"{span_name} - Status Code")
                continue

            # Separate normal and abnormal data
            normal_data = span_data.filter(pl.col("trace_type") == "normal")
            abnormal_data = span_data.filter(pl.col("trace_type") == "abnormal")

            normal_count = len(normal_data)
            abnormal_count = len(abnormal_data)

            # Plot latency over time
            if len(normal_data) > 0:
                # Sort normal data by datetime for proper line connection
                normal_sorted = normal_data.sort("datetime")
                normal_times = normal_sorted.select("datetime").to_numpy().flatten()
                normal_latencies = normal_sorted.select("duration").to_numpy().flatten()
                ax_latency.plot(
                    normal_times,
                    normal_latencies,
                    color="green",
                    alpha=0.7,
                    linewidth=1.2,
                    marker="o",
                    markersize=3,
                    label=f"Normal ({normal_count})",
                )

            if len(abnormal_data) > 0:
                # Sort abnormal data by datetime for proper line connection
                abnormal_sorted = abnormal_data.sort("datetime")
                abnormal_times = abnormal_sorted.select("datetime").to_numpy().flatten()
                abnormal_latencies = abnormal_sorted.select("duration").to_numpy().flatten()
                ax_latency.plot(
                    abnormal_times,
                    abnormal_latencies,
                    color="red",
                    alpha=0.7,
                    linewidth=1.2,
                    marker="o",
                    markersize=3,
                    label=f"Abnormal ({abnormal_count})",
                )

            # Add vertical line at last normal time
            ax_latency.axvline(
                x=self.last_normal_time,
                color="blue",
                linestyle="--",
                alpha=0.7,
                label="Last Normal Time",
            )

            ax_latency.set_xlabel("Time")
            ax_latency.set_ylabel("Latency (seconds)")
            ax_latency.set_title(f"{span_name} - Latency\n(Normal: {normal_count}, Abnormal: {abnormal_count})")
            ax_latency.legend()
            ax_latency.grid(True, alpha=0.3)

            # Format x-axis for datetime
            ax_latency.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M:%S"))
            ax_latency.tick_params(axis="x", rotation=45)

            # Plot status codes over time
            # Convert status codes to numeric for plotting
            if len(normal_data) > 0:
                # Use sorted data for consistent time ordering
                normal_sorted = normal_data.sort("datetime")
                normal_times = normal_sorted.select("datetime").to_numpy().flatten()
                normal_status = normal_sorted.select("status_code").to_numpy().flatten()
                normal_status_numeric = [int(s) if s.isdigit() else -1 for s in normal_status]
                ax_status.scatter(
                    normal_times,
                    normal_status_numeric,
                    color="green",
                    alpha=0.6,
                    s=10,
                    label=f"Normal ({normal_count})",
                )

            if len(abnormal_data) > 0:
                # Use sorted data for consistent time ordering
                abnormal_sorted = abnormal_data.sort("datetime")
                abnormal_times = abnormal_sorted.select("datetime").to_numpy().flatten()
                abnormal_status = abnormal_sorted.select("status_code").to_numpy().flatten()
                abnormal_status_numeric = [int(s) if s.isdigit() else -1 for s in abnormal_status]
                ax_status.scatter(
                    abnormal_times,
                    abnormal_status_numeric,
                    color="red",
                    alpha=0.6,
                    s=10,
                    label=f"Abnormal ({abnormal_count})",
                )

            # Add vertical line at last normal time
            ax_status.axvline(
                x=self.last_normal_time,
                color="blue",
                linestyle="--",
                alpha=0.7,
                label="Last Normal Time",
            )

            ax_status.set_xlabel("Time")
            ax_status.set_ylabel("HTTP Status Code")
            ax_status.set_title(f"{span_name} - Status Code\n(Normal: {normal_count}, Abnormal: {abnormal_count})")
            ax_status.legend()
            ax_status.grid(True, alpha=0.3)

            # Format x-axis for datetime
            ax_status.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M:%S"))
            ax_status.tick_params(axis="x", rotation=45)

            # Set y-axis to show common HTTP status codes
            status_codes = [200, 400, 401, 403, 404, 500, 502, 503, 504]
            ax_status.set_yticks(status_codes)

        plt.tight_layout()
        plt.savefig(self.output_file, bbox_inches="tight")
        plt.close()

        logger.info(f"Visualization saved to {self.output_file}")

    def vis_call(self, skip_existing: bool = True) -> None:
        client = get_rcabench_client()
        api = EvaluationsApi(client)
        resp = api.evaluate_algorithm_on_datapacks(
            request=BatchEvaluateDatapackReq(
                specs=[
                    EvaluateDatapackSpec(
                        algorithm=ContainerRef(name=DETECTOR_NAME),
                        datapack=self.datapack.name,
                    )
                ]
            )
        )

        assert resp.code and resp.code < 300 and resp.data is not None, "No detector results found"
        assert resp.data.success_items is not None and len(resp.data.success_items) > 0, (
            "No successful detector results found"
        )
        detector_result = resp.data.success_items[0]

        assert detector_result.execution_refs is not None and len(detector_result.execution_refs) > 0, (
            "No execution references found in detector results"
        )
        execution_refs = sorted(
            detector_result.execution_refs,
            key=lambda x: datetime.fromisoformat(x.executed_at) if x.executed_at else datetime.min,
            reverse=True,
        )
        execution_ref = execution_refs[0]

        data = execution_ref.detector_results

        if data is None or len(data) == 0:
            logger.warning(f"No detector results found for {self.datapack.name}, skipping visualization")
            return

        self.issue_data = [i for i in data if i.issues is not None and i.issues != "{}"]
        if len(self.issue_data) == 0:
            logger.info(f"No issues found in {self.datapack.name}, skipping visualization")
            return

        # Prepare trace data
        self._prepare_trace_data()

        if (
            self.normal_df.is_empty()
            or self.abnormal_df.is_empty()
            or self.start_time is None
            or self.last_normal_time is None
        ):
            logger.error(f"Invalid trace data in {self.datapack.name}, skipping visualization")
            return

        hour_key: str = self.start_time.astimezone(ZoneInfo("Asia/Shanghai")).strftime("%Y-%m-%d_%H")
        final_output_dir: Path = self.output_path / hour_key
        final_output_dir.mkdir(parents=True, exist_ok=True)
        self.output_file: Path = final_output_dir / f"{self.datapack.name}.png"
        if self.output_file.exists() and skip_existing:
            return

        # Prepare entry data
        self._prepare_entry_data()
        if len(self.entry_df) == 0:
            return

        # Create visualization for problematic spans
        self._create_span_visualization()


def batch_visualization(datapacks: list[Path], skip_existing: bool = False) -> None:
    tasks = []
    for datapack_dir in datapacks:
        detector = VisDetector(datapack_dir)
        task = functools.partial(detector.vis_call, skip_existing=skip_existing)
        tasks.append(task)

    cpu = os.cpu_count()
    assert cpu is not None
    if tasks:
        fmap_processpool(tasks, parallel=cpu // 2, cpu_limit_each=2)
