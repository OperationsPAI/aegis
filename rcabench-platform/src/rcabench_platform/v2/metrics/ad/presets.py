"""
Preset configuration functions
Provide ready-to-use detection configurations for different types of metrics
"""

from collections.abc import Callable

from .configs import (
    EnhancedLatencyConfig,
    MetricDetectionConfig,
    StatisticalConfig,
    SuccessRateConfig,
    ThresholdConfig,
    TrendConfig,
)
from .types import DetectionMethod, MetricType


# ===== Latency Metric Preset Configurations =====
def create_latency_config() -> MetricDetectionConfig:
    """
    Create default configuration for latency metrics

    Suitable for general web service latency monitoring
    """
    return MetricDetectionConfig(
        metric_type=MetricType.LATENCY,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=1000.0,  # 1 second
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(z_score_threshold=2.5, enabled=True),
            DetectionMethod.ENHANCED_LATENCY: EnhancedLatencyConfig(percentile_type="avg", enabled=True),
        },
    )


def create_strict_latency_config() -> MetricDetectionConfig:
    """
    Create strict configuration for latency metrics

    Suitable for latency-sensitive key services
    """
    return MetricDetectionConfig(
        metric_type=MetricType.LATENCY,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=500.0,  # 0.5 second
                severity_critical_ratio=1.5,
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(
                z_score_threshold=2.0,  # more sensitive
                enabled=True,
            ),
            DetectionMethod.TREND: TrendConfig(
                spike_threshold_multiplier=1.5,  # more sensitive
                enabled=True,
            ),
            DetectionMethod.ENHANCED_LATENCY: EnhancedLatencyConfig(
                percentile_type="p95",
                hard_timeout_threshold=10.0,  # stricter timeout
                enabled=True,
            ),
        },
    )


def create_relaxed_latency_config() -> MetricDetectionConfig:
    """
    Create relaxed configuration for latency metrics

    Suitable for batch processing or services with high latency tolerance
    """
    return MetricDetectionConfig(
        metric_type=MetricType.LATENCY,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=5000.0,  # 5 seconds
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(
                z_score_threshold=3.5,  # more relaxed
                enabled=True,
            ),
        },
    )


# ===== Traffic Metric Preset Configurations =====
def create_traffic_config() -> MetricDetectionConfig:
    """
    Create default configuration for traffic metrics

    Suitable for general request traffic monitoring
    """
    return MetricDetectionConfig(
        metric_type=MetricType.TRAFFIC,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                low_threshold=1.0,  # at least 1 RPS
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(z_score_threshold=3.0, enabled=True),
            DetectionMethod.TREND: TrendConfig(spike_threshold_multiplier=3.0, enabled=True),
        },
    )


def create_high_traffic_config() -> MetricDetectionConfig:
    """
    Create configuration for high-traffic services

    Suitable for high-concurrency service traffic monitoring
    """
    return MetricDetectionConfig(
        metric_type=MetricType.TRAFFIC,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                low_threshold=100.0,  # at least 100 RPS
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(
                z_score_threshold=2.5,  # more sensitive
                enabled=True,
            ),
            DetectionMethod.TREND: TrendConfig(
                spike_threshold_multiplier=2.5,
                window_size=15,  # larger window
                enabled=True,
            ),
        },
    )


# ===== Error Rate Metric Preset Configurations =====
def create_error_config() -> MetricDetectionConfig:
    """
    Create default configuration for error rate metrics

    Suitable for general error rate monitoring
    """
    return MetricDetectionConfig(
        metric_type=MetricType.ERROR,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=5.0,  # 5% error rate
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(z_score_threshold=2.0, enabled=True),
            DetectionMethod.SUCCESS_RATE: SuccessRateConfig(enabled=True),
        },
    )


def create_strict_error_config() -> MetricDetectionConfig:
    """
    Create strict configuration for error rate metrics

    Suitable for services with high availability requirements
    """
    return MetricDetectionConfig(
        metric_type=MetricType.ERROR,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=1.0,  # 1% error rate triggers alert
                severity_critical_ratio=1.5,
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(
                z_score_threshold=1.5,  # more sensitive
                enabled=True,
            ),
            DetectionMethod.TREND: TrendConfig(
                spike_threshold_multiplier=1.2,  # very sensitive
                enabled=True,
            ),
            DetectionMethod.SUCCESS_RATE: SuccessRateConfig(
                min_rate_drop=0.01,  # 1% drop triggers detection
                significance_threshold=0.1,  # relaxed significance requirement
                enabled=True,
            ),
        },
    )


# ===== Saturation Metric Preset Configurations =====
def create_saturation_config() -> MetricDetectionConfig:
    """
    Create default configuration for saturation metrics

    Suitable for general resource usage monitoring
    """
    return MetricDetectionConfig(
        metric_type=MetricType.SATURATION,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=80.0,  # 80% usage
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(z_score_threshold=2.5, enabled=True),
            DetectionMethod.TREND: TrendConfig(spike_threshold_multiplier=2.0, enabled=True),
        },
    )


def create_strict_saturation_config() -> MetricDetectionConfig:
    """
    Create strict configuration for saturation metrics

    Suitable for key services sensitive to resource usage
    """
    return MetricDetectionConfig(
        metric_type=MetricType.SATURATION,
        enabled_methods={
            DetectionMethod.THRESHOLD: ThresholdConfig(
                high_threshold=70.0,  # 70% triggers alert
                severity_critical_ratio=1.3,
                enabled=True,
            ),
            DetectionMethod.STATISTICAL: StatisticalConfig(
                z_score_threshold=2.0,  # more sensitive
                enabled=True,
            ),
            DetectionMethod.TREND: TrendConfig(
                spike_threshold_multiplier=1.5,  # more sensitive
                enabled=True,
            ),
        },
    )


