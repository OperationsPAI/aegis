"""Generate metrics_sli.parquet from trace data for sampler algorithms."""

from pathlib import Path

import polars as pl

from ..logging import logger, timeit
from ..utils.serde import save_parquet


@timeit(log_level="INFO")
def generate_metrics_sli(input_folder: Path, output_folder: Path | None = None) -> None:
    """
    Generate metrics_sli.parquet from trace data.

    This function aggregates trace data by service_name, span_name, and minute
    to create SLI metrics that can be used by downstream RCA algorithms.

    Args:
        input_folder: Path to the datapack folder containing trace files
        output_folder: Optional output folder. If None, saves to input_folder
    """
    if output_folder is None:
        output_folder = input_folder

    output_path = output_folder / "metrics_sli.parquet"

    # Skip if already exists
    if output_path.exists():
        logger.debug(f"metrics_sli.parquet already exists at {output_path}")
        return

    logger.info(f"Generating metrics_sli.parquet for {input_folder}")

    # Load trace data
    normal_traces_path = input_folder / "normal_traces.parquet"
    abnormal_traces_path = input_folder / "abnormal_traces.parquet"

    traces_dfs = []

    if normal_traces_path.exists():
        logger.debug("Loading normal_traces.parquet")
        normal_df = pl.read_parquet(normal_traces_path)
        traces_dfs.append(normal_df)

    if abnormal_traces_path.exists():
        logger.debug("Loading abnormal_traces.parquet")
        abnormal_df = pl.read_parquet(abnormal_traces_path)
        traces_dfs.append(abnormal_df)

    if not traces_dfs:
        logger.warning(f"No trace files found in {input_folder}")
        return

    # Combine all trace data
    traces_df = pl.concat(traces_dfs, how="vertical")
    logger.debug(f"Loaded {len(traces_df)} spans from trace files")

    # Convert timestamps to minute precision
    traces_df = traces_df.with_columns(
        [
            # Convert time to minute precision (truncate to minute)
            pl.col("time").dt.truncate("1m").alias("time_minute"),
            # Convert duration to milliseconds (from nanoseconds)
            (pl.col("duration") / 1_000_000).alias("duration_ms"),
            # Determine if span is an error (status_code == "Error" means Error)
            pl.when(pl.col("attr.status_code") == "Error").then(1).otherwise(0).alias("is_error"),
        ]
    )

    # Group by service, span name, and minute
    metrics_df = traces_df.group_by(["time_minute", "service_name", "span_name"]).agg(
        [
            pl.col("duration_ms").min().alias("min_duration"),
            pl.col("duration_ms").max().alias("max_duration"),
            pl.col("duration_ms").mean().alias("avg_duration"),
            pl.col("duration_ms").quantile(0.5).alias("duration_p50"),
            pl.col("duration_ms").quantile(0.9).alias("duration_p90"),
            pl.col("duration_ms").quantile(0.95).alias("duration_p95"),
            pl.col("duration_ms").quantile(0.99).alias("duration_p99"),
            pl.len().alias("total_count"),
            pl.col("is_error").sum().alias("error_count"),
        ]
    )

    # Convert time_minute back to timestamp format
    metrics_df = metrics_df.with_columns([pl.col("time_minute").alias("time")]).drop("time_minute")

    # Reorder columns to match the specification
    metrics_df = metrics_df.select(
        [
            "time",
            "service_name",
            "span_name",
            "min_duration",
            "max_duration",
            "avg_duration",
            "duration_p50",
            "duration_p90",
            "duration_p95",
            "duration_p99",
            "total_count",
            "error_count",
        ]
    )

    logger.info(f"Generated {len(metrics_df)} metric records")
    save_parquet(metrics_df, path=output_path)
    logger.info(f"Saved metrics_sli.parquet to {output_path}")


def copy_metrics_sli_to_sampled(input_folder: Path, sampled_folder: Path) -> None:
    """
    Copy metrics_sli.parquet from input folder to sampled folder.

    This ensures that sampled directories have access to the original
    aggregated metrics for fairness across all samplers.
    """
    source_path = input_folder / "metrics_sli.parquet"
    dest_path = sampled_folder / "metrics_sli.parquet"

    if not source_path.exists():
        logger.warning(f"Source metrics_sli.parquet not found at {source_path}")
        return

    if dest_path.exists():
        logger.debug(f"metrics_sli.parquet already exists at {dest_path}")
        return

    # Create a hard link to avoid duplication
    try:
        dest_path.hardlink_to(source_path)
        logger.debug(f"Linked metrics_sli.parquet from {source_path} to {dest_path}")
    except OSError:
        # If hard link fails, copy the file
        import shutil

        shutil.copy2(source_path, dest_path)
        logger.debug(f"Copied metrics_sli.parquet from {source_path} to {dest_path}")
