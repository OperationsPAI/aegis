"""Sampler performance report generation."""

import polars as pl

from ...datasets.spec import get_datapack_folder, get_datapack_list
from ...logging import logger, timeit
from ...utils.dataframe import print_dataframe
from ...utils.serde import save_parquet
from ..spec import SamplingMode, global_sampler_registry
from .spec import get_sampler_output_folder


@timeit(log_level="INFO")
def generate_sampler_perf_report(
    datasets: list[str],
    samplers: list[str] | None = None,
    sampling_rates: list[float] | None = None,
    modes: list[SamplingMode] | None = None,
    *,
    warn_missing: bool = False,
):
    """
    Generate performance report for sampler experiments.

    Automatically scans all available sampler outputs in the dataset.
    Similar to algorithm report, only requires dataset specification.

    Args:
        datasets: List of dataset names to include in report
        samplers: List of sampler names (default: auto-detect from outputs)
        sampling_rates: List of sampling rates (default: auto-detect from outputs)
        modes: List of modes (default: auto-detect from outputs)
        warn_missing: Whether to warn about missing result files
    """
    # Auto-detect available samplers, rates, and modes if not specified
    if samplers is None or sampling_rates is None or modes is None:
        (
            available_samplers,
            available_rates,
            available_modes,
        ) = _scan_available_configurations(datasets)

        if samplers is None:
            samplers = available_samplers
            logger.info(f"Auto-detected samplers: {samplers}")

        if sampling_rates is None:
            sampling_rates = available_rates
            logger.info(f"Auto-detected sampling rates: {sampling_rates}")

        if modes is None:
            modes = available_modes
            logger.info(f"Auto-detected sampling modes: {[m.value for m in modes]}")

    all_perf_data = []

    for dataset in datasets:
        datapacks = get_datapack_list(dataset)
        logger.info(f"Processing dataset {dataset} with {len(datapacks)} datapacks")

        # Auto-scan all available sampler outputs
        perf_files = _scan_all_sampler_outputs(dataset, datapacks, warn_missing)

        if len(perf_files) == 0:
            logger.warning(f"No sampler performance files found for dataset {dataset}")
            continue

        logger.debug(f"Loading {len(perf_files)} perf files for dataset {dataset}")

        # Load and combine all performance data
        for perf_file in perf_files:
            try:
                perf_df = pl.read_parquet(perf_file, rechunk=True)

                # Extract metadata from file path
                # Path structure: {datapack_folder}/sampled/{sampler}_{rate}_{mode}/perf.parquet
                path_parts = perf_file.parts

                # Find the sampler folder (should be the parent of perf.parquet)
                sampler_folder = perf_file.parent.name

                # The datapack name is the parent of the "sampled" folder
                sampled_idx = None
                for i, part in enumerate(path_parts):
                    if part == "sampled":
                        sampled_idx = i
                        break

                if sampled_idx is not None and sampled_idx > 0:
                    datapack_name = path_parts[sampled_idx - 1]
                else:
                    # Fallback: extract from path
                    datapack_name = None
                    for datapack in datapacks:
                        if datapack in str(perf_file):
                            datapack_name = datapack
                            break

                    if not datapack_name:
                        if warn_missing:
                            logger.warning(f"Could not extract datapack name from {perf_file}")
                        continue

                if sampler_folder:
                    # Parse sampler_rate_mode pattern
                    parts = sampler_folder.split("_")
                    if len(parts) >= 3:
                        sampler_name = "_".join(parts[:-2])  # Handle multi-word sampler names
                        rate_str = parts[-2]
                        mode_str = parts[-1]

                        try:
                            sampling_rate = float(rate_str)
                            mode = SamplingMode(mode_str)

                            # Apply filters if specified
                            if samplers and sampler_name not in samplers:
                                continue
                            if sampling_rates and sampling_rate not in sampling_rates:
                                continue
                            if modes and mode not in modes:
                                continue

                            # Ensure consistent schema by adding missing columns with default values
                            perf_df = _normalize_perf_schema(perf_df)

                            # Add metadata columns
                            perf_df = perf_df.with_columns(
                                [
                                    pl.lit(dataset).alias("dataset"),
                                    pl.lit(datapack_name).alias("datapack"),
                                    pl.lit(sampler_name).alias("sampler"),
                                    pl.lit(sampling_rate).alias("sampling_rate"),
                                    pl.lit(mode.value).alias("mode"),
                                ]
                            )

                            all_perf_data.append(perf_df)

                        except (ValueError, TypeError) as e:
                            if warn_missing:
                                logger.warning(f"Failed to parse sampler metadata from {perf_file}: {e}")

            except Exception as e:
                if warn_missing:
                    logger.warning(f"Failed to load {perf_file}: {e}")

    if len(all_perf_data) == 0:
        logger.warning("No sampler performance data found")
        return

    # Combine all performance data
    combined_perf_df = pl.concat(all_perf_data, how="vertical_relaxed", rechunk=True)

    # Calculate aggregate statistics
    agg_perf_df = (
        combined_perf_df.group_by(["sampler", "dataset", "sampling_rate", "mode"])
        .agg(
            [
                pl.len().alias("datapack_count"),
                pl.col("sampled_count").mean().alias("avg_sampled_count"),
                pl.col("total_traces").mean().alias("avg_total_traces"),
                pl.col("controllability").mean().alias("avg_controllability"),
                pl.col("comprehensiveness").mean().alias("avg_api_coverage"),
                pl.col("path_coverage").mean().alias("avg_path_coverage"),
                pl.col("event_coverage").mean().alias("avg_event_coverage"),
                pl.col("proportion_anomaly").mean().alias("avg_proportion_anomaly"),
                pl.col("proportion_rare").mean().alias("avg_proportion_rare"),
                pl.col("proportion_common").mean().alias("avg_proportion_common"),
                pl.col("actual_sampling_rate").mean().alias("avg_actual_sampling_rate"),
                pl.col("runtime_per_span_ms").mean().alias("avg_runtime_per_span_ms"),
                pl.col("runtime_per_trace_ms").mean().alias("avg_runtime_per_trace_ms"),
                pl.col("gt_trace_proportion").mean().alias("avg_gt_trace_proportion"),
                pl.col("balance_cv").mean().alias("avg_balance_cv"),
                pl.col("total_path_types").mean().alias("avg_total_path_types"),
                pl.col("sampled_path_types").mean().alias("avg_sampled_path_types"),
                # Deduplicated path coverage metrics
                pl.col("total_path_types_dedup").mean().alias("avg_total_path_types_dedup"),
                pl.col("sampled_path_types_dedup").mean().alias("avg_sampled_path_types_dedup"),
                pl.col("path_coverage_dedup").mean().alias("avg_path_coverage_dedup"),
                pl.col("total_event_pairs").mean().alias("avg_total_event_pairs"),
                pl.col("sampled_event_pairs").mean().alias("avg_sampled_event_pairs"),
                pl.col("total_unique_traces").mean().alias("avg_total_unique_traces"),
                pl.col("sampled_unique_traces").mean().alias("avg_sampled_unique_traces"),
                pl.col("unique_trace_coverage").mean().alias("avg_unique_trace_coverage"),
                pl.col("total_span_count").mean().alias("avg_total_span_count"),
                pl.col("sampled_span_count").mean().alias("avg_sampled_span_count"),
                pl.col("total_unique_span_names").mean().alias("avg_total_unique_span_names"),
                pl.col("sampled_unique_span_names").mean().alias("avg_sampled_unique_span_names"),
                pl.col("span_coverage").mean().alias("avg_span_coverage"),
                # New metrics
                pl.col("shannon_entropy").mean().alias("avg_shannon_entropy"),
                pl.col("benefit_cost_ratio").mean().alias("avg_benefit_cost_ratio"),
                pl.col("intra_sample_dissimilarity").mean().alias("avg_intra_sample_dissimilarity"),
                pl.col("avg_anomaly_score").mean().alias("avg_avg_anomaly_score"),
                # Standard deviation for key metrics
                pl.col("controllability").std().alias("std_controllability"),
                pl.col("comprehensiveness").std().alias("std_api_coverage"),
                pl.col("path_coverage").std().alias("std_path_coverage"),
                pl.col("path_coverage_dedup").std().alias("std_path_coverage_dedup"),
                pl.col("event_coverage").std().alias("std_event_coverage"),
                pl.col("actual_sampling_rate").std().alias("std_actual_sampling_rate"),
                pl.col("runtime_per_span_ms").std().alias("std_runtime_per_span_ms"),
                pl.col("runtime_per_trace_ms").std().alias("std_runtime_per_trace_ms"),
                pl.col("gt_trace_proportion").std().alias("std_gt_trace_proportion"),
                pl.col("balance_cv").std().alias("std_balance_cv"),
                pl.col("unique_trace_coverage").std().alias("std_unique_trace_coverage"),
                pl.col("span_coverage").std().alias("std_span_coverage"),
                pl.col("shannon_entropy").std().alias("std_shannon_entropy"),
                pl.col("benefit_cost_ratio").std().alias("std_benefit_cost_ratio"),
                pl.col("intra_sample_dissimilarity").std().alias("std_intra_sample_dissimilarity"),
                pl.col("avg_anomaly_score").std().alias("std_avg_anomaly_score"),
                # Minimum values for key metrics
                pl.col("controllability").min().alias("min_controllability"),
                pl.col("comprehensiveness").min().alias("min_api_coverage"),
                pl.col("path_coverage").min().alias("min_path_coverage"),
                pl.col("path_coverage_dedup").min().alias("min_path_coverage_dedup"),
                pl.col("event_coverage").min().alias("min_event_coverage"),
                pl.col("actual_sampling_rate").min().alias("min_actual_sampling_rate"),
                pl.col("runtime_per_span_ms").min().alias("min_runtime_per_span_ms"),
                pl.col("runtime_per_trace_ms").min().alias("min_runtime_per_trace_ms"),
                pl.col("gt_trace_proportion").min().alias("min_gt_trace_proportion"),
                pl.col("balance_cv").min().alias("min_balance_cv"),
                pl.col("unique_trace_coverage").min().alias("min_unique_trace_coverage"),
                pl.col("span_coverage").min().alias("min_span_coverage"),
                pl.col("shannon_entropy").min().alias("min_shannon_entropy"),
                pl.col("benefit_cost_ratio").min().alias("min_benefit_cost_ratio"),
                pl.col("intra_sample_dissimilarity").min().alias("min_intra_sample_dissimilarity"),
                pl.col("avg_anomaly_score").min().alias("min_avg_anomaly_score"),
                # Maximum values for key metrics
                pl.col("controllability").max().alias("max_controllability"),
                pl.col("comprehensiveness").max().alias("max_api_coverage"),
                pl.col("path_coverage").max().alias("max_path_coverage"),
                pl.col("path_coverage_dedup").max().alias("max_path_coverage_dedup"),
                pl.col("event_coverage").max().alias("max_event_coverage"),
                pl.col("actual_sampling_rate").max().alias("max_actual_sampling_rate"),
                pl.col("runtime_per_span_ms").max().alias("max_runtime_per_span_ms"),
                pl.col("runtime_per_trace_ms").max().alias("max_runtime_per_trace_ms"),
                pl.col("gt_trace_proportion").max().alias("max_gt_trace_proportion"),
                pl.col("balance_cv").max().alias("max_balance_cv"),
                pl.col("unique_trace_coverage").max().alias("max_unique_trace_coverage"),
                pl.col("span_coverage").max().alias("max_span_coverage"),
                pl.col("shannon_entropy").max().alias("max_shannon_entropy"),
                pl.col("benefit_cost_ratio").max().alias("max_benefit_cost_ratio"),
                pl.col("intra_sample_dissimilarity").max().alias("max_intra_sample_dissimilarity"),
                pl.col("avg_anomaly_score").max().alias("max_avg_anomaly_score"),
            ]
        )
        .sort(["sampler", "dataset", "sampling_rate", "mode"])
    )

    # Save detailed and aggregated results with dataset-specific directories
    from ...config import get_config

    config = get_config()

    # Create dataset-specific output directories
    for dataset in datasets:
        dataset_output_folder = config.output / "sampler_reports" / dataset
        dataset_output_folder.mkdir(parents=True, exist_ok=True)

        # Filter data for this dataset
        dataset_detailed_df = combined_perf_df.filter(pl.col("dataset") == dataset)
        dataset_agg_df = agg_perf_df.filter(pl.col("dataset") == dataset)

        if len(dataset_detailed_df) > 0:
            save_parquet(
                dataset_detailed_df,
                path=dataset_output_folder / "detailed_perf.parquet",
            )
            save_parquet(dataset_agg_df, path=dataset_output_folder / "aggregated_perf.parquet")
            logger.info(f"Saved {dataset} sampler reports to: {dataset_output_folder}")

    # Print summary table
    display_df = agg_perf_df.select(
        [
            "sampler",
            "dataset",
            "sampling_rate",
            "mode",
            "datapack_count",
            "avg_controllability",
            "avg_api_coverage",
            "avg_path_coverage",
            "avg_path_coverage_dedup",
            "avg_event_coverage",
            "avg_unique_trace_coverage",
            "avg_span_coverage",
            "avg_shannon_entropy",
            "avg_benefit_cost_ratio",
            "avg_intra_sample_dissimilarity",
            "avg_avg_anomaly_score",
            "avg_gt_trace_proportion",
            "avg_balance_cv",
            "avg_proportion_anomaly",
            "avg_proportion_rare",
            "avg_proportion_common",
            "avg_actual_sampling_rate",
            "avg_runtime_per_span_ms",
            "avg_runtime_per_trace_ms",
        ]
    )

    logger.info("Sampler Performance Summary:")
    print_dataframe(display_df)

    logger.info(f"Dataset-specific results saved to: {config.output / 'sampler_reports'}")


