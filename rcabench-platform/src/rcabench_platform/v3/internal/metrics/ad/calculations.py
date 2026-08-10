"""
Golden Signal metric calculation functions
Includes basic calculation functions and logic for the four golden signals
"""

import numpy as np

from .types import (
    ErrorData,
    ErrorSignals,
    LatencyData,
    LatencySignals,
    NumericList,
    SaturationData,
    SaturationSignals,
    TrafficData,
    TrafficSignals,
)


# ===== Basic Calculation Functions =====
def calculate_percentile(data: NumericList, percentile: float) -> float:
    """Calculate percentile"""
    if not data:
        return 0.0
    return float(np.percentile(data, percentile))


def calculate_average(data: NumericList) -> float:
    """Calculate average value"""
    if not data:
        return 0.0
    return float(np.mean(data))


def calculate_rate(count: int | float, duration_seconds: int | float) -> float:
    """Calculate rate (count/time)"""
    if duration_seconds <= 0:
        return 0.0
    return float(count / duration_seconds)


def calculate_ratio(numerator: int | float, denominator: int | float) -> float:
    """Calculate ratio"""
    if denominator == 0:
        return 0.0
    return float(numerator / denominator)


def calculate_percentage(numerator: int | float, denominator: int | float) -> float:
    """Calculate percentage"""
    return calculate_ratio(numerator, denominator) * 100.0


# ===== Golden Signal Metric Calculation Functions =====
def compute_latency_signals(data: LatencyData) -> LatencySignals:
    """
    Calculate latency metrics

    Args:
        data: Dictionary containing successful request latency data

    Returns:
        Dictionary containing various latency statistics
    """
    latencies = data["successful_latencies"]
    return {
        "p99": calculate_percentile(latencies, 99),
        "p95": calculate_percentile(latencies, 95),
        "p50": calculate_percentile(latencies, 50),
        "average": calculate_average(latencies),
    }


def compute_traffic_signals(data: TrafficData) -> TrafficSignals:
    """
    Calculate traffic metrics

    Args:
        data: Dictionary containing total request count and time window

    Returns:
        Dictionary containing requests per second
    """
    rps = calculate_rate(data["total_count"], data["duration_seconds"])
    return {"requests_per_second": rps}


def compute_error_signals(data: ErrorData) -> ErrorSignals:
    """
    Calculate error metrics

    Args:
        data: Dictionary containing error count, total count, and time window

    Returns:
        Dictionary containing error rate and errors per second
    """
    error_rate = calculate_percentage(data["error_count"], data["total_count"])
    eps = calculate_rate(data["error_count"], data["duration_seconds"])
    return {"error_rate_percent": error_rate, "errors_per_second": eps}


def compute_saturation_signals(data: SaturationData) -> SaturationSignals:
    """
    Calculate saturation metrics

    Args:
        data: Dictionary containing resource usage percentage list

    Returns:
        Dictionary containing average, max, and p99 usage percentage
    """
    percentages = data["usage_percentages"]
    if not percentages:
        return {
            "average_usage_percent": 0.0,
            "max_usage_percent": 0.0,
            "p99_usage_percent": 0.0,
        }

    return {
        "average_usage_percent": calculate_average(percentages),
        "max_usage_percent": max(percentages),
        "p99_usage_percent": calculate_percentile(percentages, 99),
    }
