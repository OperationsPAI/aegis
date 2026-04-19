#!/usr/bin/env -S uv run -s
import functools
import json
import os
from datetime import datetime
from pathlib import Path
from typing import Any, Literal, TypedDict

import numpy as np
import polars as pl
from dotenv import load_dotenv
from rcabench.openapi import (
    DetectorResultItem,
    ExecutionsApi,
    InjectionsApi,
    LabelItem,
    ManageInjectionLabelReq,
    UploadDetectorResultReq,
)
from tqdm import tqdm

from rcabench_platform.v3.cli.main import app
from rcabench_platform.v3.internal.clients.rcabench_ import get_rcabench_client
from rcabench_platform.v3.internal.metrics.ad.configs import (
    EnhancedLatencyConfig,
    SuccessRateConfig,
)
from rcabench_platform.v3.internal.metrics.ad.detectors import (
    EnhancedLatencyDetector,
    SuccessRateDetector,
)
from rcabench_platform.v3.internal.metrics.ad.types import HistoricalData
from rcabench_platform.v3.internal.metrics.metrics_calculator import DatasetMetricsCalculator
from rcabench_platform.v3.sdk.datasets.rcabench import RCABenchAnalyzerLoader, valid
from rcabench_platform.v3.sdk.logging import logger, timeit
from rcabench_platform.v3.sdk.pedestals import Pedestal, get_pedestal
from rcabench_platform.v3.sdk.utils.fmap import fmap_processpool

load_dotenv(Path.cwd() / ".env")


class AnomalyScoreResult(TypedDict):
    is_anomaly: bool
    total_score: float
    change_rate: float
    abnormal_value: float
    absolute_change: float
    description: str
    severity: str
    detection_method: str
    threshold_info: dict[str, Any] | None
    rule_anomaly: bool


class SuccessRateResult(TypedDict):
    is_significant: bool
    p_value: float
    z_statistic: float
    change_rate: float
    rate_drop: float
    confidence: float
    description: str
    severity: str


class ConclusionRowResult(TypedDict):
    # Latency results
    latency_is_anomaly: bool
    latency_total_score: float
    latency_change_rate: float
    latency_abnormal_value: float
    latency_absolute_change: float
    latency_description: str
    latency_severity: str
    latency_detection_method: str
    latency_threshold_info: dict[str, Any] | None

    # Success rate results
    success_rate_is_significant: bool
    success_rate_p_value: float
    success_rate_z_statistic: float
    success_rate_change_rate: float
    success_rate_rate_drop: float
    success_rate_confidence: float
    success_rate_description: str


class ConclusionRow(TypedDict):
    SpanName: str
    Issues: str
    AbnormalAvgDuration: float
    NormalAvgDuration: float
    AbnormalSuccRate: float
    NormalSuccRate: float
    AbnormalP90: float
    NormalP90: float
    AbnormalP95: float
    NormalP95: float
    AbnormalP99: float
    NormalP99: float


class AnalysisMetrics:
    def __init__(self):
        self.processed_endpoints = 0
        self.skipped_endpoints = 0
        self.anomaly_count = 0
        self.absolute_anomaly = False
        self.issue_categories = {
            "latency_only": 0,
            "success_rate_only": 0,
            "both_latency_and_success_rate": 0,
            "no_issues": 0,
        }

    def increment_processed(self) -> None:
        self.processed_endpoints += 1

    def increment_skipped(self) -> None:
        self.skipped_endpoints += 1

    def increment_anomaly(self) -> None:
        self.anomaly_count += 1

    def set_absolute_anomaly(self) -> None:
        self.absolute_anomaly = True

    def categorize_issue(self, has_latency: bool, has_success_rate: bool) -> None:
        if has_latency and has_success_rate:
            self.issue_categories["both_latency_and_success_rate"] += 1
        elif has_latency:
            self.issue_categories["latency_only"] += 1
        elif has_success_rate:
            self.issue_categories["success_rate_only"] += 1
        else:
            self.issue_categories["no_issues"] += 1

    def is_latency_only_dataset(self) -> bool:
        return (
            self.issue_categories["latency_only"] > 0
            and self.issue_categories["success_rate_only"] == 0
            and self.issue_categories["both_latency_and_success_rate"] == 0
        )


class AnalysisState(TypedDict):
    conclusion_data: list[ConclusionRow]
    metrics: AnalysisMetrics


class AnalysisResult(TypedDict):
    datapack_name: str
    is_latency_only: bool
    total_endpoints: int
    anomaly_count: int
    issue_categories: dict[str, int]
    absolute_anomaly: bool
    datapack_metrics: dict


