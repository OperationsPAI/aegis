"""Single sampler execution module."""

import dataclasses
import json
import math
import os
import time
import traceback
from pathlib import Path

import polars as pl

from ...datasets.spec import get_datapack_folder
from ...logging import logger, timeit
from ...pedestals import get_pedestal
from ...utils.fs import running_mark
from ...utils.serde import save_parquet
from ..event_encoding import calculate_event_coverage
from ..metrics_sli import copy_metrics_sli_to_sampled, generate_metrics_sli
from ..path_encoding import calculate_path_coverage, calculate_path_coverage_dedup
from ..spec import SamplerArgs, SamplingMode, global_sampler_registry
from .spec import get_sampler_output_folder


@timeit(log_level="INFO")
def run_sampler_single(
    sampler: str,
    dataset: str,
    datapack: str,
    sampling_rate: float,
    mode: SamplingMode,
    *,
    clear: bool = False,
    skip_finished: bool = True,
):
    """
    Run a single sampler on a datapack.

    Args:
        sampler: Name of the sampler algorithm
        dataset: Dataset name
        datapack: Datapack name
        sampling_rate: Sampling rate (0.0 to 1.0)
        mode: Sampling mode (online/offline)
        clear: Whether to clear existing output
        skip_finished: Whether to skip if already finished
    """
    sampler_instance = global_sampler_registry()[sampler]()

    input_folder = get_datapack_folder(dataset, datapack)
    output_folder = get_sampler_output_folder(dataset, datapack, sampler, sampling_rate, mode)
    # Generate metrics_sli.parquet in the original input folder if it doesn't exist
    logger.debug("Ensuring metrics_sli.parquet exists in input folder")
    generate_metrics_sli(input_folder, output_folder=input_folder)

    with running_mark(output_folder, clear=clear):
        finished = output_folder / ".finished"
        if skip_finished and finished.exists():
            logger.debug(f"skipping {output_folder}")
            return

        try:
            t0 = time.time()
            sample_results = sampler_instance(
                SamplerArgs(
                    dataset=dataset,
                    datapack=datapack,
                    input_folder=input_folder,
                    output_folder=output_folder,
                    sampling_rate=sampling_rate,
                    mode=mode,
                )
            )
            t1 = time.time()
            runtime = t1 - t0
        except Exception as e:
            traceback.print_exc()
            logger.error(f"Error in {sampler} for {dataset}/{datapack}: {repr(e)}")
            sample_results = []
            runtime = None

    # Convert results to dataframe
    if len(sample_results) == 0:
        # Create empty result
        results_df = pl.DataFrame(schema={"trace_id": pl.String, "sample_score": pl.Float64})
    else:
        results_data = [dataclasses.asdict(result) for result in sample_results]
        results_df = pl.DataFrame(results_data)

    # Calculate performance metrics
    perf_metrics = calculate_sampler_performance(input_folder, results_df, sampling_rate, mode, runtime, dataset)

    # Add metadata to results
    output_df = results_df.with_columns(
        pl.lit(sampler).alias("sampler"),
        pl.lit(dataset).alias("dataset"),
        pl.lit(datapack).alias("datapack"),
        pl.lit(sampling_rate).alias("sampling_rate"),
        pl.lit(mode.value).alias("mode"),
        pl.lit(runtime, dtype=pl.Float64).alias("runtime.seconds"),
    )

    # Save results
    mode_filename = f"{mode.value}.parquet"
    save_parquet(output_df, path=output_folder / mode_filename)

    # Save performance metrics
    perf_df = pl.DataFrame([perf_metrics])
    save_parquet(perf_df, path=output_folder / "perf.parquet")

    # Save sampled trace files for downstream RCA algorithms
    _save_sampled_traces(input_folder, output_folder, results_df)

    finished.touch()

    logger.info(f"Sampler {sampler} completed for {dataset}/{datapack}")
    logger.info(f"Sampled {len(results_df)} traces in {runtime:.3f}s")
    logger.info(f"Performance metrics: {perf_metrics}")


