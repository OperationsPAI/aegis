"""Conclusion alarm evidence normalization and accounting."""

from __future__ import annotations

import json
import re
from collections import defaultdict
from dataclasses import dataclass
from typing import Any

from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationResult

_WEAK_ALARM_CONFIDENCE_CAP = 0.65
_NO_ISSUE_ALARM_CONFIDENCE_CAP = 0.45
_UNKNOWN_ALARM_CONFIDENCE_CAP = 0.80
_ALARM_EVIDENCE_INDEX_KEY = "__alarm_evidence_index__"
_HTTP_METHODS = frozenset({"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"})


@dataclass(frozen=True)
class _AlarmIdentity:
    raw: str
    component_id: str | None = None
    service: str | None = None
    operation: str | None = None
    method: str | None = None
    host: str | None = None
    path: str | None = None
    normalized_path: str | None = None


def _parse_issues_payload(issues_raw: Any) -> dict[str, Any]:
    if issues_raw is None:
        return {}
    if isinstance(issues_raw, dict):
        return issues_raw
    if not isinstance(issues_raw, str):
        return {}
    payload = issues_raw.strip()
    if payload in {"", "{}", "null", "None"}:
        return {}
    try:
        parsed = json.loads(payload)
    except (TypeError, json.JSONDecodeError):
        return {}
    return parsed if isinstance(parsed, dict) else {}


def _safe_float(value: Any, default: float = 0.0) -> float:
    try:
        if value is None:
            return default
        return float(value)
    except (TypeError, ValueError):
        return default


def _ratio(after: float, before: float) -> float:
    if before <= 1e-9:
        return 0.0
    return after / before


def _normalize_alarm_path(path: str | None) -> str | None:
    if not path:
        return None
    normalized = path.split("?", 1)[0].split("#", 1)[0].strip()
    if not normalized:
        return None
    if not normalized.startswith("/"):
        normalized = "/" + normalized
    normalized = re.sub(r"/+", "/", normalized)
    if len(normalized) > 1:
        normalized = normalized.rstrip("/")
    return normalized


def _lower_key(value: str | None) -> str | None:
    return value.lower() if value else None


def _parse_http_operation(operation: str) -> tuple[str | None, str | None, str | None]:
    op = operation.strip()
    full_url = re.match(r"^(?:HTTP\s+)?([A-Z]+)\s+https?://([^/:?\s]+)(?::\d+)?([^\s?#]*)", op)
    if full_url:
        method, host, path = full_url.groups()
        return method.upper(), host, _normalize_alarm_path(path or "/")

    method_path = re.match(r"^([A-Z]+)\s+(/[^\s?#]*)", op)
    if method_path and method_path.group(1).upper() in _HTTP_METHODS:
        method, path = method_path.groups()
        return method.upper(), None, _normalize_alarm_path(path)

    bare_path = re.match(r"^(?:HTTP\s+)?(/[^\s?#]*)", op)
    if bare_path:
        return None, None, _normalize_alarm_path(bare_path.group(1))

    return None, None, None


def _parse_alarm_identity(label: str) -> _AlarmIdentity:
    raw = str(label or "").strip()
    component_id = raw if "|" in raw else None
    span_self_name = _span_self_name_from_component(raw)
    service: str | None = None
    operation = span_self_name
    if "::" in span_self_name:
        service, operation = span_self_name.split("::", 1)
        service = service.strip() or None

    method, host, normalized_path = _parse_http_operation(operation)
    canonical_operation = operation.strip() or None
    if method and normalized_path and re.match(r"^(?:HTTP\s+)?[A-Z]+\s+https?://", operation.strip()):
        canonical_operation = f"{method} {normalized_path}"
    path = normalized_path

    return _AlarmIdentity(
        raw=raw,
        component_id=component_id,
        service=service,
        operation=canonical_operation,
        method=method,
        host=host,
        path=path,
        normalized_path=normalized_path,
    )


