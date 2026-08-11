from pathlib import Path

import matplotlib.cm as cm
import matplotlib.pyplot as plt
import numpy as np
import polars as pl

from ..logging import logger

# Algorithm name mapping for display
ALGORITHM_NAME_MAPPING = {
    "art": "Art",
    "baro": "Baro",
    "diagfusion": "DiagFusion",
    "eadro": "Eadro",
    "microdig": "MicroDig",
    "microhecl": "MicroHECL",
    "microrank": "MicroRank",
    "microrca": "MicroRCA",
    "shapleyiq": "ShapleyIQ",
    "simplerca": "SimpleRCA",
    "nezha": "Nezha",
    "run": "RUN",
    "causalrca": "CausalRCA",
}
BASE_COLORS = [
    "#dd9f94",
    "#f5e8bd",
    "#b0bda0",
    "#b87264",
    "#464666",
    "#7da4a3",
    "#b3b6be",
    "#a0acc8",
    "#b0bfa1",
    "#db716e",
    "#be958a",
    "#e4ce90",
    "#c98849",
    "#785177",
    "#56777e",
    "#c5ccdb",
]


def get_display_algorithm_name(algorithm: str) -> str:
    return ALGORITHM_NAME_MAPPING.get(algorithm.lower(), algorithm)


def algo_perf_by_groups(
    df: pl.DataFrame,
    output_file: Path | None = None,
) -> None:
    if df.height == 0:
        logger.warning("No data available to generate chart")
        return

    if "algorithm" not in df.columns:
        logger.warning("No 'algorithm' column found in data")
        return

    metric_cols = ["top1", "mrr", "avg5"]
    group_cols = [col for col in df.columns if col not in ["algorithm", "time", "count"] + metric_cols]
    if not group_cols:
        logger.warning("No group columns found in data")
        return

    group_title = " × ".join(group_cols)

    # Check for metric columns
    available_metrics = [col for col in ["top1", "mrr", "avg5"] if col in df.columns]
    if not available_metrics:
        logger.warning("No performance metric data found")
        return

    algorithms = df["algorithm"].unique().to_list()
    algorithms = sorted([algo for algo in algorithms if algo is not None])

    if not algorithms:
        logger.warning("No valid algorithm data found")
        return

    # Get unique group combinations
    if len(group_cols) == 1:
        groups = df.select(group_cols[0]).unique().to_pandas()[group_cols[0]].tolist()
        groups = sorted([combo for combo in groups if combo is not None])
    else:
        group_df = df.select(group_cols).unique().to_pandas()
        groups = [tuple(row) for row in group_df.values if not any(val is None for val in row)]
        groups = sorted(groups)

    if not groups:
        logger.warning("No group data found")
        return

    metric_colors = {metric: BASE_COLORS[i % len(BASE_COLORS)] for i, metric in enumerate(available_metrics)}

    # Calculate subplot layout
    n_groups = len(groups)
    cols = min(6, n_groups)  # Maximum 6 columns per row
    rows = (n_groups + cols - 1) // cols  # Ceiling division

    # Adjust figure size based on actual layout
    adjusted_figsize = (cols * 4, rows * 4)

    # Create figure with subplots
    fig, axes = plt.subplots(rows, cols, figsize=adjusted_figsize)

    # Ensure axes is always a flat array for easy indexing
    if rows == 1 and cols == 1:
        axes = [axes]
    elif rows == 1 or cols == 1:
        axes = axes.flatten() if hasattr(axes, "flatten") else [axes]
    else:
        axes = axes.flatten()

    # Plot each group in a subplot
    for idx, group in enumerate(groups):
        ax = axes[idx]

        # Filter data for current group
        if len(group_cols) == 1:
            group_data = df.filter(pl.col(group_cols[0]) == group)
            group_label = str(group)
        else:
            filter_conditions = []
            for i, col in enumerate(group_cols):
                filter_conditions.append(pl.col(col) == group[i])

            combined_filter = filter_conditions[0]
            for condition in filter_conditions[1:]:
                combined_filter = combined_filter & condition

            group_data = df.filter(combined_filter)
            group_label = " × ".join(str(val) for val in group)

        if group_data.height == 0:
            logger.warning(f"No data found for group: {group}")
            ax.text(
                0.5,
                0.5,
                f"No data for\n{group}",
                ha="center",
                va="center",
                transform=ax.transAxes,
            )
            ax.set_xticks([])
            ax.set_yticks([])
            continue

        # Prepare data
        x_pos = np.arange(len(algorithms))
        bar_width = 0.6 / len(available_metrics)  # Reduced width for better spacing between groups

        for metric_idx, metric in enumerate(available_metrics):
            values = []

            for algorithm in algorithms:
                # Filter data for this algorithm
                algo_data = group_data.filter(pl.col("algorithm") == algorithm)
                if algo_data.height > 0 and metric in algo_data.columns:
                    value = algo_data[metric].to_list()[0]
                    values.append(value if value is not None else 0.0)
                else:
                    values.append(0.0)

            x_positions = x_pos + (metric_idx - len(available_metrics) / 2 + 0.5) * bar_width

            ax.bar(
                x_positions,
                values,
                bar_width,
                label=metric.upper() if idx == 0 else "",  # Only show legend on first subplot
                color=metric_colors[metric],
                alpha=0.8,
                edgecolor="black",
                linewidth=0.5,
            )

        # Truncate long group names for title
        title_text = group_label if len(group_label) <= 20 else group_label[:17] + "..."
        ax.set_title(title_text, fontsize=11, fontweight="bold")

        ax.set_xticks(x_pos)
        ax.set_xticklabels(
            [get_display_algorithm_name(algo) for algo in algorithms],
            fontsize=9,
            rotation=45,
            ha="right",
        )
        ax.grid(True, alpha=0.3, axis="y")

        # Set y-axis limits to accommodate labels
        ax.set_ylim(0, 1.15)

    # Hide empty subplots
    total_subplots = rows * cols
    for idx in range(n_groups, total_subplots):
        axes[idx].set_visible(False)

    # Add legend to the figure
    if n_groups > 0:
        fig.legend(
            [metric.upper() for metric in available_metrics],
            loc="upper center",
            bbox_to_anchor=(0.5, 0.98),
            ncol=len(available_metrics),
            fontsize=12,
        )

    # Add common axis labels to the figure
    fig.text(0.5, 0.02, "Algorithm", ha="center", va="bottom", fontsize=12, fontweight="bold")
    fig.text(
        0.02,
        0.5,
        "Performance Score",
        ha="center",
        va="center",
        rotation=90,
        fontsize=12,
        fontweight="bold",
    )

    # Add title with grouping information
    chart_title = f"Algorithm Performance by {group_title}"
    fig.suptitle(chart_title, fontsize=14, fontweight="bold", y=0.99)

    plt.tight_layout()
    plt.subplots_adjust(top=0.9, bottom=0.08, left=0.08)  # Make room for the legend, title and axis labels

    # Save or display chart
    if output_file:
        parent = output_file.parent
        parent.mkdir(parents=True, exist_ok=True)

        plt.savefig(output_file, format="pdf", bbox_inches="tight")

    plt.show()


