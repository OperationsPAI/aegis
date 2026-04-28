#!/usr/bin/env python
"""Inject-time tolerance (τ) sensitivity sweep across the 500-case dataset.

For each τ in a configurable list, runs the full reasoning pipeline on every
case and counts the resulting 5-class label distribution. Writes a JSON
report and a LaTeX table the paper appendix can ``\\input{}``.

The point is to demonstrate that the chosen ``INJECT_TIME_TOLERANCE_SECONDS``
default (60 s) lands on a recall plateau — increasing τ further admits
no additional ``attributed`` labels, decreasing τ flips a measurable
fraction of cases into ``unexplained_impact``.

Usage:
    uv run python bin/paper_artifacts/inject_time_sensitivity.py \\
        --dataset /home/ddq/AoyangSpace/dataset/rca \\
        --tau 30 60 90 120 \\
        --workers 12 \\
        --out output/inject_time_sensitivity

Outputs:
    <out>/sensitivity.json    -- machine-readable per-τ counts
    <out>/sensitivity.tex     -- LaTeX table (rows = labels, cols = τ values)

Notes:
    * Cases with missing injection metadata or load failures are reported
      under ``error`` rather than counted in any label bucket.
    * A τ-sweep does not use the regular ``cli batch`` command because that
      reads ``INJECT_TIME_TOLERANCE_SECONDS`` from module load. We therefore
      drive ``run_single_case`` directly with the τ override parameter.
"""

from __future__ import annotations

import argparse
import functools
import json
import logging
import sys
from collections import Counter
from concurrent.futures import ProcessPoolExecutor, as_completed
from pathlib import Path
from typing import Any

LABEL_ORDER = ["ineffective", "absorbed", "attributed", "unexplained_impact", "contaminated"]
LABEL_DISPLAY = {
    "ineffective": "Ineffective",
    "absorbed": "Absorbed",
    "attributed": "Attributed",
    "unexplained_impact": "Unexplained Impact",
    "contaminated": "Contaminated",
}


def _load_injection_data(case_dir: Path) -> dict[str, Any] | None:
    inj = case_dir / "injection.json"
    if not inj.exists():
        return None
    try:
        return json.loads(inj.read_text())
    except (OSError, json.JSONDecodeError):
        return None


def _run_case_with_tau(case_dir_str: str, tau: int, max_hops: int) -> tuple[str, str | None, str | None]:
    """Worker entrypoint: returns (case_name, label, error)."""
    from rcabench_platform.v3.internal.reasoning.cli import run_single_case

    case_dir = Path(case_dir_str)
    case_name = case_dir.name
    injection_data = _load_injection_data(case_dir)
    if injection_data is None:
        return (case_name, None, "missing-injection.json")
    try:
        result = run_single_case(
            data_dir=case_dir,
            max_hops=max_hops,
            injection_data=injection_data,
            inject_time_tolerance_seconds=tau,
        )
    except Exception as exc:  # noqa: BLE001 — surface every exception type for the audit
        return (case_name, None, f"{type(exc).__name__}: {exc}")
    label = result.get("label")
    if not isinstance(label, str):
        prop = result.get("propagation_result")
        if isinstance(prop, dict):
            label = prop.get("label")
    return (case_name, label, None)


