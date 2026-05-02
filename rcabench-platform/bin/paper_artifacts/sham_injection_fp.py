#!/usr/bin/env python
"""Sham-injection joint FP harness for the 4-gate pipeline.

For each case, runs the pipeline up to the propagator using the case's real
trace data, IR timelines, alarm nodes, and SLO surface — but replaces the
resolved injection node(s) with a random sham target of matching PlaceKind
that is *not* in the case's ground-truth set. Counts how often the
propagator returns at least one rule-admitted path on the sham injection;
that fraction is an empirical estimate of the joint false-positive rate of
the (topology, drift, temporal, inject_time) pipeline against real
baseline-noise on a quiet-but-realistic system.

Why this harness exists
-----------------------
Issue #265 §2 item 5 calls for a sham-injection FP test as the empirical
backbone of the §experiments soundness story. The methodology framing
("each gate vetoes a different failure mode") needs at least one number to
back it up; the joint FP rate on sham injections is the cleanest such
number.

Design notes
~~~~~~~~~~~~
* We *do* keep real abnormal trace data — that means the pipeline sees
  real cascade signals on the real cascade subgraph. The sham target is
  picked to be *outside* the GT set, so any ``len(paths) > 0`` is a false
  attribution: the gates passed a path rooted at an unrelated node.
* The sham target's ``PlaceKind`` matches the real injection's
  ``start_kind`` (e.g. real container injection → sham container target).
  This keeps the harness honest: we're measuring "given the same edge
  topology and rule set, can the pipeline distinguish a real cause from
  an unrelated peer of the same kind".
* All sham targets are drawn from the case's own graph. This conditions
  on the same graph + alarm set as the real run; the only thing that
  changes is the injection node.

Output
~~~~~~
JSON summary at ``--out``: per-case sham target, label, has_path; aggregate
fp_rate / total_trials / kind distribution. The fp_rate is the
methodology-section number.

Usage
~~~~~
The paper's ``sec:appendix_precision_bound`` figure is reproduced by:

    uv run python bin/paper_artifacts/sham_injection_fp.py \\
        --dataset /home/ddq/AoyangSpace/dataset/openrca2_lite_v1 \\
        --workers 12 --mode v2 \\
        --out bin/paper_artifacts/sham_fp/sham_fp_lite_v1.json

``--mode v2`` is the reportable mode for the paper: it splits each case's
``normal_traces`` in half (first half = baseline, second = synthetic
``abnormal``) so no real fault is present, and measures how often the
4-gate pipeline still emits ``attributed``. The frozen artifact at
``bin/paper_artifacts/sham_fp/sham_fp_lite_v1.json`` records the most
recent run (16/542 = 2.95%); regenerating with the same dataset + git sha
should reproduce that number bit-for-bit (sham target seed is deterministic
per-case via the ``--seed`` argument).
"""

from __future__ import annotations

import argparse
import json
import logging
import random
import sys
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


def _build_excluded_uniq_names(real_inj: dict[str, Any]) -> set[str]:
    """Return uniq_names that should NOT be picked as sham targets.

    Anything in the case's ground truth (across all kinds) is excluded so
    the sham target cannot accidentally coincide with the actual cause.
    """
    excluded: set[str] = set()
    gt_raw = real_inj.get("ground_truth") or {}
    # AegisLab hybrid schema stores ground_truth as a list of per-sub-injection
    # dicts; rca_label schema stores it as a single dict. Normalise to a list.
    gt_list = gt_raw if isinstance(gt_raw, list) else [gt_raw]
    for gt in gt_list:
        if not isinstance(gt, dict):
            continue
        for kind in ("container", "pod", "service", "span", "function"):
            for name in gt.get(kind) or []:
                if not name:
                    continue
                excluded.add(f"{kind}|{name}")
                if kind == "span":
                    excluded.add(f"span|{name}")
    return excluded


