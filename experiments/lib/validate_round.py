#!/usr/bin/env python3
"""Deterministic validator for inject-loop candidate rounds.

This enforces diversity and dedup-avoidance policy before a round is submitted.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Any


POD_FAMILY = {"PodFailure", "PodKill", "ContainerKill"}


@dataclass
class ValidationConfig:
    batch_size: int | None
    history_window: int
    duration_policy: str
    duration_minutes: int | None
    forbid_duration_override: bool
    max_per_app_divisor: int
    excluded_chaos_types: set[str]


def chaos_type_excluded(chaos_type: str, excluded_chaos_types: set[str]) -> bool:
    for pattern in excluded_chaos_types:
        if pattern.endswith("*"):
            if chaos_type.startswith(pattern[:-1]):
                return True
            continue
        if chaos_type == pattern:
            return True
    return False


def load_json(path: Path) -> dict[str, Any]:
    with path.open() as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: expected top-level JSON object")
    return data


def parse_round_number(path: Path) -> int | None:
    m = re.search(r"round(\d+)", path.stem)
    return int(m.group(1)) if m else None


def load_campaign(path: Path | None) -> ValidationConfig:
    if path is None:
        return ValidationConfig(
            batch_size=None,
            history_window=3,
            duration_policy="fixed",
            duration_minutes=None,
            forbid_duration_override=True,
            max_per_app_divisor=3,
            excluded_chaos_types=set(),
        )

    data = load_json(path)
    return ValidationConfig(
        batch_size=data.get("batch_size"),
        history_window=int(data.get("history_window", 3)),
        duration_policy=str(data.get("duration_policy", "fixed")),
        duration_minutes=data.get("duration_minutes"),
        forbid_duration_override=bool(data.get("forbid_duration_override", True)),
        max_per_app_divisor=int(data.get("max_per_app_divisor", 3)),
        excluded_chaos_types=set(data.get("excluded_chaos_types", [])),
    )


def read_supported_candidates(path: Path, excluded_chaos_types: set[str]) -> list[dict[str, Any]]:
    data = load_json(path)
    candidates = data.get("candidates")
    if not isinstance(candidates, list):
        raise SystemExit(f"{path}: expected .candidates to be a list")
    out = []
    for cand in candidates:
        if not isinstance(cand, dict):
            continue
        chaos_type = cand.get("chaos_type")
        if isinstance(chaos_type, str) and chaos_type_excluded(chaos_type, excluded_chaos_types):
            continue
        out.append(cand)
    return out


def previous_round_files(loop_dir: Path, current_round: int | None, history_window: int) -> list[Path]:
    files = sorted(loop_dir.glob("candidates_round*.json"))
    indexed: list[tuple[int, Path]] = []
    for path in files:
        round_num = parse_round_number(path)
        if round_num is None:
            continue
        if current_round is not None and round_num >= current_round:
            continue
        indexed.append((round_num, path))
    indexed.sort()
    return [path for _, path in indexed[-history_window:]]


def candidate_duration(cand: dict[str, Any], defaults: dict[str, Any]) -> Any:
    if "duration_override" in cand:
        return cand["duration_override"]
    return defaults.get("duration", 5)


def fingerprint(cand: dict[str, Any], defaults: dict[str, Any]) -> tuple[str, str, str, str, Any, Any, Any]:
    app = cand.get("app")
    chaos_type = cand.get("chaos_type")
    params_blob = json.dumps(cand.get("params", {}), sort_keys=True, separators=(",", ":"))
    duration = candidate_duration(cand, defaults)
    interval = defaults.get("interval")
    pre_duration = defaults.get("pre_duration")
    container = cand.get("container") or cand.get("params", {}).get("container") or defaults.get("container")
    return (str(app), str(chaos_type), params_blob, str(container), duration, interval, pre_duration)


def validate_round(
    candidates_path: Path,
    supported_path: Path,
    campaign_path: Path | None,
) -> int:
    config = load_campaign(campaign_path)
    round_doc = load_json(candidates_path)
    candidates = round_doc.get("candidates")
    defaults = round_doc.get("defaults")
    if not isinstance(candidates, list):
        raise SystemExit(f"{candidates_path}: expected .candidates to be a list")
    if not isinstance(defaults, dict):
        raise SystemExit(f"{candidates_path}: expected .defaults to be an object")

    errors: list[str] = []
    warnings: list[str] = []
    loop_dir = candidates_path.parent
    current_round = parse_round_number(candidates_path)
    batch_size = len(candidates)

    if config.batch_size is not None and batch_size != config.batch_size:
        errors.append(
            f"batch size mismatch: campaign expects {config.batch_size}, round has {batch_size} candidates"
        )

    if "interval" not in defaults or "pre_duration" not in defaults:
        errors.append("defaults.interval and defaults.pre_duration are required")

    if config.duration_policy == "fixed":
        if config.duration_minutes is None:
            errors.append("campaign duration_policy=fixed requires duration_minutes")
        else:
            if defaults.get("duration") != config.duration_minutes:
                errors.append(
                    f"defaults.duration must equal fixed campaign duration {config.duration_minutes}, "
                    f"got {defaults.get('duration')!r}"
                )

    per_type = Counter()
    per_app = Counter()
    pod_family_count = 0
    fingerprints = Counter()

    for idx, cand in enumerate(candidates, start=1):
        if not isinstance(cand, dict):
            errors.append(f"candidate #{idx} is not an object")
            continue
        chaos_type = cand.get("chaos_type")
        app = cand.get("app")
        if not chaos_type or not app:
            errors.append(f"candidate #{idx} missing app or chaos_type")
            continue
        if chaos_type_excluded(str(chaos_type), config.excluded_chaos_types):
            errors.append(f"candidate {cand.get('id', idx)} uses excluded chaos_type {chaos_type}")
        if config.forbid_duration_override and "duration_override" in cand:
            errors.append(
                f"candidate {cand.get('id', idx)} sets duration_override, but campaign forbids per-candidate "
                "duration changes"
            )
        per_type[chaos_type] += 1
        per_app[app] += 1
        if chaos_type in POD_FAMILY:
            pod_family_count += 1
        fingerprints[fingerprint(cand, defaults)] += 1

    duplicated = [fp for fp, n in fingerprints.items() if n > 1]
    if duplicated:
        errors.append(f"round contains {len(duplicated)} duplicate fingerprint(s); dedup risk is guaranteed")

    max_pod = math.ceil(batch_size / 2)
    if pod_family_count > max_pod:
        errors.append(f"pod-family cap exceeded: {pod_family_count} > {max_pod}")

    max_per_app = math.ceil(batch_size / max(config.max_per_app_divisor, 1))
    noisy_apps = sorted(app for app, count in per_app.items() if count > max_per_app)
    if noisy_apps:
        joined = ", ".join(f"{app}={per_app[app]}" for app in noisy_apps)
        errors.append(f"per-app cap exceeded (max {max_per_app}): {joined}")

    supported = read_supported_candidates(supported_path, config.excluded_chaos_types)
    supported_types = Counter()
    supported_pairs: set[tuple[str, str]] = set()
    for cand in supported:
        app = cand.get("app")
        chaos_type = cand.get("chaos_type")
        if not app or not chaos_type:
            continue
        supported_types[str(chaos_type)] += 1
        supported_pairs.add((str(app), str(chaos_type)))

    for cand in candidates:
        app = str(cand.get("app"))
        chaos_type = str(cand.get("chaos_type"))
        if chaos_type not in supported_types:
            errors.append(f"candidate {cand.get('id')} uses unsupported chaos_type {chaos_type}")
        elif (app, chaos_type) not in supported_pairs:
            warnings.append(
                f"candidate {cand.get('id')} uses app/chaos_type pair ({app}, {chaos_type}) not present in "
                "supported catalog"
            )

    recent_rounds = previous_round_files(loop_dir, current_round, config.history_window)
    history_types = Counter()
    for path in recent_rounds:
        try:
            doc = load_json(path)
        except Exception as exc:  # pragma: no cover - defensive
            warnings.append(f"skipping unreadable history file {path}: {exc}")
            continue
        for cand in doc.get("candidates", []):
            if not isinstance(cand, dict):
                continue
            chaos_type = cand.get("chaos_type")
            if isinstance(chaos_type, str) and chaos_type_excluded(chaos_type, config.excluded_chaos_types):
                continue
            if chaos_type:
                history_types[str(chaos_type)] += 1

    missing_recent = [
        chaos_type
        for chaos_type, count in supported_types.items()
        if count > 0 and history_types.get(chaos_type, 0) == 0
    ]
    unmet_missing = [chaos_type for chaos_type in missing_recent if per_type.get(chaos_type, 0) == 0]
    if unmet_missing:
        errors.append(
            "missing-type floor violated; these supported chaos types had zero picks in the last "
            f"{config.history_window} rounds and still have no slot now: {', '.join(sorted(unmet_missing))}"
        )

    print(f"round: {candidates_path}")
    print(f"candidate_count: {batch_size}")
    print(f"chaos_type_counts: {json.dumps(dict(sorted(per_type.items())), sort_keys=True)}")
    print(f"app_counts: {json.dumps(dict(sorted(per_app.items())), sort_keys=True)}")
    print(f"supported_chaos_types: {len(supported_types)}")
    print(f"history_window: {len(recent_rounds)} round(s)")
    if recent_rounds:
        print("history_files:")
        for path in recent_rounds:
            print(f"  - {path}")
    if missing_recent:
        print(f"missing_recent_types: {', '.join(sorted(missing_recent))}")
    if warnings:
        print("warnings:")
        for msg in warnings:
            print(f"  - {msg}")
    if errors:
        print("errors:")
        for msg in errors:
            print(f"  - {msg}")
        return 1
    print("result: OK")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--candidates", required=True, type=Path)
    ap.add_argument("--supported", required=True, type=Path)
    ap.add_argument("--campaign", type=Path)
    args = ap.parse_args()
    return validate_round(args.candidates, args.supported, args.campaign)


if __name__ == "__main__":
    sys.exit(main())