def _normalize_conclusion_span_name(span_name: str) -> str:
    """Map conclusion.parquet span labels to graph span self_name when possible."""
    identity = _parse_alarm_identity(span_name)
    if identity.host and identity.method and identity.normalized_path:
        return f"{identity.host}::{identity.method} {identity.normalized_path}"
    return identity.raw


def _classify_conclusion_alarm(row: dict[str, Any]) -> dict[str, Any]:
    issues = _parse_issues_payload(row.get("Issues"))
    normal_success = _safe_float(row.get("NormalSuccRate"), 1.0)
    abnormal_success = _safe_float(row.get("AbnormalSuccRate"), normal_success)
    normal_avg = _safe_float(row.get("NormalAvgDuration"))
    abnormal_avg = _safe_float(row.get("AbnormalAvgDuration"))
    normal_p99 = _safe_float(row.get("NormalP99"))
    abnormal_p99 = _safe_float(row.get("AbnormalP99"))

    success_drop = max(0.0, normal_success - abnormal_success)
    avg_ratio = _ratio(abnormal_avg, normal_avg)
    p99_ratio = _ratio(abnormal_p99, normal_p99)
    avg_abs_change = max(0.0, abnormal_avg - normal_avg)
    p99_abs_change = max(0.0, abnormal_p99 - normal_p99)

    if issues:
        strength = "strong"
        reason = "conclusion_issues"
    elif success_drop >= 0.10:
        strength = "strong"
        reason = "success_rate_drop"
    elif (avg_ratio >= 2.0 and avg_abs_change >= 1.0) or (p99_ratio >= 5.0 and p99_abs_change >= 3.0):
        strength = "strong"
        reason = "material_latency_anomaly"
    elif avg_ratio >= 1.5 or p99_ratio >= 2.0 or avg_abs_change >= 0.5 or p99_abs_change >= 1.0:
        strength = "weak"
        reason = "weak_latency_signal"
    else:
        strength = "none"
        reason = "no_material_conclusion_signal"

    return {
        "issue_strength": strength,
        "issue_strength_reason": reason,
        "has_issues": bool(issues),
        "issues": issues,
        "normal_success_rate": normal_success,
        "abnormal_success_rate": abnormal_success,
        "success_rate_drop": success_drop,
        "normal_avg_duration": normal_avg,
        "abnormal_avg_duration": abnormal_avg,
        "avg_duration_ratio": avg_ratio,
        "normal_p99": normal_p99,
        "abnormal_p99": abnormal_p99,
        "p99_ratio": p99_ratio,
    }


def _new_alarm_index() -> dict[str, Any]:
    return {
        "component": defaultdict(list),
        "service_operation": defaultdict(list),
        "operation": defaultdict(list),
        "owner_http": defaultdict(list),
        "method_path": defaultdict(list),
        "path": defaultdict(list),
    }


def _append_alarm_index(index: dict[str, Any], identity: _AlarmIdentity, evidence: dict[str, Any]) -> None:
    entry = {"identity": identity, "evidence": evidence}
    if identity.component_id:
        index["component"][identity.component_id].append(entry)
    if identity.operation:
        index["operation"][identity.operation].append(entry)
    owners = [owner for owner in (identity.service, identity.host) if owner]
    for owner in owners:
        if identity.operation:
            index["service_operation"][(_lower_key(owner), identity.operation)].append(entry)
        if identity.method and identity.normalized_path:
            index["owner_http"][(_lower_key(owner), identity.method, identity.normalized_path)].append(entry)
    if identity.method and identity.normalized_path:
        index["method_path"][(identity.method, identity.normalized_path)].append(entry)
    if identity.normalized_path:
        index["path"][identity.normalized_path].append(entry)


