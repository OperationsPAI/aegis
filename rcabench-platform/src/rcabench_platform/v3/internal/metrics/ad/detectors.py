from typing import Any, Protocol, runtime_checkable

import numpy as np
import scipy.stats as stats

from .configs import (
    EnhancedLatencyConfig,
    StatisticalConfig,
    SuccessRateConfig,
    ThresholdConfig,
    TrendConfig,
)
from .types import (
    AnomalyResult,
    AnomalySeverity,
    AnomalyType,
    DetectionMethod,
    HistoricalData,
)


@runtime_checkable
class AnomalyDetector(Protocol):
    """Anomaly detector protocol"""

    def detect(self, current_value: float, historical_data: HistoricalData, **kwargs: Any) -> AnomalyResult:
        """Detect anomaly"""
        ...


class ThresholdDetector:
    """
    Threshold detector

    Performs anomaly detection based on preset upper and lower threshold values
    """

    def detect(
        self,
        current_value: float,
        historical_data: HistoricalData,
        config: ThresholdConfig,
    ) -> AnomalyResult:
        result: AnomalyResult = {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": "No threshold anomaly detected",
            "threshold_info": None,
            "detection_method": DetectionMethod.THRESHOLD.value,
        }

        if not config.enabled:
            return result

        # Check high threshold
        if config.high_threshold is not None and current_value > config.high_threshold:
            severity_ratio = current_value / config.high_threshold
            result.update(
                {
                    "is_anomaly": True,
                    "anomaly_type": AnomalyType.THRESHOLD_HIGH.value,
                    "confidence": min(severity_ratio, config.severity_critical_ratio) / config.severity_critical_ratio,
                    "description": f"Value {current_value:.2f} exceeds high threshold {config.high_threshold:.2f}",
                    "threshold_info": {
                        "type": "high",
                        "threshold": config.high_threshold,
                        "ratio": severity_ratio,
                    },
                }
            )

            # Determine severity
            if severity_ratio >= config.severity_critical_ratio:
                result["severity"] = AnomalySeverity.CRITICAL.value
            elif severity_ratio >= config.severity_high_ratio:
                result["severity"] = AnomalySeverity.HIGH.value
            elif severity_ratio >= config.severity_medium_ratio:
                result["severity"] = AnomalySeverity.MEDIUM.value

        # Check low threshold
        elif config.low_threshold is not None and current_value < config.low_threshold:
            if config.low_threshold > 0:
                severity_ratio = config.low_threshold / max(current_value, 0.001)
            else:
                severity_ratio = config.severity_high_ratio

            result.update(
                {
                    "is_anomaly": True,
                    "anomaly_type": AnomalyType.THRESHOLD_LOW.value,
                    "confidence": min(severity_ratio, config.severity_critical_ratio) / config.severity_critical_ratio,
                    "description": f"Value {current_value:.2f} below low threshold {config.low_threshold:.2f}",
                    "threshold_info": {
                        "type": "low",
                        "threshold": config.low_threshold,
                        "ratio": severity_ratio,
                    },
                }
            )

            # Determine severity
            if severity_ratio >= config.severity_critical_ratio:
                result["severity"] = AnomalySeverity.CRITICAL.value
            elif severity_ratio >= config.severity_high_ratio:
                result["severity"] = AnomalySeverity.HIGH.value
            elif severity_ratio >= config.severity_medium_ratio:
                result["severity"] = AnomalySeverity.MEDIUM.value

        return result


