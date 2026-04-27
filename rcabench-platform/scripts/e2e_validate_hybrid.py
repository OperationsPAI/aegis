#!/usr/bin/env -S uv run -s
"""Hybrid-injection attribution runner.

The new dataset format (pair_diagnosis_build_ok_*) labels every case as
``fault_type="hybrid"`` with ``engine_config`` = list of N candidate fault
engines and ``ground_truth`` = list of N service sets. The task is to
identify which candidate(s) actually fired.

Strategy v1: fan out per-engine, synthesize a legacy single-fault
injection.json (string fault_type + display_config.injection_point + dict
ground_truth), call ``run_single_case`` once per engine, score:

- ``status``: success / no_paths / no_alarms / crash
- ``paths``: number of corridor paths recovered

Then per case emit a ``fire`` set (engines with status=success) and a
``silent`` set, plus rankings by path count. ``case_dir`` here is the
``converted/`` subdir, not the batch root.
"""

from __future__ import annotations

import argparse
import collections
import json
import logging
import re
import statistics
import sys
import time
from concurrent.futures import ProcessPoolExecutor, as_completed
from pathlib import Path
from typing import Any

logging.getLogger("rcabench_platform.v3.internal.reasoning").setLevel(logging.WARNING)

from rcabench_platform.v3.internal.reasoning.cli import run_single_case  # noqa: E402


def _synthesize_injection(
    case_meta: dict[str, Any],
    engine: dict[str, Any],
    gt_services: list[str],
) -> dict[str, Any]:
    """Build a legacy-shape injection dict from one engine_config entry."""
    chaos_type = engine.get("chaos_type") or ""

    injection_point = {
        "app_name": engine.get("app"),
        "source_service": engine.get("app"),
        "target_service": engine.get("target_service") or engine.get("app"),
        "container_name": engine.get("container"),
        "class_name": engine.get("class"),
        "method_name": engine.get("method"),
        "domain": engine.get("domain"),
    }
    display_config = {
        "injection_point": injection_point,
        "namespace": engine.get("namespace"),
        "direction": engine.get("direction"),
        "latency_ms": engine.get("latency"),
        "latency_duration": engine.get("latency_duration"),
        "memory_size": engine.get("memory_size"),
    }

    return {
        "fault_type": chaos_type,
        "display_config": json.dumps(display_config),
        "ground_truth": {"service": list(gt_services)},
        "category": case_meta.get("category"),
        "start_time": case_meta.get("start_time"),
        "end_time": case_meta.get("end_time"),
        "pre_duration": case_meta.get("pre_duration"),
    }


def _gt_for_index(gt_raw: Any, idx: int) -> list[str]:
    """Pull engine-aligned GT services from the new list-shaped ground_truth."""
    if isinstance(gt_raw, list) and gt_raw:
        item = gt_raw[idx] if idx < len(gt_raw) else gt_raw[0]
        if isinstance(item, dict):
            svcs = item.get("service") or []
            return [s for s in svcs if isinstance(s, str)]
        return []
    if isinstance(gt_raw, dict):
        return [s for s in (gt_raw.get("service") or []) if isinstance(s, str)]
    return []


def _category_from_name(case_name: str) -> str:
    m = re.match(r"^([a-z]+)\d+", case_name)
    return m.group(1) if m else "unknown"


