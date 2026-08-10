"""
Anomaly detection configuration classes
Define independent configuration parameters
for each detection method
"""

from dataclasses import dataclass, field

from .types import DetectionMethod, MetricType


# ===== Base Configuration Class =====
@dataclass
class DetectionConfig:
    """Base class for detection configuration"""

    enabled: bool = True


# ===== Specific Detection Method Configurations =====
@dataclass
class ThresholdConfig(DetectionConfig):
    """
    Threshold detection configuration

    Suitable for metrics with explicit
    upper and lower limits
    """

    high_threshold: float | None = None
    low_threshold: float | None = None
    severity_critical_ratio: float = 2.0
    severity_high_ratio: float = 1.5
    severity_medium_ratio: float = 1.2


@dataclass
class StatisticalConfig(DetectionConfig):
    """
    Statistical detection configuration

    Statistical anomaly detection based on
    Z-score and IQR
    """

    z_score_threshold: float = 3.0
    iqr_multiplier: float = 1.5
    severity_zscore_critical: float = 4.0
    severity_zscore_high: float = 3.5
    severity_zscore_medium: float = 3.0
    severity_confidence_high: float = 0.8
    severity_confidence_medium: float = 0.5


@dataclass
class TrendConfig(DetectionConfig):
    """
    Trend detection configuration

    Detects spike and drop
    anomaly patterns
    """

    window_size: int = 10
    spike_threshold_multiplier: float = 2.0
    severity_spike_critical: float = 4.0
    severity_spike_high: float = 3.0
    severity_spike_medium: float = 2.0


@dataclass
class EnhancedLatencyConfig(DetectionConfig):
    """
    Enhanced latency detection configuration

    Advanced detection methods specifically
    for latency metrics
    """

    # Basic configuration
    percentile_type: str = "avg"  # Select percentile type for detection:
    # "avg"(average), "p90"(90th percentile),
    # "p95"(95th percentile), "p99"(99th percentile)
    hard_timeout_threshold: float = 15.0  # Hard timeout threshold(seconds),
    # exceeding this value directly determines as critical anomaly,
    # used to capture extreme timeout situations

    # Stability threshold configuration:
    # used to evaluate latency stability under different percentiles
    stability_thresholds: dict[str, float] = field(
        default_factory=lambda: {
            "avg": 2.0,  # Average latency stability threshold(seconds),
            # exceeding this value considers latency unstable
            "p90": 3.0,  # P90 latency stability threshold(seconds),
            # used to detect high percentile latency fluctuations
            "p95": 5.0,  # P95 latency stability threshold(seconds),
            # detect stricter latency requirements
            "p99": 6.0,  # P99 latency stability threshold(seconds),
            # detect extreme latency situations
        }
    )

    # === Baseline filtering rule configuration ===
    enable_baseline_filtering: bool = (
        True  # Whether to enable baseline filtering,
        # used to filter out anomaly detection with high baseline latency
    )
    baseline_avg_threshold: float = 1.0  # Baseline average latency threshold
    # (seconds), baseline data exceeding this value will be filtered
    # to avoid false positives
    baseline_p99_threshold: float = (
        5.0  # Baseline P99 latency threshold(seconds),
        # used to filter data with extremely high latency in baseline
    )

    # === Adaptive threshold configuration ===
    enable_adaptive_thresholds: bool = True  # Whether to enable adaptive
    # threshold adjustment, dynamically adjust detection sensitivity
    # based on baseline size
    small_baseline_threshold: float = 0.5  # Small baseline latency threshold
    # (seconds), when baseline latency is less than this value,
    # use absolute threshold detection
    absolute_anomaly_threshold: float = 2.0  # Absolute anomaly threshold
    # (seconds), used for absolute latency anomaly detection
    # in small baseline situations

    # === Base multiplier threshold configuration ===
    # Base multiplier thresholds for different percentiles,
    # used to calculate anomaly detection multipliers
    # relative to baseline
    base_multiplier_thresholds: dict[str, float] = field(
        default_factory=lambda: {
            "avg": 3.0,  # Average latency base multiplier:
            # triggers anomaly detection when current latency
            # exceeds 3x baseline
            "p90": 6.0,  # P90 latency base multiplier:
            # slightly higher multiplier considering natural
            # fluctuations of high percentiles
            "p95": 7.5,  # P95 latency base multiplier:
            # higher tolerance, reduces false positives for P95 latency
            "p99": 8.0,  # P99 latency base multiplier:
            # highest tolerance, P99 itself is extreme value statistics
        }
    )

    """
    # === Percentile baseline filtering thresholds ===
    # Skip anomaly detection when baseline latency exceeds these thresholds to avoid false positives
    # in high latency environments
    # Average latency baseline filtering threshold(seconds), skip detection when baseline exceeds 1 second
     # P90 latency baseline filtering threshold(seconds), skip detection when baseline exceeds 1.5 seconds
     # P95 latency baseline filtering threshold(seconds), skip detection when baseline exceeds 2 seconds
      # P99 latency baseline filtering threshold(seconds), skip detection when baseline exceeds 3 seconds
    """

    percentile_baseline_thresholds: dict[str, float] = field(
        default_factory=lambda: {
            "avg": 1.0,
            "p90": 1.5,
            "p95": 2.0,
            "p99": 3.0,
        }
    )

    """
    # === Percentile absolute anomaly thresholds ===
    # Use absolute thresholds for anomaly detection when baseline latency is small, avoid false positives
    # from high multipliers of small values
    # Average latency absolute anomaly threshold(seconds), directly determine as anomaly when current
    # latency exceeds 2 seconds
    # P90 latency absolute anomaly threshold(seconds), adapt to P90 statistical characteristics
    # P95 latency absolute anomaly threshold(seconds), higher absolute threshold
    # P99 latency absolute anomaly threshold(seconds), highest absolute threshold, avoid natural extremes of P99
    """
    percentile_absolute_thresholds: dict[str, float] = field(
        default_factory=lambda: {
            "avg": 2.0,
            "p90": 4.0,
            "p95": 4.5,
            "p99": 5.0,
        }
    )

    # === Percentile minimum multiplier limits ===
    # Ensure that even under adaptive adjustment, detection multipliers will not be lower than these minimum values,
    # guaranteeing the lower limit of detection sensitivity
    percentile_min_multipliers: dict[str, float] = field(
        default_factory=lambda: {
            "avg": 1.5,  # Average latency minimum detection multiplier, at least 1.5x to trigger anomaly
            "p90": 4,  # P90 latency minimum detection multiplier, considering P90 volatility
            "p95": 4.5,  # P95 latency minimum detection multiplier, more conservative detection strategy
            "p99": 5.0,  # P99 latency minimum detection multiplier, most conservative detection, avoid P99 fp
        }
    )