def calculate_anomaly_score(
    tp: Literal["avg", "p90", "p95", "p99"],
    normal_data: list[float],
    abnormal_value: float,
) -> AnomalyScoreResult:
    detector = EnhancedLatencyDetector()
    config = EnhancedLatencyConfig(
        percentile_type=tp,
    )

    historical_data: HistoricalData = {"values": normal_data, "timestamps": None}

    result = detector.detect(abnormal_value, historical_data, config)

    normal_mean = np.mean(normal_data) if normal_data else 0.0
    change_rate = (abnormal_value - normal_mean) / normal_mean if normal_mean > 0 else 0.0
    absolute_change = abnormal_value - normal_mean

    # Check if it's a rule-based anomaly (hard timeout or adaptive rules)
    rule_anomaly = False
    if result["threshold_info"] and result["threshold_info"].get("rule_based_anomaly"):
        rule_anomaly = True
    elif abnormal_value > config.hard_timeout_threshold:
        rule_anomaly = True

    return_dict: AnomalyScoreResult = {
        "is_anomaly": result["is_anomaly"],
        "total_score": float(result["confidence"]),
        "change_rate": float(change_rate),
        "abnormal_value": float(abnormal_value),
        "absolute_change": float(absolute_change),
        "description": result["description"],
        "severity": result["severity"],
        "detection_method": result["detection_method"],
        "threshold_info": result["threshold_info"],
        "rule_anomaly": rule_anomaly,
    }

    return return_dict


def is_success_rate_significant(
    normal_succ_rate: float,
    abnormal_succ_rate: float,
    normal_total: int,
    abnormal_total: int,
) -> SuccessRateResult:
    detector = SuccessRateDetector()
    config = SuccessRateConfig(
        enabled=True,
        min_normal_count=10,
        min_abnormal_count=5,
        min_rate_drop=0.03,
        significance_threshold=0.05,
        min_relative_drop=0.1,
    )

    historical_data: HistoricalData = {"values": [], "timestamps": None}

    result = detector.detect(
        current_value=abnormal_succ_rate,
        historical_data=historical_data,
        config=config,
        normal_rate=normal_succ_rate,
        abnormal_rate=abnormal_succ_rate,
        normal_count=normal_total,
        abnormal_count=abnormal_total,
    )

    rate_drop = normal_succ_rate - abnormal_succ_rate
    change_rate = rate_drop / normal_succ_rate if normal_succ_rate > 0 else 0.0

    p_value = 1.0
    z_statistic = 0.0
    if result["threshold_info"]:
        p_value = result["threshold_info"].get("p_value", 1.0)
        z_statistic = result["threshold_info"].get("z_statistic", 0.0)

    return_result: SuccessRateResult = {
        "is_significant": result["is_anomaly"],
        "p_value": float(p_value),
        "z_statistic": float(z_statistic),
        "change_rate": float(change_rate),
        "rate_drop": float(rate_drop),
        "confidence": float(result["confidence"]),
        "description": result["description"],
        "severity": result["severity"],
    }
    return return_result


