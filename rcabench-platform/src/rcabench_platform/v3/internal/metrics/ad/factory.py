"""
Detector factory and unified detection interface
Provides detector instance retrieval and unified anomaly detection entry point
"""

from typing import Any

from .configs import MetricDetectionConfig
from .detectors import (
    EnhancedLatencyDetector,
    StatisticalDetector,
    SuccessRateDetector,
    ThresholdDetector,
    TrendDetector,
)
from .types import (
    AnomalyResult,
    AnomalySeverity,
    AnomalyType,
    DetectionMethod,
    HistoricalData,
)


# ===== Detector Factory =====
class DetectorFactory:
    """
    Detector factory

    Responsible for creating and managing instances of various anomaly detectors
    """

    _detectors = {
        DetectionMethod.THRESHOLD: ThresholdDetector(),
        DetectionMethod.STATISTICAL: StatisticalDetector(),
        DetectionMethod.TREND: TrendDetector(),
        DetectionMethod.ENHANCED_LATENCY: EnhancedLatencyDetector(),
        DetectionMethod.SUCCESS_RATE: SuccessRateDetector(),
    }

    @classmethod
    def get_detector(cls, method: DetectionMethod) -> Any:
        """
        Get detector instance

        Args:
            method: Detection method enum

        Returns:
            Corresponding detector instance, or None if not found
        """
        return cls._detectors.get(method)

    @classmethod
    def get_available_methods(cls) -> list[DetectionMethod]:
        """Get all available detection methods"""
        return list(cls._detectors.keys())

    @classmethod
    def register_detector(cls, method: DetectionMethod, detector: Any) -> None:
        """
        Register a new detector

        Args:
            method: Detection method enum
            detector: Detector instance
        """
        cls._detectors[method] = detector


# ===== Unified Detection Interface =====
def detect_anomalies(
    current_value: float,
    historical_data: HistoricalData,
    config: MetricDetectionConfig,
    **kwargs: Any,
) -> list[AnomalyResult]:
    """
    Unified anomaly detection entry point

    Calls the corresponding detection methods according to the configuration,
    supports parallel detection with multiple methods

    Args:
        current_value: Current value
        historical_data: Historical data
        config: Metric detection configuration
        **kwargs: Additional parameters (e.g., parameters required for success rate detection)

    Returns:
        List of anomaly detection results
    """
    results: list[AnomalyResult] = []

    for method, method_config in config.enabled_methods.items():
        if not method_config.enabled:
            continue

        detector = DetectorFactory.get_detector(method)
        if detector is None:
            continue

        try:
            if method == DetectionMethod.SUCCESS_RATE:
                # Success rate detection requires additional parameters
                required_keys = [
                    "normal_rate",
                    "abnormal_rate",
                    "normal_count",
                    "abnormal_count",
                ]
                if all(key in kwargs for key in required_keys):
                    result = detector.detect(
                        current_value,
                        historical_data,
                        method_config,
                        kwargs["normal_rate"],
                        kwargs["abnormal_rate"],
                        kwargs["normal_count"],
                        kwargs["abnormal_count"],
                    )
                else:
                    # Insufficient parameters, skip this detection method
                    continue
            else:
                result = detector.detect(current_value, historical_data, method_config)

            if result["is_anomaly"]:
                results.append(result)

        except Exception as e:
            # Log error but do not interrupt other detection methods
            print(f"Error in {method.value} detection: {e}")
            continue

    # If no anomaly is detected, return a "no anomaly" result
    if not results:
        results.append(
            {
                "is_anomaly": False,
                "anomaly_type": AnomalyType.NONE.value,
                "severity": AnomalySeverity.LOW.value,
                "confidence": 0.0,
                "current_value": current_value,
                "description": "No anomaly detected",
                "threshold_info": None,
                "detection_method": "none",
            }
        )

    return results


def detect_single_method(
    current_value: float,
    historical_data: HistoricalData,
    method: DetectionMethod,
    method_config: Any,
    **kwargs: Any,
) -> AnomalyResult:
    """
    Single-method anomaly detection

    Args:
        current_value: Current value
        historical_data: Historical data
        method: Detection method
        method_config: Method configuration
        **kwargs: Additional parameters

    Returns:
        Anomaly detection result
    """
    detector = DetectorFactory.get_detector(method)
    if detector is None:
        return {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": f"Detector for method {method.value} not found",
            "threshold_info": None,
            "detection_method": method.value,
        }

    try:
        if method == DetectionMethod.SUCCESS_RATE:
            # Success rate detection requires additional parameters
            required_keys = [
                "normal_rate",
                "abnormal_rate",
                "normal_count",
                "abnormal_count",
            ]
            if all(key in kwargs for key in required_keys):
                return detector.detect(
                    current_value,
                    historical_data,
                    method_config,
                    kwargs["normal_rate"],
                    kwargs["abnormal_rate"],
                    kwargs["normal_count"],
                    kwargs["abnormal_count"],
                )
            else:
                return {
                    "is_anomaly": False,
                    "anomaly_type": AnomalyType.NONE.value,
                    "severity": AnomalySeverity.LOW.value,
                    "confidence": 0.0,
                    "current_value": current_value,
                    "description": "Missing required parameters for success rate detection",
                    "threshold_info": None,
                    "detection_method": method.value,
                }
        else:
            return detector.detect(current_value, historical_data, method_config)

    except Exception as e:
        return {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": f"Error in {method.value} detection: {str(e)}",
            "threshold_info": None,
            "detection_method": method.value,
        }


def summarize_anomalies(results: list[AnomalyResult]) -> dict[str, Any]:
    """
    Anomaly detection result summary

    Args:
        results: List of anomaly detection results

    Returns:
        Summary information dictionary
    """
    anomaly_results = [r for r in results if r["is_anomaly"]]

    severity_counts = {"critical": 0, "high": 0, "medium": 0, "low": 0}

    methods_detected = []
    anomaly_types = []
    max_confidence = 0.0

    for result in anomaly_results:
        severity = result["severity"]
        if severity in severity_counts:
            severity_counts[severity] += 1

        methods_detected.append(result["detection_method"])
        anomaly_types.append(result["anomaly_type"])
        max_confidence = max(max_confidence, result["confidence"])

    return {
        "total_anomalies": len(anomaly_results),
        "severity_counts": severity_counts,
        "methods_detected": list(set(methods_detected)),
        "anomaly_types": list(set(anomaly_types)),
        "max_confidence": max_confidence,
        "has_critical": severity_counts["critical"] > 0,
        "has_high": severity_counts["high"] > 0,
        "is_anomalous": len(anomaly_results) > 0,
    }