def _load_alarm_evidence_index(loader: ParquetDataLoader) -> dict[str, Any]:
    try:
        conclusion_df = loader.load_conclusion()
    except (AttributeError, FileNotFoundError):
        return {}

    evidence_by_name: dict[str, Any] = {_ALARM_EVIDENCE_INDEX_KEY: _new_alarm_index()}
    for row in conclusion_df.iter_rows(named=True):
        raw_name = str(row.get("SpanName") or "")
        if not raw_name:
            continue
        evidence = _classify_conclusion_alarm(row)
        evidence["conclusion_span_name"] = raw_name
        evidence_by_name[raw_name] = evidence
        normalized_name = _normalize_conclusion_span_name(raw_name)
        evidence_by_name[normalized_name] = evidence
        _append_alarm_index(evidence_by_name[_ALARM_EVIDENCE_INDEX_KEY], _parse_alarm_identity(raw_name), evidence)
        if normalized_name != raw_name:
            _append_alarm_index(
                evidence_by_name[_ALARM_EVIDENCE_INDEX_KEY],
                _parse_alarm_identity(normalized_name),
                evidence,
            )
    return evidence_by_name


def _span_self_name_from_component(component: str) -> str:
    return component.split("|", 1)[1] if component.startswith("span|") else component


def _match_attempt(label: str, key: Any) -> str:
    if isinstance(key, tuple):
        rendered = "::".join(str(part) for part in key if part is not None)
    else:
        rendered = str(key)
    return f"{label}:{rendered}"


def _unique_entries(entries: list[dict[str, Any]]) -> list[dict[str, Any]]:
    seen: set[str] = set()
    unique: list[dict[str, Any]] = []
    for entry in entries:
        raw = str(entry["evidence"].get("conclusion_span_name") or entry["identity"].raw)
        if raw in seen:
            continue
        seen.add(raw)
        unique.append(entry)
    return unique


def _evidence_with_conclusion_match(entry: dict[str, Any], method: str, attempted_keys: list[str]) -> dict[str, Any]:
    evidence = dict(entry["evidence"])
    conclusion_span_name = str(evidence.get("conclusion_span_name") or entry["identity"].raw)
    evidence["conclusion_span_name"] = conclusion_span_name
    evidence["conclusion_match"] = {
        "status": "matched",
        "method": method,
        "conclusion_span_name": conclusion_span_name,
        "attempted_keys": attempted_keys,
    }
    return evidence


def _ambiguous_alarm_evidence(method: str, attempted_keys: list[str], entries: list[dict[str, Any]]) -> dict[str, Any]:
    candidates = sorted(
        {str(entry["evidence"].get("conclusion_span_name") or entry["identity"].raw) for entry in entries}
    )
    return {
        "issue_strength": "unknown",
        "issue_strength_reason": "ambiguous_conclusion_match",
        "has_issues": False,
        "conclusion_match": {
            "status": "ambiguous",
            "method": method,
            "attempted_keys": attempted_keys,
            "candidates": candidates,
        },
    }


def _unmatched_alarm_evidence(attempted_keys: list[str]) -> dict[str, Any]:
    return {
        "issue_strength": "unknown",
        "issue_strength_reason": "conclusion_row_unavailable",
        "has_issues": False,
        "conclusion_match": {
            "status": "unmatched",
            "method": "none",
            "attempted_keys": attempted_keys,
        },
    }


def _select_alarm_entries(
    entries: list[dict[str, Any]],
    method: str,
    attempted_keys: list[str],
) -> dict[str, Any] | None:
    unique = _unique_entries(entries)
    if not unique:
        return None
    if len(unique) == 1:
        return _evidence_with_conclusion_match(unique[0], method, attempted_keys)
    return _ambiguous_alarm_evidence(method, attempted_keys, unique)


