#!/usr/bin/env python
"""Driver for the FORGE ablation table (NeurIPS 2026 §exp_robustness).

Runs the canonical pipeline plus three ablations on a 500-case dataset
and emits a single JSON + LaTeX-snippet pair the paper repo can ingest.

Configurations (six total):
    baseline                — full pipeline
    skip_topology           — replace rule set R with admit-all
    skip_screen             — drop DriftGate from the gate stack
    slo_x{0.5,1.5,2.0}      — scale alarm-detection thresholds

For each non-SLO config we run two harnesses:
    fault_free   — split-baseline window, real injection node;
                   any "attributed" label is a joint FP.
    real         — full real injection; gives attributed_rate +
                   path_count.

SLO sweeps run only ``real`` (the SLO knob does not change the
fault-free FP semantics, per project plan).

Usage
-----
    uv run python bin/paper_artifacts/ablations_table.py \\
        --dataset /home/ddq/AoyangSpace/dataset/rca \\
        --workers 12 \\
        --out output/ablations/ablations_table.json

    # smoke
    uv run python bin/paper_artifacts/ablations_table.py \\
        --dataset /home/ddq/AoyangSpace/dataset/rca \\
        --workers 12 --max-cases 20 \\
        --out output/ablations/smoke.json
"""

from __future__ import annotations

import argparse
import json
import logging
import subprocess
import sys
import time
from collections import Counter
from concurrent.futures import ProcessPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from _ablation_lib import CONFIG_NAMES, run_one_case  # type: ignore[import-not-found]

logger = logging.getLogger("ablations_table")


def _git_sha(repo: Path) -> str:
    try:
        return subprocess.check_output(
            ["git", "-C", str(repo), "rev-parse", "HEAD"],
            stderr=subprocess.DEVNULL,
        ).decode().strip()
    except Exception:
        return "unknown"


def _summarize(rows: list[dict[str, Any]]) -> dict[str, Any]:
    """Aggregate per-case rows into a config × mode summary."""
    label_counts: Counter[str] = Counter()
    error_counts: Counter[str] = Counter()
    n_paths_attributed: list[int] = []
    n_total = len(rows)
    for r in rows:
        if r["error"] is not None:
            tag = r["error"].split(":")[0]
            error_counts[tag] += 1
            continue
        lbl = r["label"]
        if lbl is None:
            error_counts["null-label"] += 1
            continue
        label_counts[lbl] += 1
        if lbl == "attributed":
            n_paths_attributed.append(int(r["n_paths"]))
    n_classified = sum(label_counts.values())
    attributed = label_counts.get("attributed", 0)
    return {
        "n_cases": n_total,
        "n_classified": n_classified,
        "n_errors": sum(error_counts.values()),
        "labels": dict(label_counts),
        "errors": dict(error_counts),
        "attributed": attributed,
        "attributed_rate": attributed / n_classified if n_classified else 0.0,
        "joint_fp_rate": attributed / n_classified if n_classified else 0.0,
        "path_count_mean": (sum(n_paths_attributed) / len(n_paths_attributed)) if n_paths_attributed else 0.0,
        "path_count_n": len(n_paths_attributed),
    }


def _effective_max_hops(config_name: str, default_max_hops: int) -> int:
    """Cap skip_topology at a smaller hop count.

    With an admit-all rule set the corridor explorer cannot prune;
    candidate-path enumeration becomes exponential in ``max_hops``.
    Empirically ``max_hops=15`` does not terminate within minutes per
    case, while ``max_hops=5`` finishes in seconds and still produces
    an order-of-magnitude path-count signal. The cap is explicit
    rather than implicit so the comparison is honest: we report what
    the un-pruned pipeline can and cannot do under realistic budgets.
    """
    if config_name == "skip_topology":
        return min(default_max_hops, 5)
    return default_max_hops


def _run_config_mode(
    cases: list[Path],
    config_name: str,
    mode: str,
    workers: int,
    max_hops: int,
) -> dict[str, Any]:
    eff_hops = _effective_max_hops(config_name, max_hops)
    t0 = time.time()
    rows: list[dict[str, Any]] = []
    with ProcessPoolExecutor(max_workers=workers) as ex:
        futs = [
            ex.submit(run_one_case, str(c), config_name, mode, eff_hops)
            for c in cases
        ]
        done = 0
        for fut in as_completed(futs):
            rows.append(fut.result())
            done += 1
            if done % 50 == 0:
                summary = _summarize(rows)
                attr = summary["attributed"]
                tot = summary["n_classified"]
                logger.info(
                    f"  [{config_name}/{mode} {done}/{len(cases)}] attr={attr}/{tot} "
                    f"({attr / max(1, tot):.2%}) errs={summary['n_errors']}"
                )
    summary = _summarize(rows)
    summary["elapsed_seconds"] = round(time.time() - t0, 1)
    summary["max_hops"] = eff_hops
    summary["sample_rows"] = rows[:5]
    summary["all_rows"] = rows
    return summary