def preprocess_trace(file: Path, pedestal: Pedestal) -> dict[str, Any]:
    """Preprocess trace data from a Parquet file and extract endpoint statistics."""
    if not file.exists():
        raise FileNotFoundError(f"Trace file not found: {file}")

    df = pl.scan_parquet(file)

    entry_df = df.filter(
        (pl.col("ServiceName") == "loadgenerator") & (pl.col("ParentSpanId").is_null() | (pl.col("ParentSpanId") == ""))
    )

    entry_count = entry_df.select(pl.len()).collect().item()
    if entry_count == 0:
        logger.error(f"loadgenerator not found in trace data, using {pedestal.entrance_service} as fallback")
        entry_df = df.filter(
            (pl.col("ServiceName") == pedestal.entrance_service)
            & (pl.col("ParentSpanId").is_null() | (pl.col("ParentSpanId") == ""))
        )
        entry_count = entry_df.select(pl.len()).collect().item()

    if entry_count == 0:
        logger.error("No valid entrypoint found in trace data, trying all services")

        # Try to find any service with root spans (no parent span)
        available_services = df.select(pl.col("ServiceName")).unique().collect()["ServiceName"].to_list()
        logger.error(f"Available services in trace data: {available_services}")

        # Try each available service as potential entry point
        for service in available_services:
            if service in ["loadgenerator", pedestal.entrance_service]:
                continue  # Already tried these

            entry_df = df.filter(
                (pl.col("ServiceName") == service) & (pl.col("ParentSpanId").is_null() | (pl.col("ParentSpanId") == ""))
            )
            entry_count = entry_df.select(pl.len()).collect().item()
            if entry_count > 0:
                logger.info(f"Using {service} as entry point with {entry_count} root spans")
                break

        # If still no entry points found, terminate the process
        if entry_count == 0:
            logger.error("No root spans found in any service, terminating analysis")
            return {}

    entry_df_collected = entry_df.with_columns(pl.col("Timestamp").alias(pedestal.name)).sort(pedestal.name).collect()

    entrypoints = set(entry_df_collected["SpanName"].to_list())

    deduped_entrypoints = {}
    for entrypoint in entrypoints:
        path = pedestal.normalize_path(entrypoint)
        deduped_entrypoints[entrypoint] = path

    stat = {}

    span_groups = entry_df_collected.group_by("SpanName")

    for span_name, group_df in span_groups:
        dedupe_name = deduped_entrypoints.get(span_name[0], span_name[0])

        if dedupe_name not in stat:
            stat[dedupe_name] = {
                "timestamp": [],
                "duration": [],
                "status_code": [],
                "response_content_length": [],
                "request_content_length": [],
            }

        timestamps = group_df["Timestamp"].to_list()
        durations = group_df["Duration"].to_list()

        stat[dedupe_name]["timestamp"].extend(timestamps)
        stat[dedupe_name]["duration"].extend(durations)

        for row in group_df.iter_rows(named=True):
            ra = json.loads(row["SpanAttributes"])
            if "http.status_code" in ra:
                stat[dedupe_name]["status_code"].append(ra["http.status_code"])
            elif row["StatusCode"] != "Unset":
                stat[dedupe_name]["status_code"].append(row["StatusCode"])

            if "http.response_content_length" in ra:
                stat[dedupe_name]["response_content_length"].append(ra["http.response_content_length"])
            if "http.request_content_length" in ra:
                stat[dedupe_name]["request_content_length"].append(ra["http.request_content_length"])

    for k, v in stat.items():
        durations = v["duration"]
        if not durations:
            logger.warning(f"No duration data found for endpoint: {k}")
            # Set duration metrics to None when no data is available
            v["avg_duration"] = None
            v["p90_duration"] = None
            v["p95_duration"] = None
            v["p99_duration"] = None
        else:
            durations_array = np.array(durations)
            avg_duration = np.mean(durations_array)
            p90_duration = np.percentile(durations_array, 90)
            p95_duration = np.percentile(durations_array, 95)
            p99_duration = np.percentile(durations_array, 99)

            v["avg_duration"] = avg_duration / 1e9
            v["p90_duration"] = p90_duration / 1e9
            v["p95_duration"] = p95_duration / 1e9
            v["p99_duration"] = p99_duration / 1e9

        status_code = {i: v["status_code"].count(i) for i in set(v["status_code"])}
        request_content_length = {i: v["request_content_length"].count(i) for i in set(v["request_content_length"])}
        response_content_length = {i: v["response_content_length"].count(i) for i in set(v["response_content_length"])}

        # Calculate success rate
        total_requests = sum(status_code.values())
        success_count = status_code.get("200", 0)
        succ_rate = success_count / total_requests if total_requests > 0 else None

        v["status_code"] = status_code
        v["request_content_length"] = request_content_length
        v["response_content_length"] = response_content_length
        v["succ_rate"] = succ_rate

    return stat


def build_conclusion_row(
    k: str, v: dict[str, Any], normal_stat: dict[str, Any], abnormal_tag: dict[str, Any]
) -> ConclusionRow:
    return {
        "SpanName": k,
        "Issues": json.dumps(abnormal_tag),
        "AbnormalAvgDuration": v.get("avg_duration", 0.0),
        "NormalAvgDuration": normal_stat.get(k, {}).get("avg_duration", 0.0),
        "AbnormalSuccRate": v.get("succ_rate", 0.0),
        "NormalSuccRate": normal_stat.get(k, {}).get("succ_rate", 0.0),
        "AbnormalP90": v.get("p90_duration", 0.0),
        "NormalP90": normal_stat.get(k, {}).get("p90_duration", 0.0),
        "AbnormalP95": v.get("p95_duration", 0.0),
        "NormalP95": normal_stat.get(k, {}).get("p95_duration", 0.0),
        "AbnormalP99": v.get("p99_duration", 0.0),
        "NormalP99": normal_stat.get(k, {}).get("p99_duration", 0.0),
    }


def setup_paths_and_validation(in_p: Path | None, ou_p: Path | None) -> tuple[Path, Path]:
    """Setup and validate input/output paths and trace files."""
    if in_p is None:
        input_path_str = os.environ.get("INPUT_PATH", "")
        if not input_path_str:
            raise ValueError(
                "INPUT_PATH environment variable is not set. Please set INPUT_PATH to the datapack directory path."
            )
        in_p = Path(input_path_str)

    if ou_p is None:
        output_path_str = os.environ.get("OUTPUT_PATH", "")
        if not output_path_str:
            raise ValueError(
                "OUTPUT_PATH environment variable is not set. Please set OUTPUT_PATH to the output directory path."
            )
        ou_p = Path(output_path_str)

    input_path = Path(in_p)
    assert input_path.exists(), f"Input path does not exist: {input_path}"

    output_path = Path(ou_p)
    if not os.path.exists(output_path):
        os.makedirs(output_path)
        logger.info(f"Created output directory: {output_path}")

    return input_path, output_path