def _alarm_evidence_for_node(
    node_id: int,
    graph: HyperGraph,
    evidence_by_name: dict[str, Any],
) -> dict[str, Any]:
    node = graph.get_node_by_id(node_id)
    component = node.uniq_name
    span_self_name = _span_self_name_from_component(component)
    node_identity = _parse_alarm_identity(component)
    attempts: list[str] = []

    index = evidence_by_name.get(_ALARM_EVIDENCE_INDEX_KEY)
    if isinstance(index, dict):
        match_steps = [
            ("exact_component", "component", node_identity.component_id),
            (
                "service_operation",
                "service_operation",
                (_lower_key(node_identity.service), node_identity.operation)
                if node_identity.service and node_identity.operation
                else None,
            ),
            ("bare_operation_unique", "operation", node_identity.operation),
            (
                "http_endpoint",
                "owner_http",
                (
                    _lower_key(node_identity.service or node_identity.host),
                    node_identity.method,
                    node_identity.normalized_path,
                )
                if (node_identity.service or node_identity.host)
                and node_identity.method
                and node_identity.normalized_path
                else None,
            ),
            (
                "http_endpoint_unique",
                "method_path",
                (node_identity.method, node_identity.normalized_path)
                if node_identity.method and node_identity.normalized_path
                else None,
            ),
            ("bare_path_unique", "path", node_identity.normalized_path),
        ]
        for method, bucket, key in match_steps:
            if key is None:
                continue
            attempts.append(_match_attempt(method, key))
            selected = _select_alarm_entries(index[bucket].get(key, []), method, attempts)
            if selected is not None:
                return selected

    for raw_key in (component, span_self_name, node_identity.operation):
        if not raw_key:
            continue
        attempts.append(_match_attempt("legacy_exact", raw_key))
        evidence = evidence_by_name.get(raw_key)
        if isinstance(evidence, dict):
            out = dict(evidence)
            out.setdefault(
                "conclusion_match",
                {
                    "status": "matched",
                    "method": "legacy_exact",
                    "conclusion_span_name": out.get("conclusion_span_name", raw_key),
                    "attempted_keys": attempts,
                },
            )
            return out
    return _unmatched_alarm_evidence(attempts)


def _alarm_detail(
    node_id: int,
    graph: HyperGraph,
    evidence_by_name: dict[str, Any],
    *,
    reason: str | None = None,
    path_status: str | None = None,
) -> dict[str, Any]:
    node = graph.get_node_by_id(node_id)
    evidence = _alarm_evidence_for_node(node_id, graph, evidence_by_name)
    out: dict[str, Any] = {
        "node_id": node_id,
        "component": node.uniq_name,
        "issue_strength": evidence["issue_strength"],
        "issue_strength_reason": evidence["issue_strength_reason"],
    }
    if reason is not None:
        out["reason"] = reason
    if path_status is not None:
        out["path_status"] = path_status
    for key in (
        "has_issues",
        "normal_success_rate",
        "abnormal_success_rate",
        "success_rate_drop",
        "normal_avg_duration",
        "abnormal_avg_duration",
        "avg_duration_ratio",
        "normal_p99",
        "abnormal_p99",
        "p99_ratio",
        "conclusion_span_name",
        "conclusion_match",
    ):
        if key in evidence:
            out[key] = evidence[key]
    return out


def _path_terminal_alarm_ids(result: PropagationResult, alarm_nodes: set[int]) -> set[int]:
    return {path.nodes[-1] for path in result.paths if path.nodes and path.nodes[-1] in alarm_nodes}


def _path_terminal_alarm_path_ids(result: PropagationResult, alarm_nodes: set[int]) -> dict[int, list[str]]:
    terminal_path_ids: dict[int, list[str]] = defaultdict(list)
    for idx, path in enumerate(result.paths):
        if path.nodes and path.nodes[-1] in alarm_nodes:
            terminal_path_ids[path.nodes[-1]].append(f"path-{idx}")
    return dict(terminal_path_ids)


def _confidence_cap_for_strength(strength: str) -> float | None:
    if strength == "weak":
        return _WEAK_ALARM_CONFIDENCE_CAP
    if strength == "none":
        return _NO_ISSUE_ALARM_CONFIDENCE_CAP
    if strength == "unknown":
        return _UNKNOWN_ALARM_CONFIDENCE_CAP
    return None


def _apply_terminal_alarm_confidence_caps(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, Any],
) -> None:
    for path in result.paths:
        if not path.nodes or path.nodes[-1] not in alarm_nodes:
            continue
        strength = _alarm_evidence_for_node(path.nodes[-1], graph, evidence_by_name)["issue_strength"]
        cap = _confidence_cap_for_strength(strength)
        if cap is not None and path.confidence > cap:
            path.confidence = cap