def calculate_sampler_performance(
    input_folder: Path,
    sampled_df: pl.DataFrame,
    sampling_rate: float,
    mode: SamplingMode,
    runtime: float | None,
    dataset: str,
) -> dict:
    """
    Calculate performance metrics for sampler.

    Returns:
        Dictionary containing performance metrics:
        - controllability (RoD): Rate of Deviation
        - comprehensiveness (CR): API Coverage Rate based on API types (entry spans)
        - path_coverage: Coverage Rate based on execution paths with BFS encoding
        - path_coverage_dedup: Coverage Rate based on deduplicated execution paths (removes parallel spans)
        - event_coverage: Coverage Rate based on event pairs from traces+logs
        - unique_trace_coverage: Coverage Rate based on unique trace patterns (sets of event pairs)
        - gt_trace_proportion: Proportion of ground truth related traces in sampled data
        - proportion_anomaly (PRO_anomaly): Proportion of detector flagged spans in abnormal traces
        - proportion_rare (PRO_rare): Proportion of rare traces (< 5% frequency)
        - proportion_common (PRO_common): Proportion of common spans (including detector spans in normal traces)
        - actual_sampling_rate: Actual sampling rate achieved
        - runtime_per_span_ms: Runtime per span in milliseconds
        - runtime_per_trace_ms: Runtime per trace in milliseconds
        - total_path_types: Total number of unique execution paths
        - sampled_path_types: Number of unique execution paths in sampled data
        - total_path_types_dedup: Total number of unique deduplicated execution paths
        - sampled_path_types_dedup: Number of unique deduplicated execution paths in sampled data
        - total_event_pairs: Total number of unique event pairs
        - sampled_event_pairs: Number of unique event pairs in sampled data
        - total_unique_traces: Total number of unique trace patterns
        - sampled_unique_traces: Number of unique trace patterns in sampled data
        - shannon_entropy: Shannon entropy of trace pattern distribution in sampled data
        - benefit_cost_ratio: Ratio of unique trace patterns discovered to actual sample count
        - intra_sample_dissimilarity: Average dissimilarity between sampled traces (diversity metric)
        - avg_anomaly_score: Average anomaly score per trace (error spans + performance + log score)
    """
    # Check if this is a normal-only dataset (like TracePicker)
    normal_traces_lf = pl.scan_parquet(input_folder / "normal_traces.parquet")
    abnormal_traces_path = input_folder / "abnormal_traces.parquet"

    is_normal_only = False
    if abnormal_traces_path.exists():
        abnormal_count = pl.scan_parquet(abnormal_traces_path).select(pl.len()).collect().item()
        is_normal_only = abnormal_count == 0
    else:
        is_normal_only = True

    if is_normal_only:
        logger.info("Detected normal-only dataset, adapting metrics calculation")
        return _calculate_normal_only_performance(input_folder, sampled_df, sampling_rate, mode, runtime, dataset)

    # Original logic for datasets with both normal and abnormal data
    # Load traces to get total counts
    abnormal_traces_lf = pl.scan_parquet(abnormal_traces_path)

    # Get total unique traces and total spans
    all_traces_lf = pl.concat([normal_traces_lf.select("trace_id"), abnormal_traces_lf.select("trace_id")]).unique()
    all_spans_lf = pl.concat([normal_traces_lf.select("span_id"), abnormal_traces_lf.select("span_id")])

    total_traces = all_traces_lf.select(pl.len()).collect().item()
    total_spans = all_spans_lf.select(pl.len()).collect().item()
    sampled_count = len(sampled_df)

    # Calculate actual sampling rate
    actual_sampling_rate = sampled_count / total_traces if total_traces > 0 else 0.0

    # Calculate controllability (RoD)
    expected_count = int(total_traces * sampling_rate)
    controllability = abs((sampled_count - expected_count) / expected_count) if expected_count > 0 else 0.0

    pedestal = get_pedestal()

    if sampled_count == 0:
        # Still need to calculate total metrics even when no samples
        # Load full traces for total metrics calculation
        combined_traces_lf = pl.concat(
            [
                normal_traces_lf.with_columns(pl.lit(False).alias("is_abnormal")),
                abnormal_traces_lf.with_columns(pl.lit(True).alias("is_abnormal")),
            ]
        )

        # Calculate total entry types for comprehensiveness baseline
        entry_traces_lf = combined_traces_lf.filter(
            pl.col("parent_span_id").is_null() | (pl.col("parent_span_id") == "")
        )
        loadgen_entries_lf = entry_traces_lf.filter(pl.col("service_name") == "loadgenerator")
        loadgen_count = loadgen_entries_lf.select(pl.len()).collect().item()

        if loadgen_count > 0:
            selected_entries_lf = loadgen_entries_lf
        else:
            ui_entries_lf = entry_traces_lf.filter(pl.col("service_name") == "ts-ui-dashboard")
            ui_count = ui_entries_lf.select(pl.len()).collect().item()
            if ui_count > 0:
                selected_entries_lf = ui_entries_lf
            else:
                selected_entries_lf = entry_traces_lf

        traces_with_entry_lf = selected_entries_lf.with_columns(
            pl.col("span_name").map_elements(pedestal.normalize_path, return_dtype=pl.String).alias("entry_span")
        )
        trace_entries_lf = traces_with_entry_lf.group_by("trace_id").agg(
            [
                pl.first("entry_span").alias("entry_span"),
                pl.first("is_abnormal").alias("is_abnormal"),
            ]
        )
        trace_entries_df = trace_entries_lf.collect()
        entry_span_counts = trace_entries_df.group_by("entry_span").agg(pl.len().alias("count"))
        total_entry_types = entry_span_counts.shape[0]

        # Calculate total path types
        all_traces_df = pl.concat(
            [
                normal_traces_lf.select(
                    [
                        "trace_id",
                        "span_id",
                        "parent_span_id",
                        "service_name",
                        "span_name",
                    ]
                ),
                abnormal_traces_lf.select(
                    [
                        "trace_id",
                        "span_id",
                        "parent_span_id",
                        "service_name",
                        "span_name",
                    ]
                ),
            ]
        ).collect()
        path_coverage_metrics = calculate_path_coverage(all_traces_df, set(), dataset)  # Empty sampled set

        # Calculate total deduplicated path types (removing parallel spans at same level)
        path_coverage_dedup_metrics = calculate_path_coverage_dedup(all_traces_df, set(), dataset)  # Empty sampled set

        # Calculate total event pairs
        all_traces_for_events_df = pl.concat(
            [
                normal_traces_lf.select(
                    [
                        "trace_id",
                        "span_id",
                        "parent_span_id",
                        "service_name",
                        "span_name",
                        "time",
                        "duration",
                        "attr.status_code",
                    ]
                ),
                abnormal_traces_lf.select(
                    [
                        "trace_id",
                        "span_id",
                        "parent_span_id",
                        "service_name",
                        "span_name",
                        "time",
                        "duration",
                        "attr.status_code",
                    ]
                ),
            ]
        ).collect()

        logs_for_events_df = None
        normal_logs_path = input_folder / "normal_logs.parquet"
        abnormal_logs_path = input_folder / "abnormal_logs.parquet"
        if normal_logs_path.exists() and abnormal_logs_path.exists():
            normal_logs_lf = pl.scan_parquet(normal_logs_path)
            abnormal_logs_lf = pl.scan_parquet(abnormal_logs_path)
            logs_for_events_df = pl.concat(
                [
                    normal_logs_lf.select(
                        [
                            "trace_id",
                            "span_id",
                            "service_name",
                            "time",
                            "attr.template_id",
                        ]
                    ),
                    abnormal_logs_lf.select(
                        [
                            "trace_id",
                            "span_id",
                            "service_name",
                            "time",
                            "attr.template_id",
                        ]
                    ),
                ]
            ).collect()

        event_coverage_metrics = calculate_event_coverage(
            all_traces_for_events_df,
            logs_for_events_df,
            set(),
            input_folder,  # Empty sampled set
            dataset,
        )

        # Calculate ground truth trace proportion (will be 0.0 for empty sample)
        gt_trace_proportion = calculate_gt_trace_proportion(input_folder, all_traces_df, set(), set())

        # Calculate span statistics for empty sample
        total_span_count = len(all_traces_for_events_df)
        total_unique_span_names = len(
            all_traces_for_events_df.select(
                pl.concat_str(["service_name", "span_name"], separator="_").alias("span_name")
            ).unique()
        )

        return {
            "sampled_count": sampled_count,
            "total_traces": total_traces,
            "total_entry_types": total_entry_types,
            "sampled_entry_types": 0,
            "controllability": controllability,
            "comprehensiveness": 0.0,
            "proportion_anomaly": 0.0,
            "proportion_rare": 0.0,
            "proportion_common": 0.0,
            "actual_sampling_rate": actual_sampling_rate,
            "runtime_per_span_ms": runtime * 1e3 / total_spans if runtime and total_spans > 0 else 0.0,
            "runtime_per_trace_ms": runtime * 1e3 / total_traces if runtime and total_traces > 0 else 0.0,
            "gt_trace_proportion": gt_trace_proportion,  # New ground truth trace proportion metric
            "balance_cv": 0.0,  # Balance CV is 0.0 when no traces are sampled
            # Path coverage metrics
            "total_path_types": path_coverage_metrics["total_path_types"],
            "sampled_path_types": 0,
            "path_coverage": 0.0,
            # Deduplicated path coverage metrics (removes parallel spans at same level)
            "total_path_types_dedup": path_coverage_dedup_metrics["total_path_types_dedup"],
            "sampled_path_types_dedup": 0,
            "path_coverage_dedup": 0.0,
            # Event coverage metrics
            "total_event_pairs": event_coverage_metrics["total_event_pairs"],
            "sampled_event_pairs": 0,
            "event_coverage": 0.0,
            "total_unique_traces": event_coverage_metrics["total_unique_traces"],
            "sampled_unique_traces": 0,
            "unique_trace_coverage": 0.0,
            # Span count metrics
            "total_span_count": total_span_count,
            "sampled_span_count": 0,
            "total_unique_span_names": total_unique_span_names,
            "sampled_unique_span_names": 0,
            "span_coverage": 0.0,
            # New metrics
            "shannon_entropy": 0.0,
            "benefit_cost_ratio": 0.0,
            "intra_sample_dissimilarity": 0.0,
            "avg_anomaly_score": 0.0,
        }

    # Load full traces with parsed span names for analysis
    combined_traces_lf = pl.concat(
        [
            normal_traces_lf.with_columns(pl.lit(False).alias("is_abnormal")),
            abnormal_traces_lf.with_columns(pl.lit(True).alias("is_abnormal")),
        ]
    )

    # Select entry spans using the same logic as detector:
    # 1. First try loadgenerator service with null/empty parent_span_id
    # 2. Fallback to ts-ui-dashboard with null/empty parent_span_id
    # 3. Finally try any service with null/empty parent_span_id
    entry_traces_lf = combined_traces_lf.filter(pl.col("parent_span_id").is_null() | (pl.col("parent_span_id") == ""))

    # Try loadgenerator first
    loadgen_entries_lf = entry_traces_lf.filter(pl.col("service_name") == "loadgenerator")
    loadgen_count = loadgen_entries_lf.select(pl.len()).collect().item()

    if loadgen_count > 0:
        selected_entries_lf = loadgen_entries_lf
    else:
        # Fallback to ts-ui-dashboard
        ui_entries_lf = entry_traces_lf.filter(pl.col("service_name") == "ts-ui-dashboard")
        ui_count = ui_entries_lf.select(pl.len()).collect().item()

        if ui_count > 0:
            selected_entries_lf = ui_entries_lf
        else:
            # Use all root spans from any service
            selected_entries_lf = entry_traces_lf

    # Add parsed span names and get unique entry spans per trace
    traces_with_entry_lf = selected_entries_lf.with_columns(
        pl.col("span_name").map_elements(pedestal.normalize_path, return_dtype=pl.String).alias("entry_span")
    )

    # Get one entry span per trace (first occurrence)
    trace_entries_lf = traces_with_entry_lf.group_by("trace_id").agg(
        [
            pl.first("entry_span").alias("entry_span"),
            pl.first("is_abnormal").alias("is_abnormal"),
        ]
    )

    trace_entries_df = trace_entries_lf.collect()

    # Get entry span distribution for rare span calculation
    entry_span_counts = (
        trace_entries_df.group_by("entry_span")
        .agg(pl.len().alias("count"))
        .with_columns((pl.col("count") / total_traces).alias("proportion"))
    )

    rare_threshold = 0.05  # 5% threshold for rare spans
    rare_spans = entry_span_counts.filter(pl.col("proportion") < rare_threshold)["entry_span"].to_list()

    # Load detector conclusions if available
    detector_spans = set()
    conclusion_path = input_folder / "conclusion.parquet"
    if conclusion_path.exists():
        detector_df = pl.read_parquet(conclusion_path)
        if len(detector_df) > 0 and "SpanName" in detector_df.columns and "Issues" in detector_df.columns:
            # Only include spans that have actual issues (Issues column is not empty or "{}")
            detector_spans_df = detector_df.filter(
                (pl.col("Issues").is_not_null()) & (pl.col("Issues") != "{}") & (pl.col("Issues") != "")
            )
            detector_spans = set(detector_spans_df["SpanName"].to_list())

    # Join sampled traces with entry information
    sampled_trace_ids = set(sampled_df["trace_id"].to_list())
    sampled_traces_info = trace_entries_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))

    # Calculate metrics
    total_entry_types = entry_span_counts.shape[0]
    sampled_entry_types = len(sampled_traces_info["entry_span"].unique())
    comprehensiveness = sampled_entry_types / total_entry_types if total_entry_types > 0 else 0.0

    # Calculate proportions
    # PRO_anomaly: detector flagged spans in abnormal traces only
    abnormal_sampled = sampled_traces_info.filter(pl.col("is_abnormal"))
    abnormal_detector_sampled = abnormal_sampled.filter(pl.col("entry_span").is_in(list(detector_spans)))
    proportion_anomaly = len(abnormal_detector_sampled) / sampled_count if sampled_count > 0 else 0.0

    # PRO_rare: rare spans (< 5% frequency) in all sampled traces
    rare_sampled = sampled_traces_info.filter(pl.col("entry_span").is_in(rare_spans))
    proportion_rare = len(rare_sampled) / sampled_count if sampled_count > 0 else 0.0

    abnormal_detector_sampled = abnormal_detector_sampled["trace_id"].unique().to_list()
    # common sampled = total sampled id - set(rare sampled id & abnormal detector sampled id)
    common_sampled_count = len(
        set(sampled_traces_info["trace_id"].to_list())
        - set(rare_sampled["trace_id"].to_list())
        - set(abnormal_detector_sampled)
    )
    proportion_common = common_sampled_count / sampled_count if sampled_count > 0 else 0.0

    # Calculate path coverage based on execution paths
    # Load full trace data for path encoding
    all_traces_df = pl.concat(
        [
            normal_traces_lf.select(["trace_id", "span_id", "parent_span_id", "service_name", "span_name"]),
            abnormal_traces_lf.select(["trace_id", "span_id", "parent_span_id", "service_name", "span_name"]),
        ]
    ).collect()

    # Always calculate path coverage to get total_path_types, even when no traces sampled
    path_coverage_metrics = calculate_path_coverage(all_traces_df, sampled_trace_ids, dataset)

    # Calculate deduplicated path coverage (removes parallel spans at same level)
    path_coverage_dedup_metrics = calculate_path_coverage_dedup(all_traces_df, sampled_trace_ids, dataset)

    # If no traces sampled, override sampled metrics but keep total metrics
    if sampled_count == 0:
        path_coverage_metrics["sampled_path_types"] = 0
        path_coverage_metrics["path_coverage"] = 0.0
        path_coverage_dedup_metrics["sampled_path_types_dedup"] = 0
        path_coverage_dedup_metrics["path_coverage_dedup"] = 0.0

    # Calculate event coverage based on trace+log events
    # Load full trace and log data for event encoding
    all_traces_for_events_df = pl.concat(
        [
            normal_traces_lf.select(
                [
                    "trace_id",
                    "span_id",
                    "parent_span_id",
                    "service_name",
                    "span_name",
                    "time",
                    "duration",
                    "attr.status_code",
                ]
            ),
            abnormal_traces_lf.select(
                [
                    "trace_id",
                    "span_id",
                    "parent_span_id",
                    "service_name",
                    "span_name",
                    "time",
                    "duration",
                    "attr.status_code",
                ]
            ),
        ]
    ).collect()

    # Load logs if available
    logs_for_events_df = None
    normal_logs_path = input_folder / "normal_logs.parquet"
    abnormal_logs_path = input_folder / "abnormal_logs.parquet"
    if normal_logs_path.exists() and abnormal_logs_path.exists():
        normal_logs_lf = pl.scan_parquet(normal_logs_path)
        abnormal_logs_lf = pl.scan_parquet(abnormal_logs_path)
        logs_for_events_df = pl.concat(
            [
                normal_logs_lf.select(["trace_id", "span_id", "service_name", "time", "attr.template_id"]),
                abnormal_logs_lf.select(["trace_id", "span_id", "service_name", "time", "attr.template_id"]),
            ]
        ).collect()

    # Always calculate event coverage to get total_event_pairs, even when no traces sampled
    event_coverage_metrics = calculate_event_coverage(
        all_traces_for_events_df,
        logs_for_events_df,
        sampled_trace_ids,
        input_folder,
        dataset,
    )

    # Calculate span statistics
    total_span_count = len(all_traces_for_events_df)
    sampled_span_count = len(all_traces_for_events_df.filter(pl.col("trace_id").is_in(sampled_trace_ids)))

    # Calculate span coverage based on unique span names (service_name + span_name)
    all_unique_spans = all_traces_for_events_df.select(
        pl.concat_str(["service_name", "span_name"], separator="_").alias("span_name")
    ).unique()
    total_unique_span_names = len(all_unique_spans)

    sampled_unique_spans = (
        all_traces_for_events_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))
        .select(pl.concat_str(["service_name", "span_name"], separator="_").alias("span_name"))
        .unique()
    )
    sampled_unique_span_names = len(sampled_unique_spans)

    span_coverage = sampled_unique_span_names / total_unique_span_names if total_unique_span_names > 0 else 0.0

    # If no traces sampled, override sampled metrics but keep total metrics
    if sampled_count == 0:
        event_coverage_metrics["sampled_event_pairs"] = 0
        event_coverage_metrics["event_coverage"] = 0.0
        event_coverage_metrics["sampled_unique_traces"] = 0
        event_coverage_metrics["unique_trace_coverage"] = 0.0
        sampled_span_count = 0
        sampled_unique_span_names = 0
        span_coverage = 0.0

    # Calculate ground truth trace proportion
    abnormal_trace_ids = set(abnormal_traces_lf.select("trace_id").collect()["trace_id"].to_list())
    gt_trace_proportion = calculate_gt_trace_proportion(
        input_folder, all_traces_df, sampled_trace_ids, abnormal_trace_ids
    )

    # Calculate balance (CV) using trace entries
    balance_cv = calculate_balance_cv(trace_entries_df, sampled_trace_ids)

    return {
        "sampled_count": sampled_count,
        "total_traces": total_traces,
        "total_entry_types": total_entry_types,
        "sampled_entry_types": sampled_entry_types,
        "controllability": controllability,
        "comprehensiveness": comprehensiveness,
        "proportion_anomaly": proportion_anomaly,
        "proportion_rare": proportion_rare,
        "proportion_common": proportion_common,
        "actual_sampling_rate": actual_sampling_rate,
        "runtime_per_span_ms": runtime * 1e3 / total_spans if runtime and total_spans > 0 else 0.0,
        "runtime_per_trace_ms": runtime * 1e3 / total_traces if runtime and total_traces > 0 else 0.0,
        "gt_trace_proportion": gt_trace_proportion,  # New ground truth trace proportion metric
        "balance_cv": balance_cv,  # Balance metric using Coefficient of Variation
        # Path coverage metrics
        "total_path_types": path_coverage_metrics["total_path_types"],
        "sampled_path_types": path_coverage_metrics["sampled_path_types"],
        "path_coverage": path_coverage_metrics["path_coverage"],
        # Deduplicated path coverage metrics (removes parallel spans at same level)
        "total_path_types_dedup": path_coverage_dedup_metrics["total_path_types_dedup"],
        "sampled_path_types_dedup": path_coverage_dedup_metrics["sampled_path_types_dedup"],
        "path_coverage_dedup": path_coverage_dedup_metrics["path_coverage_dedup"],
        # Event coverage metrics (now includes Shannon entropy and benefit-cost ratio)
        "total_event_pairs": event_coverage_metrics["total_event_pairs"],
        "sampled_event_pairs": event_coverage_metrics["sampled_event_pairs"],
        "event_coverage": event_coverage_metrics["event_coverage"],
        "total_unique_traces": event_coverage_metrics["total_unique_traces"],
        "sampled_unique_traces": event_coverage_metrics["sampled_unique_traces"],
        "unique_trace_coverage": event_coverage_metrics["unique_trace_coverage"],
        "shannon_entropy": event_coverage_metrics["shannon_entropy"],
        "benefit_cost_ratio": event_coverage_metrics["benefit_cost_ratio"],
        "intra_sample_dissimilarity": event_coverage_metrics["intra_sample_dissimilarity"],
        "avg_anomaly_score": event_coverage_metrics["avg_anomaly_score"],
        # Span count metrics
        "total_span_count": total_span_count,
        "sampled_span_count": sampled_span_count,
        "total_unique_span_names": total_unique_span_names,
        "sampled_unique_span_names": sampled_unique_span_names,
        "span_coverage": span_coverage,
    }