def _emit_latex(per_tau_counts: dict[int, Counter[str]], errors_per_tau: dict[int, int], out_path: Path) -> None:
    taus_sorted = sorted(per_tau_counts.keys())
    header = " & ".join([f"$\\tau = {t}$ s" for t in taus_sorted])
    lines: list[str] = []
    lines.append("\\begin{tabular}{l" + "r" * len(taus_sorted) + "}")
    lines.append("\\toprule")
    lines.append(f"Label & {header} \\\\")
    lines.append("\\midrule")
    for label in LABEL_ORDER:
        row_cells = [f"{per_tau_counts[t].get(label, 0)}" for t in taus_sorted]
        lines.append(f"{LABEL_DISPLAY[label]} & {' & '.join(row_cells)} \\\\")
    lines.append("\\midrule")
    err_cells = [f"{errors_per_tau.get(t, 0)}" for t in taus_sorted]
    lines.append(f"Errors / no metadata & {' & '.join(err_cells)} \\\\")
    lines.append("\\bottomrule")
    lines.append("\\end{tabular}")
    out_path.write_text("\n".join(lines) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dataset", type=Path, required=True, help="Root directory containing case folders")
    parser.add_argument(
        "--tau",
        type=int,
        nargs="+",
        default=[30, 60, 90, 120],
        help="τ values (seconds) to sweep",
    )
    parser.add_argument("--workers", type=int, default=8, help="Parallel worker count")
    parser.add_argument("--max-hops", type=int, default=15, help="Max propagation hops")
    parser.add_argument("--out", type=Path, default=Path("output/inject_time_sensitivity"), help="Output directory")
    parser.add_argument("--max-cases", type=int, default=0, help="Limit cases (0 = all, useful for smoke testing)")
    args = parser.parse_args()

    logging.basicConfig(level=logging.WARNING, format="%(asctime)s [%(levelname)s] %(message)s")

    dataset = args.dataset.resolve()
    if not dataset.is_dir():
        print(f"dataset directory not found: {dataset}", file=sys.stderr)
        return 1

    cases = sorted([c for c in dataset.iterdir() if c.is_dir() and not c.name.startswith(".")])
    if args.max_cases > 0:
        cases = cases[: args.max_cases]
    if not cases:
        print(f"no cases found under {dataset}", file=sys.stderr)
        return 1

    args.out.mkdir(parents=True, exist_ok=True)

    print(f"sweeping τ ∈ {args.tau} across {len(cases)} cases (workers={args.workers})")
    per_tau_counts: dict[int, Counter[str]] = {t: Counter() for t in args.tau}
    errors_per_tau: dict[int, int] = {t: 0 for t in args.tau}
    error_samples_per_tau: dict[int, list[tuple[str, str]]] = {t: [] for t in args.tau}

    for tau in args.tau:
        print(f"\n=== τ = {tau} s ===")
        runner = functools.partial(_run_case_with_tau, tau=tau, max_hops=args.max_hops)
        with ProcessPoolExecutor(max_workers=args.workers) as ex:
            futures = {ex.submit(runner, str(c)): c for c in cases}
            done = 0
            for fut in as_completed(futures):
                case_name, label, err = fut.result()
                done += 1
                if err is not None:
                    errors_per_tau[tau] += 1
                    if len(error_samples_per_tau[tau]) < 5:
                        error_samples_per_tau[tau].append((case_name, err))
                    if done % 50 == 0:
                        print(f"  [{done}/{len(cases)}] errors so far: {errors_per_tau[tau]}")
                    continue
                if label is None:
                    errors_per_tau[tau] += 1
                    if len(error_samples_per_tau[tau]) < 5:
                        error_samples_per_tau[tau].append((case_name, "missing-label-in-result"))
                    continue
                per_tau_counts[tau][label] += 1
                if done % 50 == 0:
                    print(f"  [{done}/{len(cases)}] {dict(per_tau_counts[tau])}")
        print(f"  τ = {tau} done: {dict(per_tau_counts[tau])}, errors={errors_per_tau[tau]}")

    summary = {
        "dataset": str(dataset),
        "n_cases": len(cases),
        "tau_seconds": list(args.tau),
        "label_counts": {str(t): dict(per_tau_counts[t]) for t in args.tau},
        "errors": {str(t): errors_per_tau[t] for t in args.tau},
        "error_samples": {str(t): [{"case": c, "error": e} for c, e in error_samples_per_tau[t]] for t in args.tau},
    }
    json_out = args.out / "sensitivity.json"
    json_out.write_text(json.dumps(summary, indent=2, ensure_ascii=False))
    print(f"\nwrote {json_out}")

    tex_out = args.out / "sensitivity.tex"
    _emit_latex(per_tau_counts, errors_per_tau, tex_out)
    print(f"wrote {tex_out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