def _scan_available_configurations(
    datasets: list[str],
) -> tuple[list[str], list[float], list[SamplingMode]]:
    """
    Scan all available sampler configurations across datasets.

    Returns:
        Tuple of (available_samplers, available_rates, available_modes)
    """
    samplers = set()
    rates = set()
    modes = set()

    for dataset in datasets:
        datapacks = get_datapack_list(dataset)

        # Scan first few datapacks to find available configurations
        scan_datapacks = datapacks[: min(10, len(datapacks))]

        for datapack in scan_datapacks:
            datapack_folder = get_datapack_folder(dataset, datapack)
            sampled_folder = datapack_folder / "sampled"

            if not sampled_folder.exists():
                continue

            # Look for all sampler folders matching pattern: {sampler}_{rate}_{mode}
            for sampler_folder in sampled_folder.iterdir():
                if not sampler_folder.is_dir():
                    continue

                folder_name = sampler_folder.name
                parts = folder_name.split("_")

                if len(parts) >= 3:
                    try:
                        # Extract sampler name (all parts except last 2)
                        sampler_name = "_".join(parts[:-2])
                        rate_str = parts[-2]
                        mode_str = parts[-1]

                        # Validate and add to sets
                        rate = float(rate_str)
                        mode = SamplingMode(mode_str)

                        if 0.0 <= rate <= 1.0:
                            samplers.add(sampler_name)
                            rates.add(rate)
                            modes.add(mode)

                    except (ValueError, TypeError):
                        continue

    # Convert to sorted lists
    available_samplers = sorted(list(samplers))
    available_rates = sorted(list(rates))
    available_modes = sorted(list(modes), key=lambda x: x.value)

    return available_samplers, available_rates, available_modes