def _save_sampled_traces(input_folder: Path, output_folder: Path, sampled_df: pl.DataFrame) -> None:
    """
    Save sampled trace files and create links to other files for downstream RCA algorithms.

    Args:
        input_folder: Original datapack folder
        output_folder: Sampler output folder
        sampled_df: DataFrame containing sampled trace IDs
    """
    if len(sampled_df) == 0:
        logger.warning("No traces to save - sampled dataframe is empty")
        return

    sampled_trace_ids = set(sampled_df["trace_id"].to_list())

    # Load original trace files
    normal_traces_path = input_folder / "normal_traces.parquet"
    abnormal_traces_path = input_folder / "abnormal_traces.parquet"

    if normal_traces_path.exists():
        normal_traces_df = pl.read_parquet(normal_traces_path)
        sampled_normal_df = normal_traces_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))
        if len(sampled_normal_df) > 0:
            save_parquet(sampled_normal_df, path=output_folder / "normal_traces.parquet")
            unique_traces = sampled_normal_df["trace_id"].n_unique()
            logger.info(f"Saved {len(sampled_normal_df)} normal spans from {unique_traces} unique traces")
        else:
            # Create empty parquet file with correct schema
            empty_df = pl.DataFrame(schema=normal_traces_df.schema)
            save_parquet(empty_df, path=output_folder / "normal_traces.parquet")
            logger.info("Saved empty normal_traces.parquet")

    if abnormal_traces_path.exists():
        abnormal_traces_df = pl.read_parquet(abnormal_traces_path)
        sampled_abnormal_df = abnormal_traces_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))
        if len(sampled_abnormal_df) > 0:
            save_parquet(sampled_abnormal_df, path=output_folder / "abnormal_traces.parquet")
            unique_traces = sampled_abnormal_df["trace_id"].n_unique()
            logger.info(f"Saved {len(sampled_abnormal_df)} abnormal spans from {unique_traces} unique traces")
        else:
            # Create empty parquet file with correct schema
            empty_df = pl.DataFrame(schema=abnormal_traces_df.schema)
            save_parquet(empty_df, path=output_folder / "abnormal_traces.parquet")
            logger.info("Saved empty abnormal_traces.parquet")

    # Create symlinks/hardlinks for specific required files
    required_files = [
        # JSON files
        "injection.json",
        "k8s.json",
        "env.json",
        # Metrics files
        "normal_metrics.parquet",
        "abnormal_metrics.parquet",
        "normal_metrics_sum.parquet",
        "abnormal_metrics_sum.parquet",
        "normal_metrics_histogram.parquet",
        "abnormal_metrics_histogram.parquet",
        # Logs files
        "normal_logs.parquet",
        "abnormal_logs.parquet",
        # Detector conclusion
        "conclusion.parquet",
    ]

    for filename in required_files:
        source_path = input_folder / filename
        target_path = output_folder / filename

        if source_path.exists() and not target_path.exists():
            try:
                # Try to create a hard link first (more efficient)
                target_path.hardlink_to(source_path)
                logger.debug(f"Created hard link: {filename}")
            except (OSError, NotImplementedError):
                # Fall back to symbolic link if hard link fails
                try:
                    target_path.symlink_to(source_path)
                    logger.debug(f"Created symbolic link: {filename}")
                except OSError:
                    # Fall back to copying if symlink also fails
                    import shutil

                    shutil.copy2(source_path, target_path)
                    logger.debug(f"Copied file: {filename}")

    # Copy metrics_sli.parquet to sampled folder for downstream algorithms
    logger.debug("Copying metrics_sli.parquet to sampled folder")
    copy_metrics_sli_to_sampled(input_folder, output_folder)


