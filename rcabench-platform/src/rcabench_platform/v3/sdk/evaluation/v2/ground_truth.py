"""Extract ground-truth fault list from injection.json.

Two on-disk formats are supported:
  1. New (aegisctl detector_success): `engine_config` is a JSON list of dicts
     with `app`, `chaos_type`, `target_service`, `direction`, `class`, `method`.
  2. Old (FSE/openrca2): `engine_config` is an opaque JSON-encoded string,
     `fault_type` is numeric. We fall back to data.jsonl side-channel for the
     canonical chaos_type label, and read `ground_truth.service[0]` for the app.
"""
from __future__ import annotations

import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from pydantic import BaseModel, Field

from .fault_kind import NETWORK_KINDS, FaultKind, map_chaos_type


class GTFault(BaseModel):
    """One ground-truth fault entry (one item from engine_config)."""

    service: str
    fault_kind: FaultKind
    direction_src: str | None = None
    direction_dst: str | None = None
    method: str | None = Field(
        default=None,
        description="Canonical class.method (jvm/http only).",
    )

    raw_chaos_type: str | None = None


class GTContext(BaseModel):
    """All ground-truth signal a single case carries."""

    faults: list[GTFault]
    start_time_ns: int | None = None
    end_time_ns: int | None = None


_OLD_INDEX_PATH = Path("/home/ddq/AoyangSpace/dataset/rca/data.jsonl")
_OLD_INDEX_CACHE: dict[str, dict[str, Any]] | None = None


def _load_old_index() -> dict[str, dict[str, Any]]:
    global _OLD_INDEX_CACHE
    if _OLD_INDEX_CACHE is None:
        idx: dict[str, dict[str, Any]] = {}
        if _OLD_INDEX_PATH.exists():
            with _OLD_INDEX_PATH.open() as f:
                for line in f:
                    try:
                        row = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    key = row.get("source") or row.get("datapack_name")
                    if key:
                        idx[key] = row
        _OLD_INDEX_CACHE = idx
    return _OLD_INDEX_CACHE


def _parse_iso_to_ns(value: Any) -> int | None:
    if value is None:
        return None
    if isinstance(value, (int, float)):
        v = int(value)
        return v * 1_000_000_000 if v < 1_000_000_000_000_000 else v
    if not isinstance(value, str):
        return None
    s = value.strip()
    if not s:
        return None
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    try:
        dt = datetime.fromisoformat(s)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return int(dt.timestamp() * 1_000_000_000)


def _build_method(class_name: str | None, method_name: str | None) -> str | None:
    if not method_name:
        return None
    return f"{class_name}.{method_name}" if class_name else method_name


def _new_format_faults(engine_config: list[dict[str, Any]]) -> list[GTFault]:
    faults: list[GTFault] = []
    for leaf in engine_config:
        if not isinstance(leaf, dict):
            continue
        app = leaf.get("app")
        if not app:
            continue
        chaos_type = leaf.get("chaos_type") or ""
        kind = map_chaos_type(chaos_type)
        src = dst = None
        if kind in NETWORK_KINDS:
            src = app
            dst = leaf.get("target_service")
        method = None
        if kind in {FaultKind.JVM_EXCEPTION, FaultKind.JVM_MUTATOR, FaultKind.HTTP_ABORT, FaultKind.HTTP_REPLACE}:
            method = _build_method(leaf.get("class"), leaf.get("method"))
        faults.append(
            GTFault(
                service=str(app),
                fault_kind=kind,
                direction_src=src,
                direction_dst=dst,
                method=method,
                raw_chaos_type=chaos_type,
            )
        )
    return faults


def _old_format_faults(injection: dict[str, Any], case_name: str | None) -> list[GTFault]:
    canonical_chaos: str | None = None
    if case_name:
        old = _load_old_index().get(case_name) or {}
        ft = old.get("fault_type")
        if isinstance(ft, str) and ft:
            canonical_chaos = ft

    gt = injection.get("ground_truth") or {}
    if isinstance(gt, list):
        gt = gt[0] if gt and isinstance(gt[0], dict) else {}
    if not isinstance(gt, dict):
        return []
    services = gt.get("service") or []
    if not services:
        return []
    service = str(services[0])

    function = gt.get("function") or []
    method = str(function[0]) if function and function[0] else None

    return [
        GTFault(
            service=service,
            fault_kind=map_chaos_type(canonical_chaos),
            direction_src=None,
            direction_dst=None,
            method=method,
            raw_chaos_type=canonical_chaos,
        )
    ]


def extract_gt_faults(injection: dict[str, Any], case_name: str | None = None) -> GTContext:
    engine = injection.get("engine_config")
    if isinstance(engine, list) and engine and isinstance(engine[0], dict):
        faults = _new_format_faults(engine)
    else:
        faults = _old_format_faults(injection, case_name)

    return GTContext(
        faults=faults,
        start_time_ns=_parse_iso_to_ns(injection.get("start_time")),
        end_time_ns=_parse_iso_to_ns(injection.get("end_time")),
    )