@dataclass
class SuccessRateConfig(DetectionConfig):
    """
    Success rate detection configuration

    Detects success rate drops based on
    statistical significance tests
    """

    min_normal_count: int = 10
    min_abnormal_count: int = 5
    min_rate_drop: float = 0.03
    significance_threshold: float = 0.05
    min_relative_drop: float = 0.1


# ===== Composite Configuration Class =====
@dataclass
class MetricDetectionConfig:
    """
    Metric detection configuration

    Combines multiple detection methods to
    serve specific metric types
    """

    metric_type: MetricType
    enabled_methods: dict[DetectionMethod, DetectionConfig] = field(default_factory=dict)

    def get_method_config(self, method: DetectionMethod) -> DetectionConfig | None:
        """Get the configuration for the specified method"""
        return self.enabled_methods.get(method)

    def is_method_enabled(self, method: DetectionMethod) -> bool:
        """Check if the method is enabled"""
        config = self.enabled_methods.get(method)
        return config is not None and config.enabled

    def add_method(self, method: DetectionMethod, config: DetectionConfig) -> None:
        """Add a detection method"""
        self.enabled_methods[method] = config

    def remove_method(self, method: DetectionMethod) -> None:
        """Remove a detection method"""
        self.enabled_methods.pop(method, None)

    def enable_method(self, method: DetectionMethod) -> bool:
        """Enable a detection method"""
        config = self.enabled_methods.get(method)
        if config is not None:
            config.enabled = True
            return True
        return False

    def disable_method(self, method: DetectionMethod) -> bool:
        """Disable a detection method"""
        config = self.enabled_methods.get(method)
        if config is not None:
            config.enabled = False
            return True
        return False
