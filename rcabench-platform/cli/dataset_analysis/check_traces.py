#!/usr/bin/env -S uv run -s
from dataclasses import dataclass
from datetime import datetime

import matplotlib.dates as mdates
import matplotlib.pyplot as plt
import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.clients.clickhouse import (
    get_clickhouse_client,
    query_parquet_stream,
)
from rcabench_platform.v3.sdk.config import get_config
from rcabench_platform.v3.sdk.datasets.spec import (
    get_datapack_folder,
    get_dataset_meta_file,
)
from rcabench_platform.v3.sdk.pedestals import get_pedestal


@app.command()
@timeit()
def check_loadgenerator(system: str = "ts", limit: int = 10000, parallel: int = 16):
    assert limit > 0

    temp = get_config().temp

    logger.info("Checking loadgenerator traces")

    with get_clickhouse_client() as client:
        query = """
        WITH target_traces AS (
            SELECT      TraceId
            FROM        otel_traces
            WHERE       ServiceName = 'loadgenerator'
                      AND ParentSpanId = ''
                      AND Timestamp > toDateTime('2025-07-16 00:00:00')
            ORDER BY    Timestamp DESC
        )
        SELECT      TraceId, SpanName, Duration, Timestamp
        FROM        otel_traces
        WHERE       TraceId IN (SELECT TraceId FROM target_traces)
        ORDER BY    Timestamp ASC
        """
        save_path = temp / "loadgenerator_traces_all.parquet"
        query_parquet_stream(client, query, save_path)

    df = pl.read_parquet(save_path)
    assert len(df) > 0, "No traces found"

    trace_id_list = df["TraceId"].unique().to_list()

    logger.info(f"Found {len(trace_id_list)} unique traces")

    df = df.with_columns(get_pedestal(system).normalize_op_name(pl.col("SpanName")).alias("SpanName"))


@dataclass(kw_only=True, slots=True)
class DatapackCheck:
    datapack: str
    normal_start: datetime
    normal_end: datetime
    interference_start: datetime
    interference_end: datetime


@app.command()
@timeit()
def concat_normal_ranges():
    lf = pl.scan_parquet(get_dataset_meta_file("rcabench", "attributes.parquet"))

    lf = lf.filter(
        pl.col("inject_time") >= pl.datetime(2025, 7, 7, 0, time_zone="UTC"),
        pl.col("inject_time") <= pl.datetime(2025, 7, 7, 12, time_zone="UTC"),
    )

    df = lf.select("datapack").collect()
    datapacks = df["datapack"].unique().to_list()

    logger.info("found {} datapacks", len(datapacks))

    lf_list: list[pl.LazyFrame] = []
    for datapack in datapacks:
        folder = get_datapack_folder("rcabench", datapack)
        file_path = folder / "normal_traces.parquet"
        if file_path.exists():
            lf = pl.scan_parquet(file_path)
            lf = lf.select("time", "duration")
            lf = lf.group_by_dynamic("time", every="1s").agg(pl.col("duration").max())
            lf_list.append(lf)

    df = pl.concat(lf_list).collect()

    # Convert duration from nanoseconds to seconds
    df = df.with_columns(pl.col("duration") / 1e9)

    # Sort by time for plotting
    df = df.sort("time")

    # Create the plot
    temp = get_config().temp
    plt.figure(figsize=(15, 8))

    # Convert to pandas for easier plotting with matplotlib
    df_pandas = df.to_pandas()

    # Convert UTC timestamps to Beijing time (UTC+8) for display
    df_pandas["time"] = df_pandas["time"].dt.tz_convert("Asia/Shanghai")

    del df
    # Plot duration over time
    plt.plot(df_pandas["time"], df_pandas["duration"], linewidth=0.8, alpha=0.7)

    # Format the plot
    plt.title(
        "Maximum Duration Over Time (1-second aggregation) - Beijing Time (UTC+8)",
        fontsize=14,
        fontweight="bold",
    )
    plt.xlabel("Time (Beijing Time)", fontsize=12)
    plt.ylabel("Duration (seconds)", fontsize=12)
    plt.yscale("log")  # Set y-axis to logarithmic scale
    plt.grid(True, alpha=0.3)

    # Format x-axis to show dates nicely
    plt.gca().xaxis.set_major_formatter(mdates.DateFormatter("%m-%d %H:%M"))
    plt.gca().xaxis.set_major_locator(mdates.HourLocator(interval=6))
    plt.xticks(rotation=45)

    # Adjust layout to prevent label cutoff
    plt.tight_layout()

    # Save the plot
    output_path = temp / "normal_ranges_duration_timeline.png"
    plt.savefig(output_path, dpi=300, bbox_inches="tight")
    logger.info(f"Saved duration timeline plot to {output_path}")

    plt.close()