def _scan_available_sampling_rates(
    dataset: str, datapacks: list[str], samplers: list[str], modes: list[SamplingMode]
) -> list[float]:
    """Scan for available sampling rates in existing output folders."""
    from pathlib import Path

    from ...config import get_config

    config = get_config()
    rates = set()

    # Scan first few datapacks to find available rates
    scan_datapacks = datapacks[: min(5, len(datapacks))]

    for datapack in scan_datapacks:
        datapack_folder = config.output / "sampled" / dataset / datapack
        if not datapack_folder.exists():
            continue

        # Look for folders matching pattern: {sampler}_{rate}_{mode}
        for folder in datapack_folder.iterdir():
            if not folder.is_dir():
                continue

            folder_name = folder.name
            # Parse pattern: sampler_rate_mode
            parts = folder_name.split("_")
            if len(parts) >= 3:
                try:
                    # Find the rate part (should be a float)
                    for part in parts[1:-1]:  # Skip first (sampler) and last (mode)
                        rate = float(part)
                        if 0.0 <= rate <= 1.0:
                            rates.add(rate)
                            break
                except ValueError:
                    continue

    return sorted(list(rates)) if rates else [0.1, 0.2, 0.5]  # Default rates if none found


def _scan_all_sampler_outputs(dataset: str, datapacks: list[str], warn_missing: bool = False) -> list:
    """Scan all available sampler performance files for a dataset."""
    from ...datasets.spec import get_datapack_folder

    perf_files = []

    for datapack in datapacks:
        # Get the actual datapack folder (not output/sampled/dataset/datapack)
        datapack_folder = get_datapack_folder(dataset, datapack)
        sampled_folder = datapack_folder / "sampled"

        if not sampled_folder.exists():
            if warn_missing:
                logger.warning(f"Sampled folder not found: {sampled_folder}")
            continue

        # Look for all sampler folders matching pattern: {sampler}_{rate}_{mode}
        for sampler_folder in sampled_folder.iterdir():
            if not sampler_folder.is_dir():
                continue

            # Check if folder name contains mode suffix
            folder_name = sampler_folder.name
            if any(mode.value in folder_name for mode in [SamplingMode.ONLINE, SamplingMode.OFFLINE]):
                perf_file = sampler_folder / "perf.parquet"
                if perf_file.exists():
                    perf_files.append(perf_file)
                elif warn_missing:
                    logger.warning(f"Missing perf file: {perf_file}")

    return perf_files


