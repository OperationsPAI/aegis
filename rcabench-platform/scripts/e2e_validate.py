#!/usr/bin/env -S uv run -s
"""E2E validation: run reasoning on /home/ddq/AoyangSpace/dataset/rca cases.

This pipeline is *not* a blind detector — it's given the injection target
and answers "what paths reach the alarms". The metrics that matter for
validating the 7-PR methodology refactor are:

- did each case complete without crashing
- did it produce paths (status=success) for cases that should have paths
- per-case timing vs. baseline expectations
- presence of methodology signals: corridor pruning logged, inferred
  edges accounted for, etc.

The injection's service is the path start by construction, so a "did we
recover the ground truth service?" metric is trivial. We instead surface:
status distribution, path count, runtime distribution, and a small set
of cases for manual inspection.
"""

from __future__ import annotations

import argparse
import json
import logging
import statistics
import sys
import time
from concurrent.futures import ProcessPoolExecutor, as_completed
from pathlib import Path
from typing import Any

# Silence the chatty reasoning logger.
logging.getLogger("rcabench_platform.v3.internal.reasoning").setLevel(logging.WARNING)

from rcabench_platform.v3.internal.reasoning.cli import run_single_case  # noqa: E402


def _run_one(case_dir: Path) -> dict[str, Any]:
    inj_file = case_dir / "injection.json"
    if not inj_file.exists():
        return {"case": case_dir.name, "status": "skipped", "reason": "no injection.json"}

    with open(inj_file, encoding="utf-8") as f:
        injection_data = json.load(f)

    gt = injection_data.get("ground_truth") or {}
    gt_services: list[str] = gt.get("service") or []
    if not gt_services:
        return {"case": case_dir.name, "status": "skipped", "reason": "no ground_truth.service"}

    start = time.time()
    try:
        result = run_single_case(
            case_dir,
            max_hops=15,
            return_graph=False,
            injection_data=injection_data,
        )
    except Exception as e:  # noqa: BLE001
        return {
            "case": case_dir.name,
            "status": "crash",
            "error": f"{type(e).__name__}: {e}",
            "elapsed": time.time() - start,
        }

    elapsed = time.time() - start
    fault_type = injection_data.get("fault_type")

    return {
        "case": case_dir.name,
        "status": result.get("status", "unknown"),
        "paths": result.get("paths", 0),
        "elapsed": elapsed,
        "fault_type": fault_type,
        "ground_truth_services": gt_services,
    }


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--data-base", default="/home/ddq/AoyangSpace/dataset/rca")
    p.add_argument("--max-cases", type=int, default=30)
    p.add_argument("--workers", type=int, default=8)
    p.add_argument("--out", default="output/e2e_validate.json")
    args = p.parse_args()

    base = Path(args.data_base)
    if not base.is_dir():
        print(f"data base not found: {base}", file=sys.stderr)
        return 1

    cases = sorted([p for p in base.iterdir() if p.is_dir() and (p / "injection.json").exists()])
    if args.max_cases > 0:
        cases = cases[: args.max_cases]
    print(f"dispatching {len(cases)} cases x {args.workers} workers")

    results: list[dict[str, Any]] = []
    started = time.time()
    with ProcessPoolExecutor(max_workers=args.workers) as ex:
        futs = {ex.submit(_run_one, c): c.name for c in cases}
        for done_idx, fut in enumerate(as_completed(futs), 1):
            name = futs[fut]
            try:
                r = fut.result()
            except Exception as e:  # noqa: BLE001
                r = {"case": name, "status": "executor_error", "error": str(e)}
            results.append(r)
            stat = r.get("status", "?")
            paths = r.get("paths", 0)
            elapsed = r.get("elapsed", 0.0)
            print(f"[{done_idx:3d}/{len(cases)}] {name:60s} status={stat:10s} paths={paths:4d} t={elapsed:5.1f}s")

    total_elapsed = time.time() - started

    # Status distribution
    by_status: dict[str, int] = {}
    by_status_fault: dict[str, dict[str, int]] = {}
    timings: list[float] = []
    crash_cases: list[str] = []
    no_paths_cases: list[str] = []
    for r in results:
        s = r.get("status", "?")
        by_status[s] = by_status.get(s, 0) + 1
        ft = r.get("fault_type")
        if ft is not None:
            ft_key = str(ft)
            by_status_fault.setdefault(ft_key, {})
            by_status_fault[ft_key][s] = by_status_fault[ft_key].get(s, 0) + 1
        if "elapsed" in r:
            timings.append(r["elapsed"])
        if s == "crash":
            crash_cases.append(r.get("case", "?"))
        if s == "no_paths":
            no_paths_cases.append(r.get("case", "?"))

    summary = {
        "total": len(results),
        "by_status": by_status,
        "by_status_fault": by_status_fault,
        "wall_clock_seconds": total_elapsed,
        "timing": {
            "min": min(timings) if timings else 0,
            "max": max(timings) if timings else 0,
            "mean": statistics.mean(timings) if timings else 0,
            "median": statistics.median(timings) if timings else 0,
            "p95": statistics.quantiles(timings, n=20)[18] if len(timings) >= 20 else None,
        },
        "crash_cases": crash_cases,
        "no_paths_cases_count": len(no_paths_cases),
        "no_paths_cases_sample": no_paths_cases[:10],
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