def _run_one(case_dir: Path) -> dict[str, Any]:
    inj_file = case_dir / "injection.json"
    if not inj_file.exists():
        return {"case": case_dir.parent.name, "status": "skipped", "reason": "no injection.json"}

    with open(inj_file, encoding="utf-8") as f:
        case_meta = json.load(f)

    engines = case_meta.get("engine_config") or []
    gt_raw = case_meta.get("ground_truth")
    if not engines:
        return {"case": case_dir.parent.name, "status": "skipped", "reason": "no engine_config"}

    case_started = time.time()
    per_engine: list[dict[str, Any]] = []
    for i, engine in enumerate(engines):
        if not isinstance(engine, dict):
            continue
        gt_services = _gt_for_index(gt_raw, i)
        if not gt_services:
            per_engine.append(
                {
                    "engine_index": i,
                    "chaos_type": engine.get("chaos_type"),
                    "app": engine.get("app"),
                    "status": "skipped",
                    "reason": "no ground_truth.service for engine",
                }
            )
            continue

        synthesized = _synthesize_injection(case_meta, engine, gt_services)
        eng_started = time.time()
        try:
            result = run_single_case(
                case_dir,
                max_hops=15,
                return_graph=False,
                injection_data=synthesized,
            )
            per_engine.append(
                {
                    "engine_index": i,
                    "chaos_type": engine.get("chaos_type"),
                    "app": engine.get("app"),
                    "ground_truth_services": gt_services,
                    "status": result.get("status", "unknown"),
                    "paths": result.get("paths", 0),
                    "elapsed": time.time() - eng_started,
                }
            )
        except Exception as e:  # noqa: BLE001
            per_engine.append(
                {
                    "engine_index": i,
                    "chaos_type": engine.get("chaos_type"),
                    "app": engine.get("app"),
                    "ground_truth_services": gt_services,
                    "status": "crash",
                    "error": f"{type(e).__name__}: {e}",
                    "elapsed": time.time() - eng_started,
                }
            )

    fire = [e for e in per_engine if e.get("status") == "success" and (e.get("paths") or 0) > 0]
    silent = [e for e in per_engine if e.get("status") in ("no_paths", "no_alarms")]
    crashed = [e for e in per_engine if e.get("status") == "crash"]
    case_name = case_dir.parent.name

    return {
        "case": case_name,
        "category": case_meta.get("category") or _category_from_name(case_name),
        "n_engines": len(engines),
        "elapsed": time.time() - case_started,
        "per_engine": per_engine,
        "fire_indices": [e["engine_index"] for e in fire],
        "silent_indices": [e["engine_index"] for e in silent],
        "crash_indices": [e["engine_index"] for e in crashed],
        "case_status": (
            "all_fire"
            if fire and not silent and not crashed
            else "partial_fire"
            if fire
            else "all_silent"
            if silent and not crashed
            else "crash"
            if crashed
            else "unknown"
        ),
    }


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--data-base", default="/home/ddq/AoyangSpace/dataset/pair_diagnosis_build_ok_2026-04-27/data")
    p.add_argument("--max-cases", type=int, default=0, help="0 = all")
    p.add_argument("--workers", type=int, default=8)
    p.add_argument("--filter-category", default="", help="comma-separated, e.g. 'ts' or 'ts,otel-demo'")
    p.add_argument("--out", default="output/e2e_validate_hybrid.json")
    args = p.parse_args()

    base = Path(args.data_base)
    if not base.is_dir():
        print(f"data base not found: {base}", file=sys.stderr)
        return 1

    case_dirs: list[Path] = []
    for batch in sorted(base.iterdir()):
        conv = batch / "converted"
        if conv.is_dir() and (conv / "injection.json").exists():
            case_dirs.append(conv)

    # Filter by category if requested.
    cats = {c.strip() for c in args.filter_category.split(",") if c.strip()}
    if cats:
        kept: list[Path] = []
        for cd in case_dirs:
            try:
                meta = json.load(open(cd / "injection.json"))
                cat = meta.get("category") or _category_from_name(cd.parent.name)
            except Exception:
                cat = _category_from_name(cd.parent.name)
            if cat in cats:
                kept.append(cd)
        case_dirs = kept

    if args.max_cases > 0:
        case_dirs = case_dirs[: args.max_cases]
    print(f"dispatching {len(case_dirs)} cases x {args.workers} workers (filter={cats or 'none'})")

    results: list[dict[str, Any]] = []
    started = time.time()
    with ProcessPoolExecutor(max_workers=args.workers) as ex:
        futs = {ex.submit(_run_one, c): c.parent.name for c in case_dirs}
        for done_idx, fut in enumerate(as_completed(futs), 1):
            name = futs[fut]
            try:
                r = fut.result()
            except Exception as e:  # noqa: BLE001
                r = {"case": name, "case_status": "executor_error", "error": str(e)}
            results.append(r)
            stat = r.get("case_status", "?")
            n = r.get("n_engines", 0)
            fire_n = len(r.get("fire_indices", []))
            elapsed = r.get("elapsed", 0.0)
            cat = r.get("category", "?")
            print(
                f"[{done_idx:3d}/{len(case_dirs)}] {name:55s} cat={cat:10s} engines={n} "
                f"fire={fire_n}/{n} status={stat:13s} t={elapsed:5.1f}s"
            )

    total_elapsed = time.time() - started

    # Aggregate.
    by_case_status = collections.Counter()
    by_cat_status: dict[str, collections.Counter] = collections.defaultdict(collections.Counter)
    by_chaos_status: dict[str, collections.Counter] = collections.defaultdict(collections.Counter)
    timings = []
    crash_examples: list[str] = []
    for r in results:
        cs = r.get("case_status", "?")
        cat = r.get("category", "?")
        by_case_status[cs] += 1
        by_cat_status[cat][cs] += 1
        if "elapsed" in r:
            timings.append(r["elapsed"])
        for e in r.get("per_engine") or []:
            ct = e.get("chaos_type") or "?"
            by_chaos_status[ct][e.get("status", "?")] += 1
        if cs == "crash" and len(crash_examples) < 10:
            crash_examples.append(r.get("case", "?"))

    summary = {
        "total_cases": len(results),
        "wall_clock_seconds": total_elapsed,
        "by_case_status": dict(by_case_status),
        "by_category_status": {k: dict(v) for k, v in by_cat_status.items()},
        "by_chaos_engine_status": {k: dict(v) for k, v in by_chaos_status.items()},
        "case_timing": {
            "min": min(timings) if timings else 0,
            "max": max(timings) if timings else 0,
            "mean": statistics.mean(timings) if timings else 0,
            "median": statistics.median(timings) if timings else 0,
            "p95": statistics.quantiles(timings, n=20)[18] if len(timings) >= 20 else None,
        },
        "crash_examples": crash_examples,
    }
    print("\n=== summary ===")
    print(json.dumps(summary, indent=2))

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump({"summary": summary, "results": results}, f, indent=2)
    print(f"results written to {out_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
