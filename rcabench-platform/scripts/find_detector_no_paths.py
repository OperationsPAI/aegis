#!/usr/bin/env -S uv run -s
"""Find cases where the detector flagged anomalies (conclusion.parquet has
non-empty Issues) but the reasoning pipeline failed to recover paths.

Reads runner output JSONs and joins against conclusion.parquet for each case.
"""

from __future__ import annotations

import argparse
import collections
import json
import sys
from pathlib import Path

import polars as pl


def _conclusion_anomaly_count(conv_dir: Path) -> tuple[int, int]:
    """Return (total_rows, rows_with_nonempty_issues)."""
    cp = conv_dir / "conclusion.parquet"
    if not cp.exists():
        return 0, 0
    df = pl.read_parquet(cp)
    if df.height == 0 or "Issues" not in df.columns:
        return df.height, 0
    flagged = df.filter((pl.col("Issues").is_not_null()) & (pl.col("Issues") != "{}") & (pl.col("Issues") != ""))
    return df.height, flagged.height


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--data-base", default="/home/ddq/AoyangSpace/dataset/pair_diagnosis_build_ok_2026-04-27/data")
    p.add_argument("--results", nargs="+", required=True, help="runner JSON outputs to combine")
    p.add_argument("--out", default="output/detector_vs_pipeline.json")
    args = p.parse_args()

    base = Path(args.data_base)

    cases: dict[str, dict] = {}
    for rp in args.results:
        d = json.load(open(rp))
        for r in d.get("results") or []:
            cases[r["case"]] = r

    rows = []
    for case_name, r in cases.items():
        conv = base / case_name / "converted"
        total, flagged = _conclusion_anomaly_count(conv)
        rows.append(
            {
                "case": case_name,
                "category": r.get("category"),
                "case_status": r.get("case_status"),
                "n_engines": r.get("n_engines", 0),
                "fire_n": len(r.get("fire_indices") or []),
                "silent_n": len(r.get("silent_indices") or []),
                "conclusion_total": total,
                "conclusion_flagged": flagged,
                "per_engine_status": [e.get("status") for e in (r.get("per_engine") or [])],
                "per_engine_chaos": [e.get("chaos_type") for e in (r.get("per_engine") or [])],
            }
        )

    # Filter: detector flagged anomalies but pipeline failed
    detector_hit = [x for x in rows if x["conclusion_flagged"] > 0]
    pipeline_miss = [
        x
        for x in detector_hit
        if x["fire_n"] == 0  # no engine recovered paths
        and x["case_status"] in ("all_silent", "partial_fire", "unknown", "crash")
    ]
    # Also breakdown by per-engine: which engines were no_paths despite detector hit
    engine_breakdown: dict[str, collections.Counter] = collections.defaultdict(collections.Counter)
    for x in detector_hit:
        for ct, st in zip(x["per_engine_chaos"], x["per_engine_status"]):
            engine_breakdown[ct or "?"][st or "?"] += 1

    summary = {
        "total_cases": len(rows),
        "detector_flagged_cases": len(detector_hit),
        "detector_hit_pipeline_miss": len(pipeline_miss),
        "by_category": {},
        "by_chaos_when_detector_hit": {k: dict(v) for k, v in engine_breakdown.items()},
    }
    by_cat = collections.defaultdict(lambda: collections.Counter())
    for x in detector_hit:
        cat = x["category"] or "?"
        by_cat[cat]["detector_hit"] += 1
        if x["fire_n"] == 0:
            by_cat[cat]["pipeline_miss"] += 1
    summary["by_category"] = {k: dict(v) for k, v in by_cat.items()}

    print(json.dumps(summary, indent=2))
    print(f"\n=== detector hit but pipeline missed ({len(pipeline_miss)}) ===")
    for x in sorted(pipeline_miss, key=lambda y: (y["category"] or "", -y["conclusion_flagged"])):
        eng = ",".join(f"{c}:{s}" for c, s in zip(x["per_engine_chaos"], x["per_engine_status"]))
        print(
            f"  {x['category']:10s} {x['case']:55s} flagged={x['conclusion_flagged']:3d}/{x['conclusion_total']:3d} "
            f"engines=[{eng}]"
        )

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    json.dump({"summary": summary, "pipeline_miss": pipeline_miss, "all_detector_hit": detector_hit}, open(out, "w"), indent=2)
    print(f"\nwritten to {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
