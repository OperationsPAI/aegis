"""Extract ground-truth fault list from injection.json.

Two on-disk formats are supported, both keyed off the actual `injection.json`
file (no external side-channel). The new format embeds chaos_type strings
directly; the old format encodes the same information across `fault_type`
(numeric index) + `display_config` (JSON string with injection_point + direction).

  1. New (aegisctl detector_success): `engine_config` is a JSON list of dicts
     with `app`, `chaos_type`, `target_service`, `direction`, `class`, `method`.

  2. Old (FSE/openrca2): `engine_config` is an opaque JSON-encoded tree;
     `fault_type` is a numeric index into ``CANONICAL_FAULT_TYPES``;
     `display_config` is a JSON string with::

         {"direction": "to|from|both",
          "injection_point": {"source_service": "X", "target_service": "Y",
                              "app_name": ..., "class_name": ..., "method_name": ...},
          "namespace": ...}

     Service comes from ground_truth.service[0] OR display_config.injection_point.
     Direction (only meaningful for network_*) comes from display_config.
     A legacy data.jsonl side-channel is consulted only when injection.json
     itself is missing the canonical fault_type.
"""

from __future__ import annotations

import json
import os
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from pydantic import BaseModel, Field

from .fault_kind import (
    METHOD_RELEVANT_KINDS,
    NETWORK_KINDS,
    FaultKind,
    chaos_type_from_index,
    map_chaos_type,
)


class GTFault(BaseModel):
    """One ground-truth fault entry (one item from engine_config)."""

    service: str
    fault_kind: FaultKind
    direction_src: str | None = None
    direction_dst: str | None = None
    method: str | None = Field(
        default=None,
        description="Canonical class.method for jvm/http kinds; None otherwise.",
    )

    raw_chaos_type: str | None = None


class GTContext(BaseModel):
    """All ground-truth signal a single case carries."""

    faults: list[GTFault]
    start_time_ns: int | None = None
    end_time_ns: int | None = None


# Set RCABENCH_LEGACY_INDEX to the absolute path of a data.jsonl side-channel
# (rows must carry `source`/`datapack_name` + `fault_type`) for old-format
# cases whose injection.json is missing both `fault_type` and a decodable
# `display_config`. Unset → side-channel disabled, which is the right default
# for any environment that isn't the original FSE/openrca2 dump.
_LEGACY_INDEX_ENV = "RCABENCH_LEGACY_INDEX"
_LEGACY_INDEX_CACHE: dict[str, dict[str, Any]] | None = None


def _load_legacy_index() -> dict[str, dict[str, Any]]:
    """Last-resort fallback: read fault_type from a data.jsonl pointed at by
    ``$RCABENCH_LEGACY_INDEX``. Only consulted when injection.json itself is
    missing both `fault_type` (numeric) and any decodable `display_config`.
    """
    global _LEGACY_INDEX_CACHE
    if _LEGACY_INDEX_CACHE is None:
        idx: dict[str, dict[str, Any]] = {}
        env_path = os.environ.get(_LEGACY_INDEX_ENV, "").strip()
        if env_path:
            path = Path(env_path).expanduser()
            if path.exists():
                with path.open() as f:
                    for line in f:
                        try:
                            row = json.loads(line)
                        except json.JSONDecodeError:
                            continue
                        key = row.get("source") or row.get("datapack_name")
                        if key:
                            idx[key] = row
        _LEGACY_INDEX_CACHE = idx
    return _LEGACY_INDEX_CACHE


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
        if kind in METHOD_RELEVANT_KINDS:
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


def _decode_display_config(injection: dict[str, Any]) -> dict[str, Any]:
    """`display_config` is a JSON-encoded string in old-format injection.json.

    Returns the parsed dict, or {} if absent / malformed.
    """
    raw = injection.get("display_config")
    if raw is None:
        return {}
    if isinstance(raw, dict):
        return raw
    if isinstance(raw, str):
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return {}
    return {}


def _old_format_faults(injection: dict[str, Any], case_name: str | None) -> list[GTFault]:
    """Decode old-format injection.json. Single-leg only — old format never
    carried hybrid injections.

    Resolution order for chaos_type:
      1. injection.json's `fault_type` numeric index → CANONICAL_FAULT_TYPES
      2. data.jsonl side-channel for the case (keyed by case_name)

    Resolution order for service / direction:
      1. display_config.injection_point.{source_service, target_service, app_name}
         + display_config.direction
      2. ground_truth.service[0] (single-service fallback)
    """
    # 1. chaos_type — try numeric index first, then side-channel
    chaos_type = chaos_type_from_index(injection.get("fault_type"))
    if chaos_type is None and case_name:
        legacy = _load_legacy_index().get(case_name) or {}
        ft = legacy.get("fault_type")
        if isinstance(ft, str) and ft:
            chaos_type = ft
    kind = map_chaos_type(chaos_type)

    # 2. injection_point — display_config carries the rich form; ground_truth
    #    carries a flat service list as fallback
    dc = _decode_display_config(injection)
    ip = dc.get("injection_point") if isinstance(dc, dict) else None
    if not isinstance(ip, dict):
        ip = {}

    direction = dc.get("direction") if isinstance(dc, dict) else None
    src_svc = ip.get("source_service") or ip.get("app_name")
    dst_svc = ip.get("target_service")

    gt = injection.get("ground_truth") or {}
    if isinstance(gt, list):
        gt = gt[0] if gt and isinstance(gt[0], dict) else {}
    gt = gt if isinstance(gt, dict) else {}

    services = gt.get("service") or []
    function = gt.get("function") or []

    # Pick the primary service: prefer src_svc (the side that has the chaos rule)
    # then fall back to ground_truth.service[0]
    service = src_svc or (str(services[0]) if services else None)
    if not service:
        return []

    # method (jvm / http only)
    method: str | None = None
    if kind in METHOD_RELEVANT_KINDS:
        method = _build_method(ip.get("class_name"), ip.get("method_name"))
        if method is None and function:
            method = str(function[0])

    # direction.src/dst (network only)
    direction_src: str | None = None
    direction_dst: str | None = None
    if kind in NETWORK_KINDS:
        # display_config pair already gives src/dst regardless of direction value;
        # rcabench convention: the src is the side the netem rule sits on.
        direction_src = src_svc
        direction_dst = dst_svc
        # When direction is "from", the netem rule is on the listed target_service
        # shaping traffic FROM source_service. Swap so direction.src == netem side.
        if isinstance(direction, str) and direction.lower() == "from":
            direction_src, direction_dst = dst_svc, src_svc

    return [
        GTFault(
            service=str(service),
            fault_kind=kind,
            direction_src=direction_src,
            direction_dst=direction_dst,
            method=method,
            raw_chaos_type=chaos_type,
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