class StatisticalDetector:
    """
    Statistical detector

    Statistical anomaly detection based on Z-score and IQR
    """

    def detect(
        self,
        current_value: float,
        historical_data: HistoricalData,
        config: StatisticalConfig,
    ) -> AnomalyResult:
        result: AnomalyResult = {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": "No statistical anomaly detected",
            "threshold_info": None,
            "detection_method": DetectionMethod.STATISTICAL.value,
        }

        if not config.enabled or len(historical_data["values"]) < 5:
            return result

        values = np.array(historical_data["values"])

        # Z-Score detection
        mean_val = np.mean(values)
        std_val = np.std(values)

        if std_val > 0:
            z_score = abs(current_value - mean_val) / std_val

            if z_score > config.z_score_threshold:
                result.update(
                    {
                        "is_anomaly": True,
                        "anomaly_type": AnomalyType.STATISTICAL_OUTLIER.value,
                        "confidence": min(
                            float(z_score) / config.z_score_threshold,
                            config.severity_zscore_critical,
                        )
                        / config.severity_zscore_critical,
                        "description": f"Statistical outlier detected (Z-score: {z_score:.2f})",
                        "threshold_info": {
                            "z_score": float(z_score),
                            "mean": float(mean_val),
                            "std": float(std_val),
                        },
                    }
                )

                # Determine severity by Z-score
                if z_score >= config.severity_zscore_critical:
                    result["severity"] = AnomalySeverity.CRITICAL.value
                elif z_score >= config.severity_zscore_high:
                    result["severity"] = AnomalySeverity.HIGH.value
                elif z_score >= config.severity_zscore_medium:
                    result["severity"] = AnomalySeverity.MEDIUM.value

                return result

        # IQR detection (as an alternative method)
        q1, q3 = np.percentile(values, [25, 75])
        iqr = q3 - q1

        if iqr > 0:
            lower_bound = q1 - config.iqr_multiplier * iqr
            upper_bound = q3 + config.iqr_multiplier * iqr

            if current_value < lower_bound or current_value > upper_bound:
                distance = max(lower_bound - current_value, current_value - upper_bound, 0)
                confidence = min(float(distance) / (float(iqr) * config.iqr_multiplier), 1.0)

                result.update(
                    {
                        "is_anomaly": True,
                        "anomaly_type": AnomalyType.STATISTICAL_OUTLIER.value,
                        "confidence": confidence,
                        "description": f"IQR outlier detected (bounds: {lower_bound:.2f} - {upper_bound:.2f})",
                        "threshold_info": {
                            "iqr_lower": float(lower_bound),
                            "iqr_upper": float(upper_bound),
                            "iqr": float(iqr),
                        },
                    }
                )

                # Determine severity
                if confidence >= config.severity_confidence_high:
                    result["severity"] = AnomalySeverity.HIGH.value
                elif confidence >= config.severity_confidence_medium:
                    result["severity"] = AnomalySeverity.MEDIUM.value

        return result


class TrendDetector:
    """
    Trend detector

    Detects spike and drop anomaly patterns
    """

    def detect(self, current_value: float, historical_data: HistoricalData, config: TrendConfig) -> AnomalyResult:
        result: AnomalyResult = {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": "No trend anomaly detected",
            "threshold_info": None,
            "detection_method": DetectionMethod.TREND.value,
        }

        if not config.enabled or len(historical_data["values"]) < config.window_size:
            return result

        values = historical_data["values"]
        recent_values = values[-config.window_size :]

        recent_mean = np.mean(recent_values)
        recent_std = np.std(recent_values)

        if recent_std > 0:
            # Detect spike
            spike_threshold = recent_mean + config.spike_threshold_multiplier * recent_std
            if current_value > spike_threshold:
                spike_ratio = (current_value - recent_mean) / recent_std
                result.update(
                    {
                        "is_anomaly": True,
                        "anomaly_type": AnomalyType.TREND_SPIKE.value,
                        "confidence": min(float(spike_ratio) / config.spike_threshold_multiplier, 1.0),
                        "description": f"Spike detected: {current_value:.2f} vs recent mean {recent_mean:.2f}",
                        "threshold_info": {
                            "spike_threshold": float(spike_threshold),
                            "recent_mean": float(recent_mean),
                            "recent_std": float(recent_std),
                        },
                    }
                )

                if spike_ratio >= config.severity_spike_critical:
                    result["severity"] = AnomalySeverity.CRITICAL.value
                elif spike_ratio >= config.severity_spike_high:
                    result["severity"] = AnomalySeverity.HIGH.value
                elif spike_ratio >= config.severity_spike_medium:
                    result["severity"] = AnomalySeverity.MEDIUM.value

                return result

            # Detect drop
            drop_threshold = recent_mean - config.spike_threshold_multiplier * recent_std
            if current_value < drop_threshold:
                drop_ratio = (recent_mean - current_value) / recent_std
                result.update(
                    {
                        "is_anomaly": True,
                        "anomaly_type": AnomalyType.TREND_DROP.value,
                        "confidence": min(float(drop_ratio) / config.spike_threshold_multiplier, 1.0),
                        "description": f"Drop detected: {current_value:.2f} vs recent mean {recent_mean:.2f}",
                        "threshold_info": {
                            "drop_threshold": float(drop_threshold),
                            "recent_mean": float(recent_mean),
                            "recent_std": float(recent_std),
                        },
                    }
                )

                if drop_ratio >= config.severity_spike_critical:
                    result["severity"] = AnomalySeverity.CRITICAL.value
                elif drop_ratio >= config.severity_spike_high:
                    result["severity"] = AnomalySeverity.HIGH.value
                elif drop_ratio >= config.severity_spike_medium:
                    result["severity"] = AnomalySeverity.MEDIUM.value

        return result