def calculate_balance_cv(trace_entries_df: pl.DataFrame, sampled_trace_ids: set[str]) -> float:
    """
    Calculate balance metric using Coefficient of Variation (CV) of trace type distribution.

    Based on TracePicker paper: CV = sqrt(sum((n_i - n_mean)^2) / N_t) / n_mean
    where N_t is number of trace types, n_i is count of type i, n_mean is average count.

    Lower CV indicates more balanced distribution across trace types.

    Args:
        trace_entries_df: DataFrame with trace_id and entry_span columns
        sampled_trace_ids: Set of sampled trace IDs

    Returns:
        CV value (0.0 if no sampled traces or only one trace type)
    """
    if not sampled_trace_ids:
        return 0.0

    # Filter to sampled traces only
    sampled_entries_df = trace_entries_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))

    if len(sampled_entries_df) == 0:
        return 0.0

    # Count each trace type (entry_span) in sampled data
    type_counts = sampled_entries_df.group_by("entry_span").agg(pl.len().alias("count"))

    counts = type_counts["count"].to_list()

    if len(counts) <= 1:
        # Perfect balance if only one trace type
        return 0.0

    # Calculate CV: sqrt(variance) / mean

    n_mean = sum(counts) / len(counts)
    variance = sum((n_i - n_mean) ** 2 for n_i in counts) / len(counts)
    cv = math.sqrt(variance) / n_mean if n_mean > 0 else 0.0

    logger.debug(f"Balance CV: {len(counts)} trace types, counts={counts}, mean={n_mean:.2f}, CV={cv:.4f}")

    return cv