@app.command()
def vis_all(start_time: str = "2025-07-18 11:00:00"):
    visualize_span_latency("ts0", start_time)
    visualize_span_latency("ts1", start_time)
    visualize_span_latency("ts2", start_time)
    visualize_span_latency("ts3", start_time)


@app.command()
def visualize_span_latency(ns: str, system: str = "ts", start_time: str = "2025-07-18 11:00:00"):
    temp = get_config().temp

    logger.info("Starting span latency visualization")

    save_path = temp / "loadgenerator_traces_all.parquet"

    with get_clickhouse_client() as client:
        query = f"""
        SELECT      TraceId, SpanName, Duration, Timestamp
        FROM        otel_traces
        WHERE       ServiceName = 'loadgenerator'
        AND ParentSpanId = ''
        AND Timestamp > toDateTime('{start_time}')
        AND ResourceAttributes['service.namespace'] = '{ns}'
        ORDER BY    Timestamp
        """
        query_parquet_stream(client, query, save_path)

    df = pl.read_parquet(save_path)
    assert len(df) > 0, "No traces found"

    logger.info(f"Loaded {len(df)} trace records")

    if len(df) > 0:
        beijing_timestamps = df["Timestamp"].dt.convert_time_zone("Asia/Shanghai")
        min_time_beijing = beijing_timestamps.min()
        max_time_beijing = beijing_timestamps.max()
        logger.info(f"Time range (Beijing Time): {min_time_beijing} to {max_time_beijing}")

    pedestal = get_pedestal(system)

    df = df.with_columns(
        [
            pedestal.normalize_op_name(pl.col("SpanName")).alias("SpanName"),
            (pl.col("Timestamp")).alias("datetime"),
            (pl.col("Duration") / 1e9).alias("duration_seconds"),
        ]
    )

    span_names = df["SpanName"].unique().to_list()
    logger.info(f"Found {len(span_names)} unique span names")

    valid_spans = []

    for span_name in span_names:
        span_df = df.filter(pl.col("SpanName") == span_name)

        if len(span_df) < 10:
            continue

        span_data = span_df.select(["datetime", "duration_seconds"]).sort("datetime")

        if len(span_data) < 2:
            continue

        plot_data = span_data.to_pandas()
        valid_spans.append((span_name, plot_data))

    if not valid_spans:
        logger.warning("No valid spans found for plotting")
        return

    logger.info(f"Found {len(valid_spans)} valid spans for plotting")

    fig, axes = plt.subplots(len(valid_spans), 1, figsize=(15, 6 * len(valid_spans)), sharex=True)

    if len(valid_spans) == 1:
        axes = [axes]

    all_times = []
    for _, plot_data in valid_spans:
        all_times.extend(
            [
                plot_data["datetime"].min(),
                plot_data["datetime"].max(),
            ]
        )

    if all_times:
        time_span_hours = (max(all_times) - min(all_times)).total_seconds() / 3600

        # Determine interval based on time span
        if time_span_hours < 1:
            interval_minutes = 2
        elif time_span_hours <= 6:
            interval_minutes = 5
        elif time_span_hours <= 12:
            interval_minutes = 10
        elif time_span_hours <= 24:
            interval_minutes = 20
        else:
            interval_minutes = 30

        logger.info(f"Time span: {time_span_hours:.2f} hours, using {interval_minutes} minute intervals")
    else:
        interval_minutes = 2  # Default fallback

    for i, (span_name, plot_data) in enumerate(valid_spans):
        ax = axes[i]

        ax.plot(
            plot_data["datetime"],
            plot_data["duration_seconds"],
            color="blue",
            linewidth=0.8,
            marker="o",
            markersize=1,
            alpha=0.7,
        )

        ax.set_ylabel("Duration (seconds)", fontsize=12)
        ax.set_title(f"Request Latency - {span_name}", fontsize=14, fontweight="bold")
        ax.grid(True, alpha=0.3)

    axes[-1].set_xlabel("Time", fontsize=12)
    axes[-1].xaxis.set_major_formatter(mdates.DateFormatter("%H:%M", tz="Asia/Shanghai"))
    axes[-1].xaxis.set_major_locator(mdates.MinuteLocator(interval=interval_minutes))

    plt.setp(axes[-1].xaxis.get_majorticklabels(), rotation=45)

    plt.tight_layout()

    output_file = temp / "vis_trace_all"
    output_file.mkdir(exist_ok=True, parents=True)
    plt.savefig(output_file / f"{ns}_span_latency_timeseries.png", dpi=300, bbox_inches="tight")
    plt.close()

    logger.info(f"Saved individual request latency plot with {len(valid_spans)} spans to {output_file}")


if __name__ == "__main__":
    app()