class EnhancedLatencyDetector:
    """
    Enhanced latency detector

    Advanced detection methods specifically for latency metrics, including percentile-specific logic
    """

    def detect(
        self,
        current_value: float,
        historical_data: HistoricalData,
        config: EnhancedLatencyConfig,
    ) -> AnomalyResult:
        result: AnomalyResult = {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": "No enhanced latency anomaly detected",
            "threshold_info": None,
            "detection_method": DetectionMethod.ENHANCED_LATENCY.value,
        }

        if not config.enabled:
            return result

        normal_data = historical_data["values"]

        # Need at least a minimum number of data points for reliable analysis
        if len(normal_data) < 5:
            result["description"] = "Insufficient historical data for enhanced latency analysis"
            return result

        normal_array = np.array(normal_data)
        normal_mean = float(np.mean(normal_array))

        # ===== Hard filtering rules =====
        if config.enable_baseline_filtering:
            # Rule 2: If normal data average > 1s, directly consider as normal
            if normal_mean > config.baseline_avg_threshold:
                result["description"] = f"Baseline avg too high: {normal_mean:.3f}s > {config.baseline_avg_threshold}s"
                return result

            # Rule 3: If normal data p99 > 5s, consider normal period unstable
            normal_p99 = float(np.percentile(normal_array, 99))
            if normal_p99 > config.baseline_p99_threshold:
                result["description"] = f"Baseline p99 too high: {normal_p99:.3f}s > {config.baseline_p99_threshold}s"
                return result

        # Only consider performance degradation (higher latency) as anomaly
        if current_value <= normal_mean:
            return result

        # Rule 1: Hard timeout detection
        if current_value > config.hard_timeout_threshold:
            result.update(
                {
                    "is_anomaly": True,
                    "anomaly_type": AnomalyType.THRESHOLD_HIGH.value,
                    "severity": AnomalySeverity.CRITICAL.value,
                    "confidence": 1.0,
                    "description": f"Hard timeout violated: {current_value:.2f}s > {config.hard_timeout_threshold}s",
                    "threshold_info": {
                        "rule": "hardcoded_long_duration",
                        "threshold": config.hard_timeout_threshold,
                        "rule_based_anomaly": True,
                    },
                }
            )
            return result

        return self._detect_with_adaptive_thresholds(current_value, normal_array, normal_mean, config)

    def _detect_with_adaptive_thresholds(
        self,
        current_value: float,
        normal_array,
        normal_mean: float,
        config: EnhancedLatencyConfig,
    ) -> AnomalyResult:
        result: AnomalyResult = {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": current_value,
            "description": "No adaptive latency anomaly detected",
            "threshold_info": None,
            "detection_method": DetectionMethod.ENHANCED_LATENCY.value,
        }

        # ===== Hard filtering rules =====
        if config.enable_baseline_filtering:
            # Get corresponding baseline threshold based on percentile type
            baseline_threshold = config.percentile_baseline_thresholds.get(
                config.percentile_type, config.baseline_avg_threshold
            )
            if normal_mean > baseline_threshold:
                result["description"] = (
                    f"Baseline {config.percentile_type} too high: {normal_mean:.3f}s > {baseline_threshold}s"
                )
                return result

            # Rule 3: If normal data p99 > 5s, consider normal period unstable (only for non-p99 percentiles)
            if config.percentile_type != "p99":
                normal_p99 = float(np.percentile(normal_array, 99))
                if normal_p99 > config.baseline_p99_threshold:
                    result["description"] = (
                        f"Baseline p99 too high: {normal_p99:.3f}s > {config.baseline_p99_threshold}s"
                    )
                    return result

        if normal_mean < config.small_baseline_threshold:
            # Get corresponding absolute threshold based on percentile type
            absolute_threshold = config.percentile_absolute_thresholds.get(
                config.percentile_type, config.absolute_anomaly_threshold
            )
            is_anomaly = current_value > absolute_threshold
            detection_method = f"adaptive_absolute_{config.percentile_type}"
            threshold_info = {
                "method": "absolute",
                "threshold": absolute_threshold,
                "baseline_mean": normal_mean,
                "percentile_type": config.percentile_type,
            }
        else:
            base_multiplier = config.base_multiplier_thresholds.get(config.percentile_type, 3.0)
            min_multiplier = config.percentile_min_multipliers.get(config.percentile_type, 1.5)

            if normal_mean >= 2.0:
                dynamic_multiplier = min_multiplier
            else:
                ratio = (normal_mean - config.small_baseline_threshold) / (2.0 - config.small_baseline_threshold)
                dynamic_multiplier = base_multiplier - ratio * (base_multiplier - min_multiplier)

            threshold = normal_mean * dynamic_multiplier
            is_anomaly = current_value > threshold
            detection_method = f"adaptive_multiplier_{config.percentile_type}"
            threshold_info = {
                "method": "multiplier",
                "multiplier": dynamic_multiplier,
                "base_multiplier": base_multiplier,
                "min_multiplier": min_multiplier,
                "threshold": threshold,
                "baseline_mean": normal_mean,
                "percentile_type": config.percentile_type,
            }

        if is_anomaly:
            result.update(
                {
                    "is_anomaly": True,
                    "anomaly_type": AnomalyType.THRESHOLD_HIGH.value,
                    "description": f"({config.percentile_type}): {current_value:.3f}s using {detection_method}",
                    "threshold_info": threshold_info,
                }
            )

        return result