def get_percentile_config() -> list[tuple[str, str, Any]]:
    """Get the percentile configuration for anomaly detection."""
    return [
        ("avg", "avg_duration", lambda d: [x / 1e9 for x in d]),
        ("p90", "p90_duration", lambda d: sorted([x / 1e9 for x in d])),
        ("p95", "p95_duration", lambda d: sorted([x / 1e9 for x in d])),
        ("p99", "p99_duration", lambda d: sorted([x / 1e9 for x in d])),
    ]


def has_significant_latency_issue(v: dict[str, Any], abnormal_tag: dict[str, Any]) -> bool:
    """Check if there's a significant latency issue (> 10 seconds)."""
    latency_threshold = 10.0  # 10 seconds threshold

    # Check if any abnormal latency values exceed the threshold
    if v.get("avg_duration", 0.0) > latency_threshold:
        return True
    if v.get("p90_duration", 0.0) > latency_threshold:
        return True
    if v.get("p95_duration", 0.0) > latency_threshold:
        return True
    if v.get("p99_duration", 0.0) > latency_threshold:
        return True

    # Also check if hard timeout was triggered (which is already > 10s)
    if "hard_timeout" in abnormal_tag:
        return True

    return False


def handle_new_endpoint(k: str, v: dict[str, Any], state: AnalysisState) -> None:
    """Handle endpoints that don't exist in normal data."""
    logger.warning(f"New endpoint found: {k} - checking against direct thresholds")
    state["metrics"].increment_skipped()

    abnormal_tag = {}

    # Use direct thresholds from EnhancedLatencyConfig to detect anomalies
    # Hard timeout threshold (15.0s)
    hard_timeout_threshold = 15.0

    # Absolute anomaly thresholds for different percentiles
    absolute_thresholds = {
        "avg_duration": 3.0,
        "p90_duration": 7.0,
        "p95_duration": 8.0,
        "p99_duration": 10.0,
    }

    # Check latency thresholds
    for percentile_key, threshold in absolute_thresholds.items():
        if percentile_key in v:
            abnormal_value = v[percentile_key]
            if abnormal_value > threshold:
                abnormal_tag[percentile_key] = {
                    "normal": 0.0,  # No normal data available
                    "abnormal": abnormal_value,
                    "threshold": threshold,
                    "change_rate": float("inf"),  # Cannot calculate change rate without normal data
                    "absolute_change": abnormal_value,
                    "slo_violated": True,
                    "detection_reason": "new_endpoint_threshold_exceeded",
                }

    # Check hard timeout threshold
    avg_duration_val = v.get("avg_duration", 0.0)
    if avg_duration_val > hard_timeout_threshold:
        abnormal_tag["hard_timeout"] = {
            "threshold": hard_timeout_threshold,
            "abnormal": avg_duration_val,
            "slo_violated": True,
            "detection_reason": "hard_timeout_exceeded",
        }

    # Check success rate (assuming < 90% is anomalous for new endpoints)
    success_rate_threshold = 0.9
    total_requests = sum(v.get("status_code", {}).values())
    if total_requests > 0:
        success_count = v.get("status_code", {}).get("200", 0)
        success_rate = success_count / total_requests

        if success_rate < success_rate_threshold:
            abnormal_tag["succ_rate"] = {
                "normal": 1.0,  # Assume normal should be 100%
                "abnormal": success_rate,
                "threshold": success_rate_threshold,
                "rate_drop": 1.0 - success_rate,
                "slo_violated": True,
                "detection_reason": "new_endpoint_low_success_rate",
            }

    # Count anomalies and categorize issues
    if abnormal_tag:
        state["metrics"].increment_anomaly()

        # Categorize issues with 10s latency threshold
        latency_keys = [
            "avg_duration",
            "p90_duration",
            "p95_duration",
            "p99_duration",
            "hard_timeout",
        ]
        has_latency_issue_detected = any(key in abnormal_tag for key in latency_keys)
        has_success_rate_issue = "succ_rate" in abnormal_tag

        # Only count as latency issue if it exceeds 10s threshold
        has_significant_latency = has_latency_issue_detected and has_significant_latency_issue(v, abnormal_tag)

        state["metrics"].categorize_issue(has_significant_latency, has_success_rate_issue)
    else:
        # No issues detected
        state["metrics"].categorize_issue(False, False)

    # Add to conclusion data
    state["conclusion_data"].append(build_conclusion_row(k, v, {}, abnormal_tag))