def algo_perf_by_fault_type(
    df: pl.DataFrame,
    output_file: Path | None = None,
) -> None:
    if df.height == 0:
        logger.warning("No data available to generate chart")
        return

    # Check if we have the required columns
    if "algorithm" not in df.columns:
        logger.warning("No 'algorithm' column found in data")
        return

    if "fault_type" not in df.columns:
        logger.warning("No 'fault_type' column found in data")
        return

    # Check for metric columns
    metric_cols = ["top1", "mrr", "avg5"]
    available_metrics = [col for col in metric_cols if col in df.columns]
    if not available_metrics:
        logger.warning("No performance metric data found")
        return

    # Get unique fault types and algorithms
    fault_types = df["fault_type"].unique().to_list()
    fault_types = sorted([ft for ft in fault_types if ft is not None])

    algorithms = df["algorithm"].unique().to_list()
    algorithms = sorted([algo for algo in algorithms if algo is not None])

    if not fault_types:
        logger.warning("No fault_type data found")
        return

    if not algorithms:
        logger.warning("No valid algorithm data found")
        return

    # Set color mapping for metrics
    metric_colors = {metric: BASE_COLORS[i % len(BASE_COLORS)] for i, metric in enumerate(available_metrics)}

    # Calculate subplot layout
    n_fault_types = len(fault_types)
    cols = min(5, n_fault_types)  # Maximum 4 columns per row
    rows = (n_fault_types + cols - 1) // cols  # Ceiling division

    # Adjust figure size based on actual layout
    adjusted_figsize = (cols * 5, rows * 3)

    # Create figure with subplots
    fig, axes = plt.subplots(rows, cols, figsize=adjusted_figsize)

    # Ensure axes is always a flat array for easy indexing
    if rows == 1 and cols == 1:
        axes = [axes]
    elif rows == 1 or cols == 1:
        axes = axes.flatten() if hasattr(axes, "flatten") else [axes]
    else:
        axes = axes.flatten()

    # Plot each fault type in a subplot
    for idx, fault_type in enumerate(fault_types):
        ax = axes[idx]

        # Filter data for current fault type
        fault_data = df.filter(pl.col("fault_type") == fault_type)

        if fault_data.height == 0:
            logger.warning(f"No data found for fault_type: {fault_type}")
            ax.text(
                0.5,
                0.5,
                f"No data for\n{fault_type}",
                ha="center",
                va="center",
                transform=ax.transAxes,
            )
            ax.set_xticks([])
            ax.set_yticks([])
            continue

        # Prepare data
        x_pos = np.arange(len(algorithms))
        bar_width = 0.6 / len(available_metrics)  # Reduced width for better spacing between groups

        for metric_idx, metric in enumerate(available_metrics):
            values = []

            for algorithm in algorithms:
                # Filter data for this algorithm
                algo_data = fault_data.filter(pl.col("algorithm") == algorithm)
                if algo_data.height > 0 and metric in algo_data.columns:
                    value = algo_data[metric].to_list()[0]
                    values.append(value if value is not None else 0.0)
                else:
                    values.append(0.0)

            x_positions = x_pos + (metric_idx - len(available_metrics) / 2 + 0.5) * bar_width

            ax.bar(
                x_positions,
                values,
                bar_width,
                label=metric.upper() if idx == 0 else "",  # Only show legend on first subplot
                color=metric_colors[metric],
                alpha=0.8,
                edgecolor="black",
                linewidth=0.5,
            )

        # Truncate long fault type names for title
        title_text = fault_type if len(fault_type) <= 25 else fault_type[:22] + "..."
        ax.set_title(title_text, fontsize=11, fontweight="bold")

        ax.set_xticks(x_pos)
        ax.set_xticklabels(
            [get_display_algorithm_name(algo) for algo in algorithms],
            fontsize=9,
            rotation=45,
            ha="right",
        )
        ax.grid(True, alpha=0.3, axis="y")

        # Set y-axis limits to accommodate labels
        ax.set_ylim(0, 1.15)

    # Hide empty subplots
    total_subplots = rows * cols
    for idx in range(n_fault_types, total_subplots):
        axes[idx].set_visible(False)

    # Add legend to the figure
    if n_fault_types > 0:
        fig.legend(
            [metric.upper() for metric in available_metrics],
            loc="upper center",
            bbox_to_anchor=(0.5, 0.98),
            ncol=len(available_metrics),
            fontsize=12,
        )

    plt.tight_layout()
    plt.subplots_adjust(top=0.9, bottom=0.08, left=0.08)  # Make room for the legend, title and axis labels

    # Save or display chart
    if output_file:
        parent = output_file.parent
        parent.mkdir(parents=True, exist_ok=True)

        # Determine format from file extension
        file_format = output_file.suffix.lower().lstrip(".")

        # Configure save parameters based on format
        if file_format == "pdf":
            # PDF-specific settings for better quality
            plt.savefig(output_file, format="pdf", bbox_inches="tight")
        else:
            # For raster formats (png, jpg, etc.), use high DPI
            plt.savefig(output_file, dpi=300, bbox_inches="tight")

        logger.info(f"Chart saved to: {output_file} (format: {file_format.upper()})")

    plt.show()