def _split_default_and_weak_paths(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, Any],
) -> tuple[list[Any], list[Any]]:
    default_paths = []
    weak_paths = []
    for path in result.paths:
        if path.nodes and path.nodes[-1] in alarm_nodes:
            strength = _alarm_evidence_for_node(path.nodes[-1], graph, evidence_by_name)["issue_strength"]
            if strength in {"weak", "none"}:
                weak_paths.append(path)
                continue
        default_paths.append(path)

    # Avoid producing an empty causal graph for datasets where conclusion rows
    # are unavailable or all alarm evidence is weak; weak_paths still makes the
    # isolation explicit in result.json.
    if not default_paths and weak_paths:
        return weak_paths, []
    return default_paths, weak_paths


def _build_alarm_accounting(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, Any],
) -> dict[str, Any]:
    explained_ids = _path_terminal_alarm_ids(result, alarm_nodes)
    terminal_path_ids = _path_terminal_alarm_path_ids(result, alarm_nodes)
    unexplained_ids = set(alarm_nodes) - explained_ids
    candidate_details = [_alarm_detail(nid, graph, evidence_by_name) for nid in sorted(alarm_nodes)]
    explained_details = [
        _alarm_detail(nid, graph, evidence_by_name, reason="path_terminal", path_status="explained")
        | {"path_ids": terminal_path_ids.get(nid, [])}
        for nid in sorted(explained_ids)
    ]
    unexplained_details = []
    for nid in sorted(unexplained_ids):
        evidence = _alarm_evidence_for_node(nid, graph, evidence_by_name)
        strength = evidence["issue_strength"]
        match_status = evidence.get("conclusion_match", {}).get("status")
        path_status = "strong_unexplained" if strength == "strong" else "unexplained"
        if strength == "strong":
            drop_reason = "no_path_found"
        elif match_status == "ambiguous":
            drop_reason = "ambiguous_conclusion_match"
        elif match_status == "unmatched":
            drop_reason = "schema_unmatched"
        else:
            drop_reason = "weak_noise"
        unexplained_details.append(
            _alarm_detail(
                nid,
                graph,
                evidence_by_name,
                reason="no_path_found",
                path_status=path_status,
            )
            | {"drop_reason": drop_reason}
        )

    candidate_strong_count = sum(1 for detail in candidate_details if detail["issue_strength"] == "strong")
    explained_strong_count = sum(1 for detail in explained_details if detail["issue_strength"] == "strong")
    strong_alarm_coverage = None if candidate_strong_count == 0 else explained_strong_count / candidate_strong_count

    out: dict[str, Any] = {
        "candidate_alarm_nodes": candidate_details,
        "explained_alarm_nodes": explained_details,
        "unexplained_alarm_nodes": unexplained_details,
        "path_terminal_alarm_nodes": explained_details,
        "candidate_alarm_node_ids": sorted(alarm_nodes),
        "explained_alarm_node_ids": sorted(explained_ids),
        "unexplained_alarm_node_ids": sorted(unexplained_ids),
        "path_terminal_alarm_node_ids": sorted(explained_ids),
        "candidate_alarm_count": len(candidate_details),
        "explained_alarm_count": len(explained_details),
        "unexplained_alarm_count": len(unexplained_details),
        "path_terminal_alarm_count": len(explained_details),
        "strong_alarm_coverage": strong_alarm_coverage,
        "candidate_strong_alarm_count": candidate_strong_count,
        "explained_strong_alarm_count": explained_strong_count,
        "unexplained_strong_alarm_count": candidate_strong_count - explained_strong_count,
    }
    if candidate_strong_count == 0:
        out["strong_alarm_coverage_reason"] = "no_candidate_strong_alarms"
    return out


def _evidence_confidence_for_strength(strength: str) -> float:
    if strength == "strong":
        return 1.0
    if strength == "weak":
        return 0.5
    return 0.0