def detect_latency_anomalies(
    k: str,
    v: dict[str, Any],
    normal_stat: dict[str, Any],
    percentiles: list[tuple[str, str, Any]],
    state: AnalysisState,
) -> dict[str, Any]:
    """Detect latency anomalies for an endpoint."""
    abnormal_tag = {}
    normal_durations = [d / 1e9 for d in normal_stat[k]["duration"]]
    sorted_durations = sorted(normal_durations)

    for idx, (tp, key, norm_fn) in enumerate(percentiles):
        if tp == "avg":
            normal_data = norm_fn(normal_stat[k]["duration"])
            abnormal_value = v.get("avg_duration", 0.0)
        else:
            # p90, p95, p99
            if tp == "p90":
                start, end = int(len(sorted_durations) * 0.85), int(len(sorted_durations) * 0.95)
            elif tp == "p95":
                start, end = int(len(sorted_durations) * 0.90), int(len(sorted_durations) * 0.99)
            else:  # p99
                start, end = int(len(sorted_durations) * 0.95), len(sorted_durations)
            normal_data = sorted_durations[start:end] if start < end else sorted_durations
            abnormal_value = v.get(key, 0.0)

        if normal_data:
            from typing import cast

            result = calculate_anomaly_score(
                cast(Literal["avg", "p90", "p95", "p99"], tp),
                normal_data,
                abnormal_value,
            )
            if result.get("is_anomaly"):
                abnormal_tag[key] = {
                    "normal": normal_stat[k][key],
                    "abnormal": v[key],
                    "anomaly_score": result.get("total_score"),
                    "change_rate": result.get("change_rate"),
                    "absolute_change": result.get("abnormal_value"),
                    "slo_violated": True,
                }
                if result.get("rule_anomaly"):
                    state["metrics"].set_absolute_anomaly()

    return abnormal_tag


def detect_success_rate_anomalies(
    k: str,
    v: dict[str, Any],
    normal_stat: dict[str, Any],
    abnormal_tag: dict[str, Any],
    state: AnalysisState,
) -> dict[str, Any]:
    """Detect success rate anomalies for an endpoint."""
    normal_total = sum(normal_stat[k]["status_code"].values())
    abnormal_total = sum(v["status_code"].values())
    normal_succ_rate = normal_stat[k]["status_code"].get("200", 0) / max(normal_total, 1)
    abnormal_succ_rate = v["status_code"].get("200", 0) / max(abnormal_total, 1)

    success_rate_result = is_success_rate_significant(
        normal_succ_rate, abnormal_succ_rate, normal_total, abnormal_total
    )

    if success_rate_result.get("is_significant"):
        abnormal_tag["succ_rate"] = {
            "normal": normal_succ_rate,
            "abnormal": abnormal_succ_rate,
            "p_value": success_rate_result.get("p_value"),
            "z_statistic": success_rate_result.get("z_statistic"),
            "change_rate": success_rate_result.get("change_rate"),
            "rate_drop": success_rate_result.get("rate_drop"),
            "slo_violated": True,
        }
        logger.debug(
            f"Success rate anomaly detected for {k}: "
            f"drop={success_rate_result.get('rate_drop', 0.0):.3f}, "
            f"p_value={success_rate_result.get('p_value', 0.0):.3f}"
        )
        state["metrics"].set_absolute_anomaly()

    return abnormal_tag


def analyze_single_endpoint(
    k: str,
    v: dict[str, Any],
    normal_stat: dict[str, Any],
    percentiles: list[tuple[str, str, Any]],
    state: AnalysisState,
) -> None:
    """Analyze a single endpoint for anomalies."""
    state["metrics"].increment_processed()

    if k not in normal_stat:
        handle_new_endpoint(k, v, state)
        return

    # Detect latency anomalies
    abnormal_tag = detect_latency_anomalies(k, v, normal_stat, percentiles, state)

    # Detect success rate anomalies
    abnormal_tag = detect_success_rate_anomalies(k, v, normal_stat, abnormal_tag, state)

    # Count anomalies
    if abnormal_tag:
        state["metrics"].increment_anomaly()

    # Categorize issues with 10s latency threshold
    latency_keys = [x[1] for x in percentiles]
    has_latency_issue_detected = any(key in abnormal_tag for key in latency_keys)
    has_success_rate_issue = "succ_rate" in abnormal_tag

    # Only count as latency issue if it exceeds 10s threshold
    has_significant_latency = has_latency_issue_detected and has_significant_latency_issue(v, abnormal_tag)

    state["metrics"].categorize_issue(has_significant_latency, has_success_rate_issue)

    # Add to conclusion data
    state["conclusion_data"].append(build_conclusion_row(k, v, normal_stat, abnormal_tag))