def algo_success_by_algo(
    df: pl.DataFrame,
    output_file: Path | None = None,
) -> None:
    if df.height == 0:
        logger.warning("No data available to generate chart")
        return

    # Check if input is wide format (from perf_group_by_fault_type) or long format
    wide_format_cols = ["algorithm", "fault_type", "top1", "top3", "top5"]
    long_format_cols = ["algorithm", "top@k", "fault_type", "success_rate"]

    if all(col in df.columns for col in wide_format_cols):
        # Convert wide format to long format
        df = (
            df.select(
                [
                    pl.col("algorithm"),
                    pl.col("fault_type"),
                    pl.col("top1").alias("top@1"),
                    pl.col("top3").alias("top@3"),
                    pl.col("top5").alias("top@5"),
                ]
            )
            .unpivot(
                index=["algorithm", "fault_type"],
                on=["top@1", "top@3", "top@5"],
                variable_name="top@k",
                value_name="success_rate",
            )
            .filter(pl.col("success_rate").is_not_null())
        )
    elif all(col in df.columns for col in long_format_cols):
        # Already in long format, use as is
        pass
    else:
        logger.warning(
            f"DataFrame must have either wide format columns {wide_format_cols} "
            f"or long format columns {long_format_cols}"
        )
        return

    topk_values = df["top@k"].unique().sort().to_list()
    algorithms = df["algorithm"].unique().sort().to_list()

    if not topk_values or not algorithms:
        logger.warning("No valid top@k or algorithm data found")
        return

    # Layout configuration
    n_algos = len(algorithms)
    n_cols = min(4, n_algos)  # At most 4 columns
    n_rows = (n_algos + n_cols - 1) // n_cols  # Ceiling division

    fig_width = 20
    fig_height = 6 if n_rows <= 2 else 9

    # Create figure with subplots
    fig, axes = plt.subplots(n_rows, n_cols, figsize=(fig_width, fig_height))

    # Ensure axes is always a flat array
    if n_rows == 1 and n_cols == 1:
        axes = [axes]
    elif n_rows == 1 or n_cols == 1:
        axes = axes.flatten() if hasattr(axes, "flatten") else [axes]
    else:
        axes = axes.flatten()

    # Get all fault types for consistent x-axis and create abbreviations
    all_fault_types = df["fault_type"].unique().sort().to_list()

    # Create fault type abbreviations
    fault_type_abbrev = {}
    for fault_type in all_fault_types:
        if fault_type:
            # Create more readable abbreviation by taking meaningful parts
            if "HTTP" in fault_type:
                # For HTTP-related faults, keep HTTP prefix
                parts = fault_type.replace("HTTP", "").replace("Request", "Req").replace("Response", "Resp")
                parts = parts.replace("Replace", "Repl").replace("Abort", "Abrt").replace("Delay", "Del")
                abbrev = "HTTP" + parts
            elif "JVM" in fault_type:
                # For JVM-related faults, keep JVM prefix
                parts = fault_type.replace("JVM", "").replace("Memory", "Mem").replace("Latency", "Lat")
                parts = parts.replace("Exception", "Exc").replace("MySQL", "SQL")
                abbrev = "JVM" + parts
            elif "Network" in fault_type:
                # For Network-related faults, use Net prefix
                parts = fault_type.replace("Network", "").replace("Partition", "Part")
                parts = parts.replace("Bandwidth", "BW").replace("Corrupt", "Corr")
                parts = parts.replace("Delay", "Del")
                abbrev = "Net" + parts
            elif "Container" in fault_type:
                abbrev = fault_type.replace("Container", "Cont")
            elif "Memory" in fault_type:
                abbrev = fault_type.replace("Memory", "Mem")
            else:
                abbrev = fault_type
            fault_type_abbrev[fault_type] = abbrev
        else:
            fault_type_abbrev[fault_type] = "N/A"

    topk_colors = {
        "top@1": BASE_COLORS[0],
        "top@3": BASE_COLORS[1],
        "top@5": BASE_COLORS[2],
    }

    # Calculate bar width and positions for overlapping bars
    bar_width = 0.6
    x_positions = np.arange(len(all_fault_types))

    for idx, algorithm in enumerate(algorithms):
        ax = axes[idx]

        fault_types_abbrev = [fault_type_abbrev[ft] for ft in all_fault_types]

        # Create overlapping bars for each top@k, drawing in order: top@5, top@3, top@1
        # This ensures top@1 (smallest) is drawn on top
        for topk in ["top@5", "top@3", "top@1"]:
            if topk not in topk_values:
                continue

            success_rates = []

            for fault_type in all_fault_types:
                subset = df.filter(
                    (pl.col("top@k") == topk)
                    & (pl.col("algorithm") == algorithm)
                    & (pl.col("fault_type") == fault_type)
                )

                if subset.height > 0:
                    success_rate = subset["success_rate"].to_list()[0]
                    success_rates.append(success_rate)
                else:
                    success_rates.append(0.0)

            ax.bar(
                x_positions,
                success_rates,
                width=bar_width,
                color=topk_colors.get(topk, BASE_COLORS[0]),
                alpha=0.8,
                edgecolor="black",
                linewidth=0.3,
                label=topk.upper() if idx == 0 else "",
            )

        ax.set_title(f"{get_display_algorithm_name(algorithm)}", fontsize=12, fontweight="bold")

        # Set x-axis
        ax.set_xticks(x_positions)
        ax.set_xticklabels(fault_types_abbrev, rotation=45, ha="right", fontsize=6)

        if idx % n_cols == 0:
            ax.set_ylabel("Success Rate", fontsize=11)
        ax.set_ylim(0, 1)  # 0-1 range for success rates

        # Add grid for better readability
        ax.grid(True, alpha=0.3, axis="y")

    # Hide empty subplots if there are fewer algorithms than subplot positions
    total_subplots = n_rows * n_cols
    for idx in range(n_algos, total_subplots):
        if idx < len(axes):
            axes[idx].set_visible(False)

    # Add legend to the figure
    if algorithms:
        fig.legend(
            loc="upper center",
            bbox_to_anchor=(0.5, 0.95),
            ncol=len(topk_values),
            fontsize=10,
        )

    # Adjust layout
    plt.tight_layout()
    if n_rows == 2:
        plt.subplots_adjust(top=0.85, bottom=0.20, left=0.08, right=0.95, hspace=0.55)
    else:
        plt.subplots_adjust(top=0.88, bottom=0.20, left=0.08, right=0.95)

    # Save or display chart
    if output_file:
        parent = output_file.parent
        parent.mkdir(parents=True, exist_ok=True)

        # Determine format from file extension
        file_format = output_file.suffix.lower().lstrip(".")

        # Configure save parameters based on format
        if file_format == "pdf":
            plt.savefig(output_file, format="pdf", bbox_inches="tight")
        else:
            plt.savefig(output_file, dpi=300, bbox_inches="tight")

        logger.info(f"Chart saved to: {output_file} (format: {file_format.upper()})")

    plt.show()


