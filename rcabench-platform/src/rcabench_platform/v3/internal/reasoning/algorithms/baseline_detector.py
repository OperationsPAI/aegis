"""Baseline-aware state detection using Z-score and percentile thresholds.

This module addresses Gap 2.1 from implementation_gaps.md by replacing
static global thresholds with baseline-relative detection.

Two detection methods are supported:
1. Z-score based: For Gaussian-distributed metrics (CPU, memory utilization)
   Z = (value - baseline_mean) / baseline_std

2. Percentile based: For heavy-tailed metrics (latency, error counts)
   Compares against baseline percentiles (P50, P90, P99, P99.9)
   More robust for non-Gaussian distributions.
"""

from typing import Any

import numpy as np


class BaselineStatistics:
    """Stores baseline statistics for a metric."""

    def __init__(self, mean: float, std: float, count: int = 0):
        """Initialize baseline statistics.

        Args:
            mean: Baseline mean value
            std: Baseline standard deviation
            count: Number of samples (for confidence)
        """
        self.mean = mean
        self.std = std
        self.count = count


class PercentileStatistics:
    """Stores percentile-based statistics for a metric.

    More robust for heavy-tailed distributions like latency.
    """

    def __init__(
        self,
        p50: float,
        p90: float,
        p99: float,
        p999: float,
        count: int = 0,
    ):
        """Initialize percentile statistics.

        Args:
            p50: 50th percentile (median)
            p90: 90th percentile
            p99: 99th percentile
            p999: 99.9th percentile
            count: Number of samples (for confidence)
        """
        self.p50 = p50
        self.p90 = p90
        self.p99 = p99
        self.p999 = p999
        self.count = count


class ZScoreThresholds:
    """Z-score based thresholds for anomaly detection.

    These are in units of standard deviations from the baseline mean.
    """

    # General anomaly thresholds (Z-scores)
    CRITICAL_Z = 3.0  # 3 sigma (99.7% confidence)
    WARNING_Z = 2.0  # 2 sigma (95% confidence)
    MODERATE_Z = 1.5  # 1.5 sigma (86.6% confidence)

    # Minimum std dev to avoid division by zero
    MIN_STD = 1e-6


class PercentileThresholds:
    """Percentile-based thresholds for anomaly detection.

    More robust for heavy-tailed distributions (latency, error counts).
    """

    # Multipliers for percentile-based detection
    # Value > baseline_p99 * CRITICAL_MULTIPLIER = critical anomaly
    CRITICAL_MULTIPLIER = 2.0  # 2x above P99
    WARNING_MULTIPLIER = 1.5  # 1.5x above P99
    MODERATE_MULTIPLIER = 1.2  # 1.2x above P99

    # Absolute percentile thresholds
    # Value > baseline_p999 = critical (exceeds 99.9th percentile)
    CRITICAL_PERCENTILE = "p999"
    WARNING_PERCENTILE = "p99"
    MODERATE_PERCENTILE = "p90"


class BaselineAwareDetector:
    """Detector that uses baseline statistics for Z-score based anomaly detection.

    Best for Gaussian-distributed metrics like CPU/memory utilization.
    """

    def __init__(self, baseline_stats: dict[str, BaselineStatistics] | None = None):
        """Initialize baseline-aware detector.

        Args:
            baseline_stats: Dictionary mapping metric names to their baseline statistics
                           Format: {"metric_name": BaselineStatistics(mean, std, count)}

        Raises:
            TypeError: If baseline_stats is not None and not a dict
        """
        if baseline_stats is not None and not isinstance(baseline_stats, dict):
            raise TypeError(f"baseline_stats must be a dict or None, got {type(baseline_stats)}")
        self.baseline_stats = baseline_stats if baseline_stats is not None else {}

    def calculate_z_score(self, metric_name: str, value: float) -> float:
        """Calculate Z-score for a metric value relative to baseline.

        Z = (value - baseline_mean) / baseline_std

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            Z-score (number of standard deviations from baseline mean)
            Returns 0.0 if no baseline statistics available
        """
        if metric_name not in self.baseline_stats:
            return 0.0

        stats = self.baseline_stats[metric_name]

        # Avoid division by zero
        std = max(stats.std, ZScoreThresholds.MIN_STD)

        z_score = (value - stats.mean) / std
        return z_score

    def is_critical_anomaly(self, metric_name: str, value: float) -> bool:
        """Check if metric value is critically anomalous (Z > 3).

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            True if Z-score > CRITICAL_Z (3 sigma)
        """
        z = self.calculate_z_score(metric_name, value)
        return z > ZScoreThresholds.CRITICAL_Z

    def is_warning_anomaly(self, metric_name: str, value: float) -> bool:
        """Check if metric value is at warning level (Z > 2).

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            True if Z-score > WARNING_Z (2 sigma)
        """
        z = self.calculate_z_score(metric_name, value)
        return z > ZScoreThresholds.WARNING_Z

    def is_moderate_anomaly(self, metric_name: str, value: float) -> bool:
        """Check if metric value is moderately anomalous (Z > 1.5).

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            True if Z-score > MODERATE_Z (1.5 sigma)
        """
        z = self.calculate_z_score(metric_name, value)
        return z > ZScoreThresholds.MODERATE_Z