def _normalize_perf_schema(perf_df: pl.DataFrame) -> pl.DataFrame:
    """
    Normalize performance DataFrame schema by adding missing columns with default values.

    This handles cases where sampling resulted in 0 traces, causing coverage metrics
    and other derived columns to be missing.
    """
    # Define all expected columns with their default values in correct order
    expected_columns = {
        "sampled_count": 0,
        "total_traces": 0,
        "total_entry_types": 0,
        "sampled_entry_types": 0,
        "controllability": 0.0,
        "comprehensiveness": 0.0,  # API coverage
        "path_coverage": 0.0,
        "event_coverage": 0.0,
        "proportion_anomaly": 0.0,
        "proportion_rare": 0.0,
        "proportion_common": 0.0,
        "actual_sampling_rate": 0.0,
        "runtime_per_span_ms": 0.0,
        "runtime_per_trace_ms": 0.0,
        "gt_trace_proportion": 0.0,  # Ground truth trace proportion
        "balance_cv": 0.0,  # Balance metric using Coefficient of Variation
        "total_path_types": 0,
        "sampled_path_types": 0,
        # Deduplicated path coverage metrics
        "total_path_types_dedup": 0,
        "sampled_path_types_dedup": 0,
        "path_coverage_dedup": 0.0,
        "total_event_pairs": 0,
        "sampled_event_pairs": 0,
        "total_unique_traces": 0,
        "sampled_unique_traces": 0,
        "unique_trace_coverage": 0.0,
        "total_span_count": 0,
        "sampled_span_count": 0,
        "total_unique_span_names": 0,
        "sampled_unique_span_names": 0,
        "span_coverage": 0.0,
        # New metrics
        "shannon_entropy": 0.0,
        "benefit_cost_ratio": 0.0,
        "intra_sample_dissimilarity": 0.0,
        "avg_anomaly_score": 0.0,
    }

    # Add missing columns with default values
    missing_columns = []
    existing_columns = set(perf_df.columns)

    for col_name, default_value in expected_columns.items():
        if col_name not in existing_columns:
            missing_columns.append(pl.lit(default_value).alias(col_name))

    if missing_columns:
        perf_df = perf_df.with_columns(missing_columns)

    # Ensure consistent column ordering by selecting in expected order
    # Only select columns that exist (some might be missing in old data)
    available_columns = [col for col in expected_columns.keys() if col in perf_df.columns]
    if available_columns:
        perf_df = perf_df.select(available_columns)

    return perf_df