def calculate_gt_trace_proportion(
    input_folder: Path,
    all_traces_df: pl.DataFrame,
    sampled_trace_ids: set[str],
    abnormal_trace_ids: set[str],
) -> float:
    """
    Calculate the proportion of ground truth related traces in sampled abnormal traces.

    Only considers abnormal traces, similar to proportion_anomaly metric.

    Args:
        input_folder: Datapack input folder containing injection.json
        all_traces_df: DataFrame with all traces
        sampled_trace_ids: Set of sampled trace IDs
        abnormal_trace_ids: Set of abnormal trace IDs

    Returns:
        Proportion of ground truth related traces in sampled abnormal traces (0.0 to 1.0)
    """
    # Load injection.json to get ground truth services
    injection_path = input_folder / "injection.json"
    if not injection_path.exists():
        logger.warning(f"injection.json not found in {input_folder}")
        return 0.0

    try:
        with open(injection_path) as f:
            injection_data = json.load(f)

        ground_truth = injection_data.get("ground_truth", {})
        gt_services = ground_truth.get("service", [])

        if not gt_services:
            logger.warning("No ground truth services found in injection.json")
            return 0.0

        # Remove mysql if present (as requested)
        gt_services = [svc for svc in gt_services if svc != "mysql"]

        if not gt_services:
            logger.warning("No ground truth services after removing mysql")
            return 0.0

        logger.debug(f"Ground truth services (excluding mysql): {gt_services}")

        # Only consider abnormal traces for GT analysis
        abnormal_traces_df = all_traces_df.filter(pl.col("trace_id").is_in(abnormal_trace_ids))

        if len(abnormal_traces_df) == 0:
            logger.debug("No abnormal traces found")
            return 0.0

        # Find ground truth related traces in abnormal traces only
        gt_trace_ids = set()

        if len(gt_services) == 1:
            # Single service: find abnormal traces containing this service
            service = gt_services[0]
            gt_traces = abnormal_traces_df.filter(pl.col("service_name") == service)["trace_id"].unique()
            gt_trace_ids = set(gt_traces.to_list())
        else:
            # Multiple services: find abnormal traces with call relationships between them
            # Look for parent-child relationships between any pair of gt services
            for i, parent_service in enumerate(gt_services):
                for j, child_service in enumerate(gt_services):
                    if i != j:  # Different services
                        # Find abnormal traces where parent_service calls child_service
                        parent_spans = abnormal_traces_df.filter(pl.col("service_name") == parent_service)
                        child_spans = abnormal_traces_df.filter(pl.col("service_name") == child_service)

                        # Join to find parent-child relationships
                        relationships = parent_spans.join(
                            child_spans.select(["trace_id", "span_id", "parent_span_id"]),
                            left_on=["trace_id", "span_id"],
                            right_on=["trace_id", "parent_span_id"],
                            how="inner",
                        )

                        if len(relationships) > 0:
                            related_traces = relationships["trace_id"].unique().to_list()
                            gt_trace_ids.update(related_traces)

        if not gt_trace_ids:
            logger.debug("No ground truth related traces found in abnormal traces")
            return 0.0

        # Calculate proportion in sampled abnormal traces only
        sampled_abnormal_trace_ids = abnormal_trace_ids.intersection(sampled_trace_ids)
        sampled_gt_traces = gt_trace_ids.intersection(sampled_abnormal_trace_ids)

        if len(sampled_abnormal_trace_ids) == 0:
            logger.debug("No sampled abnormal traces")
            return 0.0

        proportion = len(sampled_gt_traces) / len(sampled_abnormal_trace_ids)

        logger.debug(
            f"Found {len(gt_trace_ids)} GT traces in {len(abnormal_trace_ids)} abnormal traces, "
            f"{len(sampled_gt_traces)} GT in {len(sampled_abnormal_trace_ids)} sampled abnormal, "
            f"proportion: {proportion:.4f}"
        )

        return proportion
    except Exception as e:
        logger.warning(f"Error calculating GT trace proportion: {e}")
        return 0.0