class SuccessRateDetector:
    """
    Success rate detector

    Detects success rate drops based on statistical significance tests
    """

    def detect(
        self,
        current_value: float,
        historical_data: HistoricalData,
        config: SuccessRateConfig,
        normal_rate: float,
        abnormal_rate: float,
        normal_count: int,
        abnormal_count: int,
    ) -> AnomalyResult:
        result: AnomalyResult = {
            "is_anomaly": False,
            "anomaly_type": AnomalyType.NONE.value,
            "severity": AnomalySeverity.LOW.value,
            "confidence": 0.0,
            "current_value": abnormal_rate,
            "description": "No success rate anomaly detected",
            "threshold_info": None,
            "detection_method": DetectionMethod.SUCCESS_RATE.value,
        }

        if not config.enabled or normal_count < config.min_normal_count or abnormal_count < config.min_abnormal_count:
            result["description"] = "Insufficient data for success rate analysis"
            return result

        # Calculate pooled ratio and standard error
        pooled_p = (normal_rate * normal_count + abnormal_rate * abnormal_count) / (normal_count + abnormal_count)
        se = np.sqrt(pooled_p * (1 - pooled_p) * (1 / normal_count + 1 / abnormal_count))

        if se <= 0:
            return result

        # Z test
        z_stat = abs(abnormal_rate - normal_rate) / se
        p_value_array = 2 * (1 - stats.norm.cdf(abs(z_stat)))
        p_value = float(p_value_array) if hasattr(p_value_array, "item") else float(p_value_array)

        rate_drop = normal_rate - abnormal_rate

        # Multi-standard anomaly detection
        is_significant = (
            rate_drop > config.min_rate_drop
            and p_value < config.significance_threshold
            and rate_drop > config.min_relative_drop * normal_rate
        )

        if is_significant:
            confidence = min(1.0, (1.0 - p_value) * 2.0)

            result.update(
                {
                    "is_anomaly": True,
                    "anomaly_type": AnomalyType.SUCCESS_RATE_DROP.value,
                    "confidence": confidence,
                    "description": f"Success rate drop detected: {rate_drop:.3f} (p={p_value:.3f})",
                    "threshold_info": {
                        "p_value": p_value,
                        "z_statistic": float(z_stat),
                        "rate_drop": rate_drop,
                        "normal_rate": normal_rate,
                        "abnormal_rate": abnormal_rate,
                        "normal_count": normal_count,
                        "abnormal_count": abnormal_count,
                    },
                }
            )

            # Determine severity by drop magnitude
            if rate_drop > 0.2:  # 20%+ drop
                result["severity"] = AnomalySeverity.CRITICAL.value
            elif rate_drop > 0.1:  # 10%+ drop
                result["severity"] = AnomalySeverity.HIGH.value
            elif rate_drop > 0.05:  # 5%+ drop
                result["severity"] = AnomalySeverity.MEDIUM.value

        return result