class PercentileAwareDetector:
    """Detector that uses percentile-based thresholds for anomaly detection.

    Best for heavy-tailed distributions like latency, error counts.
    More robust than Z-score for non-Gaussian metrics.
    """

    def __init__(self, percentile_stats: dict[str, PercentileStatistics] | None = None):
        """Initialize percentile-aware detector.

        Args:
            percentile_stats: Dictionary mapping metric names to their percentile statistics
                             Format: {"metric_name": PercentileStatistics(p50, p90, p99, p999, count)}

        Raises:
            TypeError: If percentile_stats is not None and not a dict
        """
        if percentile_stats is not None and not isinstance(percentile_stats, dict):
            raise TypeError(f"percentile_stats must be a dict or None, got {type(percentile_stats)}")
        self.percentile_stats = percentile_stats if percentile_stats is not None else {}

    def is_critical_anomaly(self, metric_name: str, value: float) -> bool:
        """Check if metric value is critically anomalous (exceeds P99.9 or 2x P99).

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            True if value exceeds P99.9 or is 2x above P99
        """
        if metric_name not in self.percentile_stats:
            return False

        stats = self.percentile_stats[metric_name]

        # Critical if exceeds P99.9 or 2x P99
        return value > stats.p999 or value > stats.p99 * PercentileThresholds.CRITICAL_MULTIPLIER

    def is_warning_anomaly(self, metric_name: str, value: float) -> bool:
        """Check if metric value is at warning level (exceeds P99 or 1.5x P99).

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            True if value exceeds P99 or is 1.5x above P99
        """
        if metric_name not in self.percentile_stats:
            return False

        stats = self.percentile_stats[metric_name]

        # Warning if exceeds P99 or 1.5x P99
        return value > stats.p99 or value > stats.p99 * PercentileThresholds.WARNING_MULTIPLIER

    def is_moderate_anomaly(self, metric_name: str, value: float) -> bool:
        """Check if metric value is moderately anomalous (exceeds P90 or 1.2x P99).

        Args:
            metric_name: Name of the metric
            value: Current metric value

        Returns:
            True if value exceeds P90 or is 1.2x above P99
        """
        if metric_name not in self.percentile_stats:
            return False

        stats = self.percentile_stats[metric_name]

        # Moderate if exceeds P90 or 1.2x P99
        return value > stats.p90 or value > stats.p99 * PercentileThresholds.MODERATE_MULTIPLIER


def compute_baseline_statistics(metric_values: np.ndarray) -> BaselineStatistics:
    """Compute baseline statistics from historical metric values.

    Args:
        metric_values: Array of historical metric values

    Returns:
        BaselineStatistics with mean, std, and count
    """
    if len(metric_values) == 0:
        return BaselineStatistics(mean=0.0, std=0.0, count=0)

    # Remove NaN values
    clean_values = metric_values[~np.isnan(metric_values)]

    if len(clean_values) == 0:
        return BaselineStatistics(mean=0.0, std=0.0, count=0)

    mean = float(np.mean(clean_values))
    std = float(np.std(clean_values))
    count = len(clean_values)

    return BaselineStatistics(mean=mean, std=std, count=count)