def create_labels(state: AnalysisState) -> list[LabelItem]:
    """Create tags and labels from analysis state."""
    tags = []
    labels = []
    metrics = state["metrics"]

    # Add datapack label
    if metrics.anomaly_count > 0:
        labels.append(LabelItem(key="anomaly_count", value=str(metrics.anomaly_count)))
    if metrics.skipped_endpoints > 0:
        labels.append(LabelItem(key="skipped_endpoints", value=str(metrics.skipped_endpoints)))

    for category, count in metrics.issue_categories.items():
        if count > 0:
            labels.append(LabelItem(key=f"issue_{category}", value=str(count)))

    # Determine anomaly severity based on issue presence
    has_any_issues = (
        metrics.issue_categories["latency_only"] > 0
        or metrics.issue_categories["success_rate_only"] > 0
        or metrics.issue_categories["both_latency_and_success_rate"] > 0
    )

    # Check if any endpoint has latency > 10 seconds for may_anomaly threshold
    has_significant_latency = False
    latency_threshold = 10.0  # 10 seconds threshold

    if has_any_issues:
        for conclusion_row in state["conclusion_data"]:
            # Check various latency metrics against the 10s threshold
            abnormal_avg = conclusion_row.get("AbnormalAvgDuration", 0.0)
            abnormal_p90 = conclusion_row.get("AbnormalP90", 0.0)
            abnormal_p95 = conclusion_row.get("AbnormalP95", 0.0)
            abnormal_p99 = conclusion_row.get("AbnormalP99", 0.0)

            if (
                abnormal_avg > latency_threshold
                or abnormal_p90 > latency_threshold
                or abnormal_p95 > latency_threshold
                or abnormal_p99 > latency_threshold
            ):
                has_significant_latency = True
                break

    if metrics.absolute_anomaly:
        tags.append("absolute_anomaly")
    elif has_any_issues and has_significant_latency:
        tags.append("may_anomaly")
    else:
        tags.append("no_anomaly")

    # Keep specific issue type tags for detailed analysis
    if metrics.issue_categories["latency_only"] > 0:
        tags.append("has_latency_issues")
    elif metrics.issue_categories["success_rate_only"] > 0:
        tags.append("has_success_rate_issues")
    elif metrics.issue_categories["both_latency_and_success_rate"] > 0:
        tags.append("has_mixed_issues")

    tags.append("analysis_completed")

    labels.extend([LabelItem(key="tag", value=tag) for tag in tags])
    return labels


def save_analysis_results(state: AnalysisState, output_path: Path) -> AnalysisState:
    if not state["conclusion_data"]:
        logger.warning("No conclusion data available, skipping file creation")
        return state

    conclusion = pl.DataFrame(state["conclusion_data"])
    conclusion.write_csv(Path(output_path) / "conclusion.csv")
    logger.info(f"Results saved to {Path(output_path) / 'conclusion.csv'}")

    return state


def platform_convert(
    injection_name: str,
    in_p: Path | None = None,
    ou_p: Path | None = None,
    system: str = "ts",
) -> None:
    from rcabench_platform.v3.internal.sources.convert import convert_datapack
    from rcabench_platform.v3.internal.sources.rcabench import RCABenchDatapackLoader

    if in_p is None:
        in_p = Path(os.environ.get("INPUT_PATH", ""))
    if ou_p is None:
        ou_p = Path(os.environ.get("OUTPUT_PATH", ""))

    input_path = in_p
    output_path = ou_p
    assert input_path.exists(), f"Input path does not exist: {input_path}"
    assert output_path.exists(), f"Output path does not exist: {output_path}"

    # Check if conclusion.csv exists before proceeding with conversion
    conclusion_csv = input_path / "conclusion.csv"
    if not conclusion_csv.exists():
        logger.warning("conclusion.csv not found, skipping platform conversion")
        return

    # Assert essential trace files exist and are not empty
    normal_traces = input_path / "normal_traces.parquet"
    abnormal_traces = input_path / "abnormal_traces.parquet"

    assert normal_traces.exists(), f"normal_traces.parquet not found in {input_path}"
    assert abnormal_traces.exists(), f"abnormal_traces.parquet not found in {input_path}"

    # Assert trace files are not empty
    normal_df = pl.scan_parquet(normal_traces)
    normal_count = normal_df.select(pl.len()).collect().item()
    assert normal_count > 0, f"normal_traces.parquet is empty in {input_path}"

    abnormal_df = pl.scan_parquet(abnormal_traces)
    abnormal_count = abnormal_df.select(pl.len()).collect().item()
    assert abnormal_count > 0, f"abnormal_traces.parquet is empty in {input_path}"

    logger.info(f"Trace files validated: normal={normal_count} records, abnormal={abnormal_count} records")

    converted_input_path = output_path / "converted"

    convert_datapack(
        loader=RCABenchDatapackLoader(input_path, datapack=injection_name, system=system),
        dst_folder=converted_input_path,
        skip_finished=False,
    )
    logger.info(f"Successfully converted datapack for {injection_name}")