def _emit_latex_snippet(out: Path, configs: dict[str, dict[str, Any]]) -> None:
    """Emit \\newcommand definitions for paper repo's extract_experiment_data.py."""
    lines = [
        "% Auto-generated by bin/paper_artifacts/ablations_table.py",
        "% Do not edit; regenerate via the driver script.",
        "%",
    ]

    def pct(x: float) -> str:
        return f"{100 * x:.1f}\\%"

    def num(x: float) -> str:
        return f"{x:.2f}"

    def cmd(name: str, value: str) -> str:
        return f"\\newcommand{{\\{name}}}{{{value}}}"

    for cfg_name, cfg in configs.items():
        slug = cfg_name.replace(".", "").replace("_", "")
        if "real" in cfg:
            r = cfg["real"]
            lines.append(cmd(f"abl{slug}AttrRate", pct(r["attributed_rate"])))
            lines.append(cmd(f"abl{slug}PathCount", num(r["path_count_mean"])))
        if "fault_free" in cfg:
            f = cfg["fault_free"]
            lines.append(cmd(f"abl{slug}JointFP", pct(f["joint_fp_rate"])))
    out.write_text("\n".join(lines) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dataset", type=Path, required=True)
    parser.add_argument("--workers", type=int, default=12)
    parser.add_argument("--max-cases", type=int, default=0)
    parser.add_argument("--max-hops", type=int, default=15)
    parser.add_argument(
        "--configs",
        nargs="*",
        default=list(CONFIG_NAMES),
        help=f"Subset of {CONFIG_NAMES}. Default: all.",
    )
    parser.add_argument("--out", type=Path, default=Path("output/ablations/ablations_table.json"))
    args = parser.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(message)s")

    cases = sorted([c for c in args.dataset.iterdir() if c.is_dir() and not c.name.startswith(".")])
    if args.max_cases > 0:
        cases = cases[: args.max_cases]
    if not cases:
        print(f"no cases under {args.dataset}", file=sys.stderr)
        return 1

    print(f"ablations driver: {len(cases)} cases × {len(args.configs)} configs; workers={args.workers}")
    print(f"  configs: {args.configs}")

    configs_out: dict[str, dict[str, Any]] = {}
    for cfg in args.configs:
        if cfg not in CONFIG_NAMES:
            print(f"unknown config '{cfg}'; skipping", file=sys.stderr)
            continue
        configs_out[cfg] = {}
        modes = ("real",) if cfg.startswith("slo_x") else ("fault_free", "real")
        for mode in modes:
            print(f"\n=== {cfg} / {mode} ===")
            summary = _run_config_mode(cases, cfg, mode, args.workers, args.max_hops)
            configs_out[cfg][mode] = summary
            attr = summary["attributed"]
            tot = summary["n_classified"]
            rate = summary["attributed_rate"]
            mean_paths = summary["path_count_mean"]
            tag = "joint_fp" if mode == "fault_free" else "attr"
            print(
                f"  -> {tag}={attr}/{tot} ({rate:.2%}); "
                f"path_count_mean={mean_paths:.2f}; errors={summary['n_errors']}; "
                f"elapsed={summary['elapsed_seconds']}s"
            )

    repo = Path(__file__).resolve().parents[2]
    bundle = {
        "version": "1",
        "dataset": str(args.dataset),
        "n_cases": len(cases),
        "configs": configs_out,
        "git_sha": _git_sha(repo),
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "args": {
            "workers": args.workers,
            "max_hops": args.max_hops,
            "configs": args.configs,
            "max_cases": args.max_cases,
        },
    }

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(bundle, indent=2, ensure_ascii=False))
    print(f"\nwrote {args.out}")

    snippet = args.out.with_name(args.out.stem + "_cells.tex")
    _emit_latex_snippet(snippet, configs_out)
    print(f"wrote {snippet}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