def _run_one_sham(
    case_dir_str: str,
    seed: int,
    max_hops: int,
    mode: str = "v1",
) -> tuple[str, str | None, str | None]:
    """Worker entrypoint: run a sham trial on the case dir.

    ``mode``:
      * ``v1`` — sham target on real cascade traces (wrong-target rate).
      * ``v2`` — split normal_traces into halves; first half = baseline,
        second half = fake-abnormal; sham_at = midpoint. Tests joint
        FP under genuine baseline noise (no real fault present).

    Returns ``(case_name, label, error)``. ``label`` is the 5-class label
    produced by the pipeline against the sham injection; ``error`` is set
    on resolver / IR pipeline failure.
    """
    from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
        INJECT_TIME_TOLERANCE_SECONDS,
        manifest_aware_gates,
    )
    from rcabench_platform.v3.internal.reasoning.algorithms.label_classifier import classify
    from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
    from rcabench_platform.v3.internal.reasoning.algorithms.starting_point_resolver import StartingPointResolver
    from rcabench_platform.v3.internal.reasoning.cli import (
        _compute_local_effect,
        _compute_slo_impact,
        _earliest_abnormal_seconds,
        _filter_alarms_by_surface,
        _latest_abnormal_seconds,
        _resolve_alarm_nodes,
    )
    from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
    from rcabench_platform.v3.internal.reasoning.manifests.extractors.feature_extractor import (
        extract_feature_samples,
    )
    from rcabench_platform.v3.internal.reasoning.manifests.registry import (
        get_default_registry,
    )
    from rcabench_platform.v3.internal.reasoning.config.slo_surface import SLOSurface
    from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
    from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import enrich_with_inferred_edges
    from rcabench_platform.v3.internal.reasoning.ir.adapters.log_dependency import dispatch_log_adapters
    from rcabench_platform.v3.internal.reasoning.ir.adapters.trace_db_binding import (
        dispatch_trace_db_binding_adapters,
    )
    from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
    from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
    from rcabench_platform.v3.internal.reasoning.models.injection import InjectionNodeResolver
    from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules

    case_dir = Path(case_dir_str)
    case_name = case_dir.name
    real_inj = _load_injection_data(case_dir)
    if real_inj is None:
        return (case_name, None, "missing-injection.json")

    rng = random.Random(seed)

    try:
        loader = ParquetDataLoader(case_dir, 2)
        graph = loader.build_graph_from_parquet()
        alarm_node_names = loader.identify_alarm_nodes_v2()
        surface = SLOSurface.default()
        alarm_node_names = _filter_alarms_by_surface(list(alarm_node_names), graph, surface)
        alarm_nodes = _resolve_alarm_nodes(graph, list(alarm_node_names))
        slo_impact = _compute_slo_impact(alarm_nodes, graph, surface)

        # Resolve the real injection only to learn its start_kind — sham
        # target must be of the same kind so we are comparing peers.
        resolver = InjectionNodeResolver(graph)
        try:
            resolved_real = resolver.resolve(real_inj)
        except Exception as resolve_err:
            return (case_name, None, f"resolve-failed: {resolve_err}")
        sham_kind = resolved_real.start_kind

        excluded = _build_excluded_uniq_names(real_inj)

        # Candidate sham targets: nodes of the same kind not in GT, with at
        # least one outbound or inbound edge (so the propagator has
        # something to walk).
        candidates: list[int] = []
        for node_id in graph._graph.nodes:
            node = graph.get_node_by_id(node_id)
            if node is None or node.kind.value != sham_kind:
                continue
            if node.uniq_name in excluded:
                continue
            if graph._graph.degree(node_id) == 0:
                continue
            candidates.append(node_id)
        if not candidates:
            return (case_name, "no-sham-candidate", None)

        sham_node_id = rng.choice(candidates)
        sham_node = graph.get_node_by_id(sham_node_id)  # noqa: F841
        physical_node_ids = [sham_node_id]

        # Run trace-DB binding + IR pipeline against real traces. The IR
        # adapters are graph- and trace-driven; they don't depend on the
        # injection node, so reusing them gives an honest "what does the
        # observation pipeline produce" baseline.
        if mode == "v2":
            # Split normal_traces in half; first half = baseline, second =
            # fake-abnormal. Sham_at is the midpoint timestamp. No real
            # fault is present in either half, so any path the pipeline
            # admits is a genuine joint FP.
            import polars as pl_mod

            normal_full = loader.load_traces("normal")
            if normal_full.height < 2 or "time" not in normal_full.columns:
                return (case_name, "no-baseline-data", None)
            normal_sorted = normal_full.sort("time")
            mid_idx = normal_sorted.height // 2
            baseline_traces = normal_sorted.slice(0, mid_idx)
            abnormal_traces = normal_sorted.slice(mid_idx)
            if baseline_traces.height == 0 or abnormal_traces.height == 0:
                return (case_name, "no-baseline-data", None)
            del pl_mod  # unused after slicing
        else:
            baseline_traces = loader.load_traces("normal")
            abnormal_traces = loader.load_traces("abnormal")
        injection_at = _earliest_abnormal_seconds(abnormal_traces)
        abnormal_window_end = _latest_abnormal_seconds(abnormal_traces)
        dispatch_trace_db_binding_adapters(graph, abnormal_traces, baseline_traces)

        ctx = AdapterContext(datapack_dir=case_dir, case_name=case_name)
        timelines = run_reasoning_ir(
            graph=graph,
            ctx=ctx,
            resolved=resolved_real,
            injection_at=injection_at,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_end=abnormal_window_end,
        )
        enrich_with_inferred_edges(graph, timelines, physical_node_ids)
        try:
            abnormal_logs = loader.load_logs("abnormal")
            normal_logs = loader.load_logs("normal")
            dispatch_log_adapters(graph, timelines, abnormal_logs, normal_logs)
        except FileNotFoundError:
            pass

        # StartingPointResolver expects a ResolvedInjection; we want it to
        # treat the sham node like a span/service injection point of the
        # same kind. Reusing resolved_real's category is honest because we
        # are *holding the fault category constant* and only swapping the
        # target.
        rules = get_builtin_rules()
        starting_resolver = StartingPointResolver(graph)
        injection_node_ids = starting_resolver.resolve(
            physical_node_ids=physical_node_ids,
            resolved_injection=resolved_real,
            rules=rules,
        )

        local_effect = _compute_local_effect(physical_node_ids, timelines, graph)
        if not slo_impact.detected:
            label, _reason = classify(local_effect, slo_impact, has_path=False)
            return (case_name, label, None)

        delta_t = max(0, abnormal_window_end - injection_at)
        injection_window = (injection_at, injection_at + delta_t + INJECT_TIME_TOLERANCE_SECONDS)

        # Wire the manifest-driven gates and PathBuilder. The sham harness
        # tests "would the manifest pipeline fire on a non-GT target under
        # baseline noise?" — so we honestly invoke the same code path as
        # cli.run_single_case (cli.py:728-767), with the sham node as
        # v_root but holding the real fault_type_name (we are testing the
        # manifest's specificity, not its category routing).
        v_root_id: int | None = (
            injection_node_ids[0] if injection_node_ids else physical_node_ids[0]
        )
        feature_samples = extract_feature_samples(
            graph=graph,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_start=injection_at,
            abnormal_window_end=abnormal_window_end,
            timelines=timelines,
        )
        registry = get_default_registry()
        manifest = registry.get(resolved_real.fault_type_name)
        reasoning_ctx = ReasoningContext(
            fault_type_name=resolved_real.fault_type_name,
            manifest=manifest,
            v_root_node_id=v_root_id,
            t0=injection_at,
            feature_samples=feature_samples,
            registry=registry,
            graph=graph,
        )

        propagator = FaultPropagator(
            graph=graph,
            rules=rules,
            timelines=timelines,
            max_hops=max_hops,
            injection_window=injection_window,
            gates=manifest_aware_gates(reasoning_ctx),
            reasoning_ctx=reasoning_ctx,
        )
        result = propagator.propagate_from_injection(
            injection_node_ids=injection_node_ids,
            alarm_nodes=alarm_nodes,
        )
        has_path = bool(result.paths)
        label, _reason = classify(local_effect, slo_impact, has_path)
        return (case_name, label, None)
    except Exception as exc:  # noqa: BLE001 — surface every failure for the audit
        return (case_name, None, f"{type(exc).__name__}: {exc}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dataset", type=Path, required=True)
    parser.add_argument("--workers", type=int, default=8)
    parser.add_argument("--trials-per-case", type=int, default=1)
    parser.add_argument("--max-hops", type=int, default=15)
    parser.add_argument("--seed", type=int, default=20260428)
    parser.add_argument("--max-cases", type=int, default=0)
    parser.add_argument("--out", type=Path, default=Path("output/sham_fp/sham_fp.json"))
    parser.add_argument(
        "--mode",
        choices=("v1", "v2"),
        default="v1",
        help="v1 = sham target on real cascade (wrong-target rate); v2 = sham on baseline-only data (joint baseline FP).",  # noqa: E501
    )
    args = parser.parse_args()

    logging.basicConfig(level=logging.WARNING, format="%(message)s")

    cases = sorted([c for c in args.dataset.iterdir() if c.is_dir() and not c.name.startswith(".")])
    if args.max_cases > 0:
        cases = cases[: args.max_cases]
    if not cases:
        print(f"no cases under {args.dataset}", file=sys.stderr)
        return 1

    print(
        f"sham-FP harness: {len(cases)} cases × {args.trials_per_case} trial(s); "
        f"workers={args.workers}; seed={args.seed}"
    )

    label_counts: Counter[str] = Counter()
    error_counts: Counter[str] = Counter()
    no_candidate = 0
    fp_cases: list[dict[str, Any]] = []

    rng = random.Random(args.seed)
    seeds = [(case_dir, rng.randint(0, 2**31 - 1)) for case_dir in cases for _ in range(args.trials_per_case)]

    with ProcessPoolExecutor(max_workers=args.workers) as ex:
        futures = {ex.submit(_run_one_sham, str(c), s, args.max_hops, args.mode): (c, s) for c, s in seeds}
        done = 0
        for fut in as_completed(futures):
            case_name, label, err = fut.result()
            done += 1
            if err is not None:
                error_counts[err.split(":")[0]] += 1
                continue
            if label is None or label == "no-sham-candidate":
                no_candidate += 1
                continue
            label_counts[label] += 1
            if label == "attributed":
                fp_cases.append({"case": case_name})
            if done % 50 == 0:
                attributed = label_counts.get("attributed", 0)
                total = sum(label_counts.values())
                print(f"  [{done}/{len(seeds)}] FP={attributed}/{total} ({attributed / max(1, total):.2%})")

    total = sum(label_counts.values())
    fp = label_counts.get("attributed", 0)
    fp_rate = fp / total if total > 0 else 0.0

    summary = {
        "dataset": str(args.dataset),
        "n_cases": len(cases),
        "trials_per_case": args.trials_per_case,
        "n_trials_admitted": total,
        "n_no_candidate": no_candidate,
        "label_counts": dict(label_counts),
        "joint_fp_rate": fp_rate,
        "n_fp": fp,
        "fp_cases": fp_cases[:50],
        "error_counts": dict(error_counts),
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(summary, indent=2, ensure_ascii=False))
    print(f"\nresult: joint_fp_rate = {fp_rate:.2%} ({fp}/{total} sham trials produced 'attributed')")
    print(f"label distribution: {dict(label_counts)}")
    print(f"errors: {dict(error_counts)} (total {sum(error_counts.values())})")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