def _calculate_normal_only_performance(
    input_folder: Path,
    sampled_df: pl.DataFrame,
    sampling_rate: float,
    mode: SamplingMode,
    runtime: float | None,
    dataset: str,
) -> dict:
    """Calculate performance metrics for normal-only datasets (like TracePicker)."""
    import math

    # Load only normal traces
    normal_traces_lf = pl.scan_parquet(input_folder / "normal_traces.parquet")

    # Get total counts
    total_traces = normal_traces_lf.select("trace_id").unique().select(pl.len()).collect().item()
    total_spans = normal_traces_lf.select(pl.len()).collect().item()
    sampled_count = len(sampled_df)

    # Calculate actual sampling rate
    actual_sampling_rate = sampled_count / total_traces if total_traces > 0 else 0.0

    # Calculate controllability (RoD)
    expected_count = int(total_traces * sampling_rate)
    controllability = abs((sampled_count - expected_count) / expected_count) if expected_count > 0 else 0.0

    if sampled_count == 0:
        # Return minimal metrics for empty sample
        return {
            "sampled_count": 0,
            "total_traces": total_traces,
            "total_entry_types": 0,
            "sampled_entry_types": 0,
            "controllability": controllability,
            "comprehensiveness": 0.0,
            "proportion_anomaly": 0.0,  # N/A for normal-only
            "proportion_rare": 0.0,
            "proportion_common": 1.0,  # All traces are "common" in normal-only
            "actual_sampling_rate": actual_sampling_rate,
            "runtime_per_span_ms": runtime * 1e3 / total_spans if runtime and total_spans > 0 else 0.0,
            "runtime_per_trace_ms": runtime * 1e3 / total_traces if runtime and total_traces > 0 else 0.0,
            "gt_trace_proportion": 0.0,  # N/A for normal-only
            "balance_cv": 0.0,
            # Path coverage metrics - calculate with empty sampled set
            "total_path_types": 0,
            "sampled_path_types": 0,
            "path_coverage": 0.0,
            "total_path_types_dedup": 0,
            "sampled_path_types_dedup": 0,
            "path_coverage_dedup": 0.0,
            # Event coverage metrics - calculate with empty sampled set
            "total_event_pairs": 0,
            "sampled_event_pairs": 0,
            "event_coverage": 0.0,
            "total_unique_traces": 0,
            "sampled_unique_traces": 0,
            "unique_trace_coverage": 0.0,
            "shannon_entropy": 0.0,
            "benefit_cost_ratio": 0.0,
            "intra_sample_dissimilarity": 0.0,
            "avg_anomaly_score": 0.0,  # N/A for normal-only
            "total_span_count": total_spans,
            "sampled_span_count": 0,
            "total_unique_span_names": 0,
            "sampled_unique_span_names": 0,
            "span_coverage": 0.0,
        }

    # Calculate metrics for normal-only data with samples
    sampled_trace_ids = set(sampled_df["trace_id"].to_list())

    # Load trace data for analysis
    all_traces_df = normal_traces_lf.select(
        ["trace_id", "span_id", "parent_span_id", "service_name", "span_name"]
    ).collect()

    # Calculate entry types for comprehensiveness
    # For TracePicker datasets, we don't have loadgenerator, so we use any root spans
    entry_traces_df = all_traces_df.filter(pl.col("parent_span_id").is_null() | (pl.col("parent_span_id") == ""))

    # For TracePicker, we'll use the operation name directly as entry_span
    # since there's no loadgenerator pattern to extract from
    if len(entry_traces_df) > 0:
        trace_entries_df = entry_traces_df.group_by("trace_id").agg(pl.first("span_name").alias("entry_span"))

        entry_span_counts = trace_entries_df.group_by("entry_span").agg(pl.len().alias("count"))
        total_entry_types = len(entry_span_counts)

        # Calculate sampled entry types
        sampled_entries_df = trace_entries_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))
        sampled_entry_types = len(sampled_entries_df["entry_span"].unique())
        comprehensiveness = sampled_entry_types / total_entry_types if total_entry_types > 0 else 0.0

        # Calculate rare trace proportion (< 5% frequency)
        entry_span_proportions = entry_span_counts.with_columns((pl.col("count") / total_traces).alias("proportion"))
        rare_spans = entry_span_proportions.filter(pl.col("proportion") < 0.05)["entry_span"].to_list()
        rare_sampled = sampled_entries_df.filter(pl.col("entry_span").is_in(rare_spans))
        proportion_rare = len(rare_sampled) / sampled_count if sampled_count > 0 else 0.0

        # Balance CV calculation for normal-only
        sampled_entry_counts = sampled_entries_df.group_by("entry_span").agg(pl.len().alias("count"))
        counts = sampled_entry_counts["count"].to_list()

        if len(counts) <= 1:
            balance_cv = 0.0
        else:
            n_mean = sum(counts) / len(counts)
            variance = sum((n_i - n_mean) ** 2 for n_i in counts) / len(counts)
            balance_cv = math.sqrt(variance) / n_mean if n_mean > 0 else 0.0
    else:
        total_entry_types = 0
        sampled_entry_types = 0
        comprehensiveness = 0.0
        proportion_rare = 0.0
        balance_cv = 0.0

    # Calculate path coverage
    path_coverage_metrics = calculate_path_coverage(all_traces_df, sampled_trace_ids, dataset)
    path_coverage_dedup_metrics = calculate_path_coverage_dedup(all_traces_df, sampled_trace_ids, dataset)

    # Calculate event coverage (normal traces only, no logs)
    all_traces_for_events_df = normal_traces_lf.select(
        [
            "trace_id",
            "span_id",
            "parent_span_id",
            "service_name",
            "span_name",
            "time",
            "duration",
            "attr.status_code",
        ]
    ).collect()

    event_coverage_metrics = calculate_event_coverage(
        all_traces_for_events_df, None, sampled_trace_ids, input_folder, dataset
    )

    # Calculate span statistics
    total_span_count = len(all_traces_for_events_df)
    sampled_span_count = len(all_traces_for_events_df.filter(pl.col("trace_id").is_in(sampled_trace_ids)))

    all_unique_spans = all_traces_for_events_df.select(
        pl.concat_str(["service_name", "span_name"], separator="_").alias("span_name")
    ).unique()
    total_unique_span_names = len(all_unique_spans)

    sampled_unique_spans = (
        all_traces_for_events_df.filter(pl.col("trace_id").is_in(sampled_trace_ids))
        .select(pl.concat_str(["service_name", "span_name"], separator="_").alias("span_name"))
        .unique()
    )
    sampled_unique_span_names = len(sampled_unique_spans)
    span_coverage = sampled_unique_span_names / total_unique_span_names if total_unique_span_names > 0 else 0.0

    return {
        "sampled_count": sampled_count,
        "total_traces": total_traces,
        "total_entry_types": total_entry_types,
        "sampled_entry_types": sampled_entry_types,
        "controllability": controllability,
        "comprehensiveness": comprehensiveness,
        "proportion_anomaly": 0.0,  # N/A for normal-only datasets
        "proportion_rare": proportion_rare,
        "proportion_common": 1.0 - proportion_rare,  # Rest are common
        "actual_sampling_rate": actual_sampling_rate,
        "runtime_per_span_ms": runtime * 1e3 / total_spans if runtime and total_spans > 0 else 0.0,
        "runtime_per_trace_ms": runtime * 1e3 / total_traces if runtime and total_traces > 0 else 0.0,
        "gt_trace_proportion": 0.0,  # N/A for normal-only datasets
        "balance_cv": balance_cv,
        # Path coverage metrics
        "total_path_types": path_coverage_metrics["total_path_types"],
        "sampled_path_types": path_coverage_metrics["sampled_path_types"],
        "path_coverage": path_coverage_metrics["path_coverage"],
        "total_path_types_dedup": path_coverage_dedup_metrics["total_path_types_dedup"],
        "sampled_path_types_dedup": path_coverage_dedup_metrics["sampled_path_types_dedup"],
        "path_coverage_dedup": path_coverage_dedup_metrics["path_coverage_dedup"],
        # Event coverage metrics
        "total_event_pairs": event_coverage_metrics["total_event_pairs"],
        "sampled_event_pairs": event_coverage_metrics["sampled_event_pairs"],
        "event_coverage": event_coverage_metrics["event_coverage"],
        "total_unique_traces": event_coverage_metrics["total_unique_traces"],
        "sampled_unique_traces": event_coverage_metrics["sampled_unique_traces"],
        "unique_trace_coverage": event_coverage_metrics["unique_trace_coverage"],
        "shannon_entropy": event_coverage_metrics["shannon_entropy"],
        "benefit_cost_ratio": event_coverage_metrics["benefit_cost_ratio"],
        "intra_sample_dissimilarity": event_coverage_metrics["intra_sample_dissimilarity"],
        "avg_anomaly_score": 0.0,  # N/A for normal-only datasets (no errors expected)
        # Span count metrics
        "total_span_count": total_span_count,
        "sampled_span_count": sampled_span_count,
        "total_unique_span_names": total_unique_span_names,
        "sampled_unique_span_names": sampled_unique_span_names,
        "span_coverage": span_coverage,
    }