@app.command()
@timeit()
def run(
    in_p: Path | None = None,
    ou_p: Path | None = None,
    system: str = "ts",
    convert: bool = False,
    online: bool = False,
) -> AnalysisResult | None:
    start_time = datetime.now()
    input_path, output_path = setup_paths_and_validation(in_p, ou_p)
    _, is_valid = valid(input_path)
    if not is_valid:
        logger.error(
            f"Input path validation failed: {input_path}. "
            f"Please check if all required files exist and are valid. "
            f"Run with DEBUG=true for detailed validation logs."
        )
        raise ValueError("Input path validation failed.")

    datapack_name = input_path.name
    normal_trace = input_path / "normal_traces.parquet"
    abnormal_trace = input_path / "abnormal_traces.parquet"

    pedestal = get_pedestal(system)

    normal_stat = preprocess_trace(normal_trace, pedestal)
    abnormal_stat = preprocess_trace(abnormal_trace, pedestal)

    # Check if we have valid data for analysis
    if not normal_stat or not abnormal_stat:
        logger.error("No endpoints found in normal or abnormal trace data, terminating analysis.")
        raise ValueError("No endpoints found in normal or abnormal trace data.")

    # Initialize analysis state and configuration
    state = AnalysisState(
        conclusion_data=[],
        metrics=AnalysisMetrics(),
    )
    percentiles = get_percentile_config()

    for k, v in abnormal_stat.items():
        analyze_single_endpoint(k, v, normal_stat, percentiles, state)

    if not state["conclusion_data"]:
        logger.warning("No anomalies detected, skipping file creation")
        return None

    save_analysis_results(state, output_path)  # legacy, @Lincyaw delete it in the future

    datapack_name = input_path.name

    if convert:
        platform_convert(datapack_name, in_p, ou_p, system)

    # Return analysis metadata
    result: AnalysisResult = {
        "datapack_name": datapack_name,
        "is_latency_only": state["metrics"].is_latency_only_dataset(),
        "total_endpoints": state["metrics"].processed_endpoints,
        "anomaly_count": state["metrics"].anomaly_count,
        "issue_categories": state["metrics"].issue_categories,
        "absolute_anomaly": state["metrics"].absolute_anomaly,
        "datapack_metrics": {},
    }

    if online:
        datapack_id_str = os.environ.get("DATAPACK_ID")
        assert datapack_id_str is not None, "DATAPACK_ID is not set"
        datapack_id = int(datapack_id_str)
        assert datapack_id > 0, "DATAPACK_ID must be positive"
        logger.debug(f"Datapack ID: {datapack_id}")

        execution_id_str = os.environ.get("EXECUTION_ID")
        assert execution_id_str is not None, "EXECUTION_ID is not set"
        execution_id = int(execution_id_str)
        assert execution_id > 0, "EXECUTION_ID must be positive"
        logger.debug(f"Execution ID: {execution_id}")

        client = get_rcabench_client(base_url=os.environ.get("RCABENCH_BASE_URL"))

        # Create tags and labels from analysis state
        labels = create_labels(state)
        logger.info(f"Generated labels: {[f'{label.key}={label.value}' for label in labels]}")

        if "no_anomaly" not in labels:
            duration = datetime.now() - start_time
            executions_api = ExecutionsApi(client)

            resp = executions_api.upload_detection_results(
                execution_id=execution_id,
                request=UploadDetectorResultReq(
                    duration=duration.total_seconds(),
                    results=[
                        DetectorResultItem(
                            issues=i["Issues"],
                            span_name=i["SpanName"],
                            abnormal_avg_duration=i["AbnormalAvgDuration"],
                            abnormal_p90=i["AbnormalP90"],
                            abnormal_p95=i["AbnormalP95"],
                            abnormal_p99=i["AbnormalP99"],
                            abnormal_succ_rate=i["AbnormalSuccRate"],
                            normal_avg_duration=i["NormalAvgDuration"],
                            normal_p90=i["NormalP90"],
                            normal_p95=i["NormalP95"],
                            normal_p99=i["NormalP99"],
                            normal_succ_rate=i["NormalSuccRate"],
                        )
                        for i in state["conclusion_data"]
                    ],
                ),
            )

            if resp.code is not None and 200 <= resp.code < 300:
                logger.info("Submit detector result successfully")

        try:
            injections_api = InjectionsApi(client)
            resp = injections_api.manage_injection_labels(
                id=datapack_id, manage=ManageInjectionLabelReq(add_labels=labels)
            )
            if resp.code is not None and 200 <= resp.code < 300:
                logger.info(f"Updated injection labels: {resp.code} - {resp.message}")
        except Exception as e:
            logger.error(f"Failed to update injection labels: {e}")

        calculator = DatasetMetricsCalculator(RCABenchAnalyzerLoader(datapack_name, input_path))
        res = calculator.calculate_and_report(datapack_id, client=client)
        result["datapack_metrics"] = res

    return result