# ===== Scenario-based Configurations =====
def create_microservice_config(metric_type: MetricType) -> MetricDetectionConfig:
    """
    Create configuration for microservice scenarios

    Args:
        metric_type: Metric type

    Returns:
        Detection configuration suitable for microservice architecture
    """
    if metric_type == MetricType.LATENCY:
        return create_strict_latency_config()
    elif metric_type == MetricType.TRAFFIC:
        return create_traffic_config()
    elif metric_type == MetricType.ERROR:
        return create_strict_error_config()
    elif metric_type == MetricType.SATURATION:
        return create_saturation_config()
    else:
        raise ValueError(f"Unsupported metric type: {metric_type}")


def create_batch_processing_config(metric_type: MetricType) -> MetricDetectionConfig:
    """
    Create configuration for batch processing scenarios

    Args:
        metric_type: Metric type

    Returns:
        Detection configuration suitable for batch processing systems
    """
    if metric_type == MetricType.LATENCY:
        return create_relaxed_latency_config()
    elif metric_type == MetricType.TRAFFIC:
        # Batch systems focus on trend changes
        return MetricDetectionConfig(
            metric_type=MetricType.TRAFFIC,
            enabled_methods={
                DetectionMethod.TREND: TrendConfig(
                    window_size=20,  # larger time window
                    spike_threshold_multiplier=3.0,
                    enabled=True,
                ),
                DetectionMethod.STATISTICAL: StatisticalConfig(z_score_threshold=3.0, enabled=True),
            },
        )
    elif metric_type == MetricType.ERROR:
        return create_error_config()
    elif metric_type == MetricType.SATURATION:
        return create_strict_saturation_config()  # Batch processing usually needs a lot of resources
    else:
        raise ValueError(f"Unsupported metric type: {metric_type}")


def create_development_config(metric_type: MetricType) -> MetricDetectionConfig:
    """
    Create configuration for development environments

    Args:
        metric_type: Metric type

    Returns:
        Relaxed detection configuration suitable for development environments
    """
    if metric_type == MetricType.LATENCY:
        return MetricDetectionConfig(
            metric_type=MetricType.LATENCY,
            enabled_methods={
                DetectionMethod.THRESHOLD: ThresholdConfig(
                    high_threshold=10000.0,  # 10 seconds, very relaxed
                    enabled=True,
                )
            },
        )
    elif metric_type == MetricType.TRAFFIC:
        return MetricDetectionConfig(
            metric_type=MetricType.TRAFFIC,
            enabled_methods={
                DetectionMethod.THRESHOLD: ThresholdConfig(
                    low_threshold=0.1,  # very low threshold
                    enabled=True,
                )
            },
        )
    elif metric_type == MetricType.ERROR:
        return MetricDetectionConfig(
            metric_type=MetricType.ERROR,
            enabled_methods={
                DetectionMethod.THRESHOLD: ThresholdConfig(
                    high_threshold=20.0,  # 20% error rate
                    enabled=True,
                )
            },
        )
    elif metric_type == MetricType.SATURATION:
        return MetricDetectionConfig(
            metric_type=MetricType.SATURATION,
            enabled_methods={
                DetectionMethod.THRESHOLD: ThresholdConfig(
                    high_threshold=95.0,  # 95% usage
                    enabled=True,
                )
            },
        )
    else:
        raise ValueError(f"Unsupported metric type: {metric_type}")


# ===== Configuration Factory =====
def get_preset_config(metric_type: MetricType, scenario: str = "default") -> MetricDetectionConfig:
    """
    Get preset configuration

    Args:
        metric_type: Metric type
        scenario: Scenario type ("default", "strict", "relaxed", "microservice",
                 "batch_processing", "development", "high_traffic")

    Returns:
        Corresponding detection configuration
    """
    scenario_map: dict[
        str,
        dict[MetricType, Callable[[], MetricDetectionConfig]] | Callable[[MetricType], MetricDetectionConfig],
    ] = {
        "default": {
            MetricType.LATENCY: create_latency_config,
            MetricType.TRAFFIC: create_traffic_config,
            MetricType.ERROR: create_error_config,
            MetricType.SATURATION: create_saturation_config,
        },
        "strict": {
            MetricType.LATENCY: create_strict_latency_config,
            MetricType.TRAFFIC: create_high_traffic_config,
            MetricType.ERROR: create_strict_error_config,
            MetricType.SATURATION: create_strict_saturation_config,
        },
        "relaxed": {
            MetricType.LATENCY: create_relaxed_latency_config,
            MetricType.TRAFFIC: create_traffic_config,
            MetricType.ERROR: create_error_config,
            MetricType.SATURATION: create_saturation_config,
        },
        "microservice": create_microservice_config,
        "batch_processing": create_batch_processing_config,
        "development": create_development_config,
        "high_traffic": {
            MetricType.LATENCY: create_latency_config,
            MetricType.TRAFFIC: create_high_traffic_config,
            MetricType.ERROR: create_error_config,
            MetricType.SATURATION: create_saturation_config,
        },
    }

    if scenario not in scenario_map:
        raise ValueError(f"Unknown scenario: {scenario}")

    config_source = scenario_map[scenario]

    if callable(config_source):
        return config_source(metric_type)
    elif metric_type in config_source:
        return config_source[metric_type]()
    else:
        raise ValueError(f"Unsupported metric type {metric_type} for scenario {scenario}")
