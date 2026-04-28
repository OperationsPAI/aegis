#!/usr/bin/env python3
"""Migrate inject-loop round files to fixed-duration schema."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any


def round_number(path: Path) -> int | None:
    match = re.search(r"round(\d+)", path.stem)
    return int(match.group(1)) if match else None


def load_round(path: Path) -> dict[str, Any]:
    with path.open() as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: expected top-level object")
    if not isinstance(data.get("candidates"), list):
        raise SystemExit(f"{path}: expected candidates list")
    if not isinstance(data.get("defaults"), dict):
        data["defaults"] = {}
    return data


def save_round(path: Path, data: dict[str, Any]) -> None:
    path.write_text(json.dumps(data, indent=2, ensure_ascii=True) + "\n")


def migrate_round(path: Path, duration: int, preserve_legacy: bool) -> tuple[bool, int]:
    data = load_round(path)
    changed = False
    migrated_candidates = 0

    defaults = data.setdefault("defaults", {})
    if defaults.get("duration") != duration:
        defaults["duration"] = duration
        changed = True

    for cand in data["candidates"]:
        if not isinstance(cand, dict):
            continue
        if "duration_override" in cand:
            old_duration = cand.pop("duration_override")
            if preserve_legacy:
                cand["_legacy_duration_override"] = old_duration
            migrated_candidates += 1
            changed = True

    note = (
        f"Migrated to fixed-duration schema with defaults.duration={duration}. "
        "Candidate-level duration_override removed from routine round generation."
    )
    if data.get("_migration_note") != note:
        data["_migration_note"] = note
        changed = True

    if changed:
        save_round(path, data)
    return changed, migrated_candidates


def select_rounds(loop_dir: Path, round_numbers: list[int]) -> list[Path]:
    files = sorted(loop_dir.glob("candidates_round*.json"))
    if not round_numbers:
        return files
    wanted = set(round_numbers)
    out = []
    for path in files:
        num = round_number(path)
        if num in wanted:
            out.append(path)
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--loop-dir", required=True, type=Path)
    ap.add_argument("--duration", required=True, type=int)
    ap.add_argument("--round", dest="rounds", action="append", type=int, default=[])
    ap.add_argument("--write", action="store_true", help="apply changes in place")
    ap.add_argument("--no-preserve-legacy", action="store_true")
    args = ap.parse_args()

    round_files = select_rounds(args.loop_dir, args.rounds)
    if not round_files:
        raise SystemExit(f"no candidates_round*.json found under {args.loop_dir}")

    changed_paths: list[Path] = []
    summary: list[str] = []

    for path in round_files:
        data = load_round(path)
        defaults = data.get("defaults", {})
        migrated_candidates = sum(
            1 for cand in data["candidates"] if isinstance(cand, dict) and "duration_override" in cand
        )
        will_change = defaults.get("duration") != args.duration or migrated_candidates > 0
        summary.append(
            f"{path}: defaults.duration {defaults.get('duration')!r} -> {args.duration}, "
            f"candidate_overrides={migrated_candidates}"
        )
        if args.write and will_change:
            changed, _ = migrate_round(path, args.duration, not args.no_preserve_legacy)
            if changed:
                changed_paths.append(path)

    mode = "write" if args.write else "dry-run"
    print(f"mode: {mode}")
    for line in summary:
        print(line)
    if args.write:
        print(f"changed_files: {len(changed_paths)}")
    else:
        print("No files changed. Re-run with --write to apply.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
