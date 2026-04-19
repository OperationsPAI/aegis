"""
Core type definitions for the anomaly detection system
Includes all enums, TypedDicts, and basic data structures
"""

from enum import Enum
from typing import Any, TypedDict

# ===== Basic Type Aliases =====
NumericList = list[float | int]


# ===== Metric Type Enum =====
class MetricType(Enum):
    """Metric type enum"""

    LATENCY = "latency"
    TRAFFIC = "traffic"
    ERROR = "error"
    SATURATION = "saturation"


class AnomalyType(Enum):
    """Anomaly type enum"""

    NONE = "none"
    THRESHOLD_HIGH = "threshold_high"
    THRESHOLD_LOW = "threshold_low"
    STATISTICAL_OUTLIER = "statistical_outlier"
    TREND_SPIKE = "trend_spike"
    TREND_DROP = "trend_drop"
    SUCCESS_RATE_DROP = "success_rate_drop"


class AnomalySeverity(Enum):
    """Anomaly severity"""

    LOW = "low"
    MEDIUM = "medium"
    HIGH = "high"
    CRITICAL = "critical"


class DetectionMethod(Enum):
    """Anomaly detection method enum"""

    THRESHOLD = "threshold"
    STATISTICAL = "statistical"
    TREND = "trend"
    ENHANCED_LATENCY = "enhanced_latency"
    SUCCESS_RATE = "success_rate"


# ===== Metric Calculation Related Data Structures =====
class LatencyData(TypedDict):
    """Raw latency metric data"""

    successful_latencies: NumericList


class LatencySignals(TypedDict):
    """Latency metric calculation result"""

    p99: float
    p95: float
    p50: float
    average: float


class TrafficData(TypedDict):
    """Raw traffic metric data"""

    total_count: int
    duration_seconds: int | float


class TrafficSignals(TypedDict):
    """Traffic metric calculation result"""

    requests_per_second: float


class ErrorData(TypedDict):
    """Raw error metric data"""

    error_count: int
    total_count: int
    duration_seconds: int | float


class ErrorSignals(TypedDict):
    """Error metric calculation result"""

    error_rate_percent: float
    errors_per_second: float


class SaturationData(TypedDict):
    """Raw saturation metric data"""

    usage_percentages: NumericList


class SaturationSignals(TypedDict):
    """Saturation metric calculation result"""

    average_usage_percent: float
    max_usage_percent: float
    p99_usage_percent: float


# ===== Anomaly Detection Related Data Structures =====
class AnomalyResult(TypedDict):
    """Anomaly detection result"""

    is_anomaly: bool
    anomaly_type: str  # AnomalyType.value
    severity: str  # AnomalySeverity.value
    confidence: float  # 0.0 - 1.0
    current_value: float
    description: str
    threshold_info: dict[str, Any] | None
    detection_method: str  # DetectionMethod.value


class HistoricalData(TypedDict):
    """Historical data"""

    values: NumericList
    timestamps: list[int] | None  # Unix timestamps


class AllHistoricalData(TypedDict, total=False):
    """Collection of historical data for all metrics"""

    latency: dict[str, HistoricalData]
    traffic: dict[str, HistoricalData]
    error: dict[str, HistoricalData]
    saturation: dict[str, HistoricalData]


class AnomalySummary(TypedDict):
    """Anomaly detection summary"""

    total_anomalies: int
    critical_anomalies: int
    high_anomalies: int
    medium_anomalies: int
    low_anomalies: int