@app.command()
@timeit()
def validate_datapacks(
    mapping_file: Path,
    system: str = "ts",
    force: bool = False,
    delete_invalid: bool = False,
    online: bool = False,
) -> dict[str, Any]:
    dataset_path = Path("data") / "rcabench_dataset"
    assert dataset_path.exists(), f"Dataset path does not exist: {dataset_path}"

    # Get all datapack directories
    datapack_paths = [p for p in dataset_path.iterdir() if p.is_dir() and not p.name.startswith("drain")]
    total_datapacks = len(datapack_paths)

    if total_datapacks == 0:
        logger.warning(f"No datapack directories found in {dataset_path}")
        return {"total": 0, "valid": 0, "invalid": 0, "deleted": 0}

    logger.info(f"Found {total_datapacks} datapacks to validate")

    cpu = os.cpu_count()
    assert cpu is not None, "Cannot determine CPU count"
    parallel = max(1, cpu // 4)

    validation_tasks = [functools.partial(valid, dp, force) for dp in datapack_paths]

    # Run validation in parallel
    validation_results = fmap_processpool(validation_tasks, parallel=parallel, cpu_limit_each=1, ignore_exceptions=True)

    # Process results
    valid_datapacks = []
    invalid_datapacks: list[Path] = []
    manage_req_dict: dict[str, ManageInjectionLabelReq] = {}

    pedestal = get_pedestal(system)

    for datapack_path, is_valid in tqdm(validation_results):
        if any(bl in datapack_path.name for bl in pedestal.black_list):
            is_valid = False

        add_tag = "valid" if is_valid else "invalid"
        remove_tag = "invalid" if is_valid else "valid"

        if is_valid:
            valid_datapacks.append(datapack_path)
        else:
            invalid_datapacks.append(datapack_path)

        if online:
            manage_req_dict[datapack_path.name] = ManageInjectionLabelReq(
                add_labels=[LabelItem(key="tag", value=add_tag)],
                remove_labels=[remove_tag],
            )

    # Batch update injection labels
    if online and manage_req_dict:
        client = get_rcabench_client(base_url=os.environ.get("RCABENCH_BASE_URL"))
        injections_api = InjectionsApi(client)

        with open(mapping_file) as f:
            mapping_data = json.load(f)

        for datapack_name, manage_req in tqdm(manage_req_dict.items()):
            injection_id = mapping_data.get(datapack_name)
            if injection_id is None:
                logger.warning(f"No injection ID found for {datapack_name} in mapping file")
                continue

            try:
                resp = injections_api.manage_injection_labels(
                    id=injection_id,
                    manage=manage_req,
                )
                if resp.code is not None and 200 <= resp.code < 300:
                    logger.info(f"Updated labels for {datapack_name}: {resp.code} - {resp.message}")
            except Exception:
                continue

    # Summary statistics
    valid_count = len(valid_datapacks)
    invalid_count = len(invalid_datapacks)
    deleted_count = 0

    logger.info(f"Validation complete: {valid_count} valid, {invalid_count} invalid")

    if delete_invalid and invalid_datapacks:
        logger.warning(f"Deleting {len(invalid_datapacks)} invalid datapacks...")

        for datapack in invalid_datapacks:
            try:
                import shutil

                for file_path in datapack.iterdir():
                    if file_path.is_file():
                        file_path.unlink()
                    elif file_path.is_dir():
                        shutil.rmtree(file_path)

                datapack.rmdir()
                deleted_count += 1

            except Exception as e:
                logger.error(f"Failed to delete {datapack.name}: {e}")

    summary = {
        "total": total_datapacks,
        "valid": valid_count,
        "invalid": invalid_count,
        "deleted": deleted_count,
    }

    logger.info(f"Final summary: {summary}")
    return summary


@app.command()
@timeit()
def patch_detection():
    """Run patch detection on all valid datapacks that haven't been converted yet."""
    dataset_path: Path = Path("data") / "rcabench_dataset"
    assert dataset_path.exists(), f"Dataset path does not exist: {dataset_path}"

    tasks = []
    skipped = []

    for datapack in dataset_path.iterdir():
        if not datapack.is_dir():
            continue

        # Check if datapack has .valid marker
        valid_marker = datapack / ".valid"
        if not valid_marker.exists():
            skipped.append((datapack.name, "no .valid marker"))
            continue

        # Check if datapack has already been converted
        converted_dir = datapack / "converted"
        if converted_dir.exists():
            skipped.append((datapack.name, "already converted"))
            continue

        tasks.append(functools.partial(run, in_p=datapack, ou_p=datapack, convert=True, online=False))

    logger.info(f"Found {len(tasks)} datapacks to convert (skipped {len(skipped)})")

    if len(tasks) == 0:
        logger.warning("No datapacks found that need conversion")
        return

    cpu = os.cpu_count()
    assert cpu is not None, "Cannot determine CPU count"

    parallel = cpu // 2
    results = fmap_processpool(tasks, parallel=parallel, cpu_limit_each=2, ignore_exceptions=True)

    # Create temp directory if it doesn't exist
    temp_dir = Path("temp")
    temp_dir.mkdir(exist_ok=True)

    with open("temp/patch_skipped.txt", "w") as f:
        for datapack_name, reason in skipped:
            f.write(f"{datapack_name}: {reason}\n")

    with open("temp/patch_results.txt", "w") as f:
        for result in results:
            if result is not None:
                f.write(f"{result['datapack_name']}: converted\n")


if __name__ == "__main__":
    app()