def compute_percentile_statistics(metric_values: np.ndarray) -> PercentileStatistics:
    """Compute percentile statistics from historical metric values.

    More robust than mean/std for heavy-tailed distributions.

    Args:
        metric_values: Array of historical metric values

    Returns:
        PercentileStatistics with p50, p90, p99, p999, and count
    """
    if len(metric_values) == 0:
        return PercentileStatistics(p50=0.0, p90=0.0, p99=0.0, p999=0.0, count=0)

    # Remove NaN values
    clean_values = metric_values[~np.isnan(metric_values)]

    if len(clean_values) == 0:
        return PercentileStatistics(p50=0.0, p90=0.0, p99=0.0, p999=0.0, count=0)

    p50 = float(np.percentile(clean_values, 50))
    p90 = float(np.percentile(clean_values, 90))
    p99 = float(np.percentile(clean_values, 99))
    p999 = float(np.percentile(clean_values, 99.9))
    count = len(clean_values)

    return PercentileStatistics(p50=p50, p90=p90, p99=p99, p999=p999, count=count)


def extract_metric_value(metric: Any) -> float:
    """Extract scalar value from metric (handle both arrays and scalars).

    Args:
        metric: Metric value (array or scalar)

    Returns:
        Aggregated scalar value (max value if array, else value itself)
    """
    if isinstance(metric, np.ndarray):
        return float(np.max(metric)) if len(metric) > 0 else 0.0
    return float(metric) if metric is not None else 0.0


def calculate_series_stats(values: np.ndarray) -> tuple[float, float, float]:
    """Calculate mean, std, and CV for a time series.

    Args:
        values: Time series values

    Returns:
        Tuple of (mean, std, cv) where cv is coefficient of variation (std/mean)
    """
    if len(values) == 0:
        return 0.0, 0.0, 0.0

    mean = float(np.mean(values))
    std = float(np.std(values))

    if mean > 1e-6:  # Avoid division by zero
        cv = std / mean
    else:
        cv = 0.0

    return mean, std, cv


def get_adaptive_threshold(
    baseline_mean: float,
    cv: float,
    cv_scale: float = 0.15,
    cv_range: float = 3.0,
    cv_base: float = 1.5,
    latency_sensitivity: float = 0.6,
    reference_latency: float = 0.2,
) -> float:
    """Calculate adaptive threshold for latency anomaly detection.

    Core principle: smaller baseline → tolerate larger multiplier.
    - 1ms → 100ms (100x): small absolute change, user barely notices
    - 100ms → 1s (10x): large absolute change, severely impacts UX

    Formula: threshold = (cv_base + cv_range * f(cv)) * (reference/baseline)^sensitivity
    - Volatility component: f(cv) = 1 - exp(-cv/cv_scale)
    - Latency-level component: (reference/baseline)^sensitivity

    Args:
        baseline_mean: Baseline latency mean (seconds)
        cv: Baseline coefficient of variation (std/mean)
        cv_base: Minimum threshold for stable data (default: 1.5)
                 Smaller=stricter, larger=looser
        cv_range: Maximum additional threshold from volatility (default: 3.0)
                  Total range: [cv_base, cv_base + cv_range]
        cv_scale: Volatility sensitivity (default: 0.15)
                  Smaller=saturates faster, larger=saturates slower
        latency_sensitivity: Latency-level adjustment strength (default: 0.5)
                            Larger=baseline impact stronger
        reference_latency: Neutral latency level in seconds (default: 0.3)
                          Baseline below this gets higher threshold

    Returns:
        Threshold multiplier (typically 1.5-40.0)

    Examples:
        1ms baseline (CV=0.05):   threshold ≈ 40.0 (allows ~100x+)
        300ms baseline (CV=0.2):  threshold ≈ 3.5 (allows ~5-10x)
        5s baseline (CV=0.3):     threshold ≈ 1.8 (allows ~2-3x)
    """
    # Volatility component: cv_base + cv_range * (1 - e^(-cv/cv_scale))
    cv_component = cv_base + cv_range * (1.0 - float(np.exp(-cv / cv_scale)))

    # Latency-level component: (reference/baseline)^sensitivity
    safe_baseline = max(baseline_mean, 1e-6)
    latency_boost = float((reference_latency / safe_baseline) ** latency_sensitivity)

    # Ensure threshold is at least 1.0 (otherwise normal values would be flagged)
    return max(float(cv_component * latency_boost), 1.0)
