#!/usr/bin/env python
"""Audit script for ``PathBuilder._fallback_global_start`` triggers.

P1-F: count how often the silent-fallback path fires across the 500-case
dataset. If 0 → the fallback is dead code and can be deleted. If N > 0 →
emit a JSON report listing per-case occurrences so we can decide whether
each is a legitimate ``ineffective`` / ``unexplained_impact`` case that
should never reach this code path.

Usage:
    uv run python bin/paper_artifacts/fallback_global_start_audit.py \\
        --dataset /home/ddq/AoyangSpace/dataset/rca \\
        --workers 12 \\
        --out output/fallback_global_start_audit.json
"""

from __future__ import annotations

import argparse
import json
import logging
import sys
import traceback
from collections import Counter
from concurrent.futures import ProcessPoolExecutor, as_completed
from pathlib import Path
from typing import Any


def _load_injection_data(case_dir: Path) -> dict[str, Any] | None:
    inj = case_dir / "injection.json"
    if not inj.exists():
        return None
    try:
        return json.loads(inj.read_text())
    except (OSError, json.JSONDecodeError):
        return None


def _run_case(case_dir_str: str, max_hops: int) -> tuple[str, int, list[str], str | None]:
    """Worker entrypoint.

    Returns ``(case_name, fallback_hits, sample_call_sites, error)``.
    Each child process owns its own monkey-patched PathBuilder so the
    counter is local and we don't leak state across cases.
    """
    from rcabench_platform.v3.internal.reasoning.algorithms import path_builder as pb_mod
    from rcabench_platform.v3.internal.reasoning.cli import run_single_case

    case_dir = Path(case_dir_str)
    case_name = case_dir.name
    injection_data = _load_injection_data(case_dir)
    if injection_data is None:
        return (case_name, 0, [], "missing-injection.json")

    hits: list[str] = []
    original = pb_mod.PathBuilder._fallback_global_start

    def instrumented(self):  # type: ignore[no-untyped-def]
        # Capture the calling frame's function name (one of: _build_multi_hop,
        # _build_single_hop) for forensic purposes.
        import inspect

        frame = inspect.currentframe()
        caller = frame.f_back.f_code.co_name if frame and frame.f_back else "<unknown>"
        hits.append(caller)
        return original(self)

    pb_mod.PathBuilder._fallback_global_start = instrumented  # type: ignore[method-assign]
    try:
        run_single_case(
            data_dir=case_dir,
            max_hops=max_hops,
            injection_data=injection_data,
        )
    except Exception as exc:  # noqa: BLE001
        return (case_name, len(hits), hits[:5], f"{type(exc).__name__}: {exc}\n{traceback.format_exc()}")
    finally:
        pb_mod.PathBuilder._fallback_global_start = original  # type: ignore[method-assign]
    return (case_name, len(hits), hits[:5], None)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dataset", type=Path, required=True)
    parser.add_argument("--workers", type=int, default=8)
    parser.add_argument("--max-hops", type=int, default=15)
    parser.add_argument("--out", type=Path, default=Path("output/fallback_global_start_audit.json"))
    parser.add_argument("--max-cases", type=int, default=0)
    args = parser.parse_args()

    logging.basicConfig(level=logging.WARNING, format="%(message)s")

    cases = sorted([c for c in args.dataset.iterdir() if c.is_dir() and not c.name.startswith(".")])
    if args.max_cases > 0:
        cases = cases[: args.max_cases]
    if not cases:
        print(f"no cases under {args.dataset}", file=sys.stderr)
        return 1

    print(f"auditing _fallback_global_start across {len(cases)} cases (workers={args.workers})")

    per_case: dict[str, dict[str, Any]] = {}
    callsite_counter: Counter[str] = Counter()
    errors: dict[str, str] = {}
    cases_with_hits = 0

    with ProcessPoolExecutor(max_workers=args.workers) as ex:
        futures = {ex.submit(_run_case, str(c), args.max_hops): c for c in cases}
        done = 0
        for fut in as_completed(futures):
            case_name, hits, sample, err = fut.result()
            done += 1
            if err is not None:
                errors[case_name] = err
            if hits > 0:
                cases_with_hits += 1
                per_case[case_name] = {"hits": hits, "sample_call_sites": sample}
                for site in sample:
                    callsite_counter[site] += 1
            if done % 50 == 0:
                print(f"  [{done}/{len(cases)}] cases-with-hits={cases_with_hits} total-hits={sum(c['hits'] for c in per_case.values())}")

    summary = {
        "n_cases": len(cases),
        "cases_with_hits": cases_with_hits,
        "total_hits": sum(c["hits"] for c in per_case.values()),
        "callsite_distribution": dict(callsite_counter),
        "per_case": per_case,
        "n_errors": len(errors),
        "error_samples": {k: errors[k][:200] for k in list(errors)[:5]},
    }

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(summary, indent=2, ensure_ascii=False))
    print(f"\ncases_with_hits={cases_with_hits}/{len(cases)}, total_hits={summary['total_hits']}, errors={len(errors)}")
    print(f"call-site distribution: {dict(callsite_counter)}")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