def dataset_anomaly_distribution(
    df: pl.DataFrame,
    output_file: Path | None = None,
) -> None:
    if df.height == 0:
        logger.warning("No data available to generate chart")
        return

    required_cols = ["fault_type", "no", "may", "absolute"]
    missing = [c for c in required_cols if c not in df.columns]
    if missing:
        logger.warning(f"Missing required columns: {missing}")
        return

    if "total_count" in df.columns:
        plot_df = df.sort("total_count", descending=True)
    else:
        plot_df = df

    fault_types = plot_df["fault_type"].to_list()
    n = len(fault_types)
    if n == 0:
        logger.warning("No fault_type data found")
        return

    # Build abbreviated labels for fault types (referencing logic used above)
    fault_type_abbrev: dict[str | None, str] = {}
    for ft in fault_types:
        if ft:
            if "HTTP" in ft:
                parts = (
                    ft.replace("HTTP", "")
                    .replace("Request", "Req")
                    .replace("Response", "Resp")
                    .replace("Replace", "Repl")
                    .replace("Abort", "Abrt")
                    .replace("Delay", "Del")
                )
                abbrev = parts
            elif "JVM" in ft:
                parts = (
                    ft.replace("JVM", "")
                    .replace("Memory", "Mem")
                    .replace("Latency", "Lat")
                    .replace("Exception", "Exc")
                    .replace("MySQL", "SQL")
                    .replace("GarbageCollector", "GC")
                )
                abbrev = "JVM" + parts
            elif "Network" in ft:
                parts = (
                    ft.replace("Network", "")
                    .replace("Partition", "Part")
                    .replace("Bandwidth", "BW")
                    .replace("Corrupt", "Corr")
                )
                abbrev = "Net" + parts
            elif "Container" in ft:
                abbrev = ft.replace("Container", "Cont")
            elif "Memory" in ft:
                abbrev = ft.replace("Memory", "Mem")
            else:
                abbrev = ft
            fault_type_abbrev[ft] = abbrev
        else:
            fault_type_abbrev[ft] = "N/A"
    fault_types_abbrev = [fault_type_abbrev.get(ft, ft or "N/A") for ft in fault_types]

    # Data arrays (fill None with 0)
    def _col_values(name: str) -> list[float]:
        values = plot_df[name].to_list()
        return [float(v) if v is not None else 0.0 for v in values]

    counts_no = _col_values("no")
    counts_may = _col_values("may")
    counts_abs = _col_values("absolute")

    # Combine no and may counts
    counts_no_may = [counts_no[i] + counts_may[i] for i in range(n)]

    # Sort by total count (no + may + absolute) in descending order
    totals = [counts_no_may[i] + counts_abs[i] for i in range(n)]
    order = sorted(range(n), key=lambda i: totals[i], reverse=True)

    fault_types = [fault_types[i] for i in order]
    fault_types_abbrev = [fault_types_abbrev[i] for i in order]
    counts_no_may = [counts_no_may[i] for i in order]
    counts_abs = [counts_abs[i] for i in order]

    # Layout and styling
    x = np.arange(n)
    group_width = 0.8
    bar_w = group_width / 2  # Now we only have 2 bars instead of 3

    colors = {
        "no_may": BASE_COLORS[0],
        "absolute": BASE_COLORS[2],
    }

    # Helper to compute max for range decisions
    max_y = max(max(counts_no_may), max(counts_abs)) if n > 0 else 0

    # Figure size adjusts with number of fault types
    base_width = max(8, n * 0.5)

    # We'll allow compressing values above 100 into the top band (r units)
    compress_band = 20.0  # top band height on the axis for values > 100

    # Prepare possibly transformed heights for plotting
    plot_no_may = counts_no_may
    plot_abs = counts_abs

    if max_y > 100:
        low_scale = (100.0 - compress_band) / 100.0
        high_span = max(1.0, float(max_y - 100.0))

        def _transform(v: float) -> float:
            v = float(v)
            if v <= 100.0:
                return v * low_scale
            return (100.0 - compress_band) + (v - 100.0) / high_span * compress_band

        plot_no_may = [_transform(v) for v in counts_no_may]
        plot_abs = [_transform(v) for v in counts_abs]

    def _plot_bars(ax):
        ax.bar(
            x - bar_w / 2,
            plot_no_may,
            width=bar_w,
            label="No Anomaly",
            color=colors["no_may"],
            edgecolor="black",
            linewidth=0.4,
        )
        ax.bar(
            x + bar_w / 2,
            plot_abs,
            width=bar_w,
            label="Anomaly",
            color=colors["absolute"],
            edgecolor="black",
            linewidth=0.4,
        )

    # Always use a single axis; customize tick labels density
    fig, ax = plt.subplots(figsize=(base_width, 5))
    _plot_bars(ax)

    ax.set_xticks(x)
    ax.set_xticklabels(fault_types_abbrev, rotation=45, ha="right", fontsize=12)
    ax.set_ylabel("Count", fontsize=12)
    ax.grid(True, axis="y", alpha=0.3)

    ticks: list[float] = []
    if max_y <= 100:
        upper = 100 if max_y == 100 else (int(np.ceil(max_y / 20.0)) * 20)
        ax.set_ylim(0, upper)
        ticks = [float(v) for v in range(0, upper + 1, 20)]
        tick_pos = ticks  # identity mapping
    else:
        # Axis stays 0..100, but we'll compress >100 into top band
        ax.set_ylim(0, 100)
        # Build original tick labels
        ticks_low_orig = [float(v) for v in range(0, 101, 20)]
        # Upper labels every 300 up to cover max_y
        extra = max(0.0, float(max_y - 100.0))
        steps = int(np.ceil(extra / 300.0))
        ticks_high_orig = [float(100 + i * 300) for i in range(1, max(1, steps) + 1)]

        # Map to positions
        low_scale = (100.0 - compress_band) / 100.0

        def _pos(v: float) -> float:
            return v * low_scale if v <= 100.0 else (100.0 - compress_band)

        tick_pos_low = [_pos(v) for v in ticks_low_orig]
        # For high ticks, spread linearly in the compressed band
        high_span = max(1.0, float(max_y - 100.0))
        tick_pos_high = [(100.0 - compress_band) + (v - 100.0) / high_span * compress_band for v in ticks_high_orig]

        ticks = ticks_low_orig + ticks_high_orig
        tick_pos = tick_pos_low + tick_pos_high

    ax.set_yticks(np.array(tick_pos, dtype=float))
    ax.set_yticklabels([str(int(t)) for t in ticks])

    ax.legend(ncol=2, loc="upper center", bbox_to_anchor=(0.5, 1.12), fontsize=10)

    plt.tight_layout()
    plt.subplots_adjust(top=0.85, bottom=0.22)

    # Save or show
    if output_file:
        parent = output_file.parent
        parent.mkdir(parents=True, exist_ok=True)

        file_format = output_file.suffix.lower().lstrip(".")
        if file_format == "pdf":
            plt.savefig(output_file, format="pdf", bbox_inches="tight")
        else:
            plt.savefig(output_file, dpi=300, bbox_inches="tight")
        logger.info(f"Chart saved to: {output_file} (format: {file_format.upper()})")

    plt.show()
