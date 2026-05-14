"""Batch and dataset filtering helpers for reasoning CLI commands."""

from __future__ import annotations

import json
import logging
from collections import Counter
from functools import partial
from pathlib import Path
from typing import Any

import polars as pl
from tqdm import tqdm

from rcabench_platform.v3.internal.reasoning.loaders.utils import fmap_processpool
from rcabench_platform.v3.internal.reasoning.runner import run_single_case
from rcabench_platform.v3.sdk.utils.serde import save_json

logger = logging.getLogger(__name__)

_FILTER_REQUIRED_PARQUETS = (
    "abnormal_traces.parquet",
    "normal_traces.parquet",
    "abnormal_metrics.parquet",
    "abnormal_metrics_histogram.parquet",
    "abnormal_metrics_sum.parquet",
)
_FILTER_EXTERNAL_SERVICES = {"mysql", "redis", "postgres", "mongodb", "kafka", "rabbitmq"}


def _log_batch_header(base_path: Path, output_path: Path, max_workers: int, max_cases: int) -> None:
    """Log batch run header."""
    logger.info("=" * 60)
    logger.info("Batch RCA Label Runner")
    logger.info("=" * 60)
    logger.info(f"Data directory: {base_path}")
    logger.info(f"Output directory: {output_path}")
    logger.info(f"Max workers: {max_workers}")
    logger.info(f"Max cases: {max_cases if max_cases > 0 else 'all'}")
    logger.info("=" * 60)


def _collect_batch_tasks(
    base_path: Path,
    max_cases: int,
    skip_existing: bool = True,
    retry_no_paths_only: bool = False,
) -> tuple[list[tuple[Path, list[str], dict[str, Any]]], int]:
    """Collect all tasks to run from case folders."""
    tasks: list[tuple[Path, list[str], dict[str, Any]]] = []
    skipped = 0

    for case_folder in sorted(base_path.iterdir()):
        if not case_folder.is_dir():
            continue
        if max_cases > 0 and len(tasks) >= max_cases:
            break

        # Two layouts in the wild:
        #   legacy:  case_folder/converted/{injection.json, parquet, ...}
        #   aegis:   case_folder/{injection.json, parquet, ...}
        # Pick whichever has injection.json so callers don't have to flag it.
        legacy_dir = case_folder / "converted"
        if (legacy_dir / "injection.json").exists():
            data_dir = legacy_dir
        elif (case_folder / "injection.json").exists():
            data_dir = case_folder
        else:
            logger.debug(f"[{case_folder.name}] Skipping: injection.json not found")
            continue

        # Validity marker: `.valid` (legacy) or any of the aegislab markers.
        # Empty marker files; their presence is the only signal.
        if not any((case_folder / m).exists() or (data_dir / m).exists() for m in (".valid", ".done", ".finished")):
            logger.debug(f"[{case_folder.name}] Skipping: no .valid/.done/.finished marker")
            continue

        case_output_folder = data_dir

        if retry_no_paths_only:
            no_paths_marker = case_output_folder / "no_paths.marker"
            if not no_paths_marker.exists():
                skipped += 1
                continue
            no_paths_marker.unlink()

        if skip_existing and not retry_no_paths_only:
            if (case_output_folder / "result.json").exists():
                skipped += 1
                continue
            if (case_output_folder / "no_alarms.marker").exists():
                skipped += 1
                continue

        try:
            with open(data_dir / "injection.json", encoding="utf-8") as f:
                injection_data = json.load(f)

            services = _extract_services_from_injection(injection_data)
            if not services:
                logger.debug(f"[{case_folder.name}] Skipping: No services in ground_truth")
                continue

            # Keep legacy injection_nodes as fallback
            injection_nodes = [f"service|{service}" for service in services if service != "mysql"]

            if injection_nodes:
                # Pass both injection_nodes (fallback) and injection_data (for smart resolution)
                tasks.append((data_dir, injection_nodes, injection_data))

        except Exception as e:
            logger.warning(f"[{case_folder.name}] Error reading injection.json: {e}")
            continue

    return tasks, skipped


def _extract_services_from_injection(injection_data: dict[str, Any]) -> list[str]:
    """Extract service names from injection.json ground_truth field."""
    ground_truth = injection_data.get("ground_truth", {})

    if isinstance(ground_truth, dict):
        services: list[str] = ground_truth.get("service", [])
        return services
    elif isinstance(ground_truth, list):
        services = []
        for gt_item in ground_truth:
            if isinstance(gt_item, dict):
                services.extend(gt_item.get("service", []))
        return services
    return []


def _run_batch_tasks(
    tasks: list[tuple[Path, list[str], dict[str, Any]]],
    max_hops: int,
    output_path: Path,
    max_workers: int,
    log_path: Path,
) -> dict[str, int]:
    """Run batch tasks in parallel and collect statistics."""
    stats = {
        "total": len(tasks),
        "success": 0,
        "failed": 0,
        "no_alarms": 0,
        "no_paths": 0,
    }
    no_paths_records: list[dict[str, Any]] = []

    task_callables = [
        partial(
            run_single_case,
            data_dir,
            max_hops,
            False,
            injection_data,  # Pass injection_data for smart resolution
        )
        for data_dir, injection_nodes, injection_data in tasks
    ]

    results = fmap_processpool(
        task_callables,
        parallel=max_workers,
        ignore_exceptions=True,
        cpu_limit_each=2,
        log_level=logging.DEBUG,
        log_file=str(log_path),
    )

    for i, result in enumerate(tqdm(results, desc="Processing", total=len(results))):
        if result is None:
            continue

        _, injection_nodes, _ = tasks[i]
        status = result["status"]

        if status == "success":
            stats["success"] += 1
        elif status == "no_alarms":
            stats["no_alarms"] += 1
        elif status == "no_paths":
            stats["no_paths"] += 1
            no_paths_records.append({"case": result["case"], "injection_nodes": injection_nodes})
        elif status == "injection_node_not_found":
            stats["failed"] += 1
        else:
            stats["failed"] += 1

    if no_paths_records:
        no_paths_file = output_path / "no_paths_records.json"
        save_json(no_paths_records, path=no_paths_file)
        logger.info(f"Exported {len(no_paths_records)} no-paths records to: {no_paths_file}")

    return stats


def _log_batch_summary(stats: dict[str, int], total_time: float) -> None:
    """Log batch run summary."""
    logger.info("\n" + "=" * 60)
    logger.info("Batch Run Complete")
    logger.info("=" * 60)
    logger.info(f"Total tasks: {stats['total']}")
    logger.info(f"Success: {stats['success']}")
    logger.info(f"Failed: {stats['failed']}")
    logger.info(f"No alarms: {stats['no_alarms']}")
    logger.info(f"No paths: {stats['no_paths']}")
    logger.info(f"Total time: {total_time:.2f}s")
    logger.info("=" * 60)


def _classify_case(
    case_dir: Path,
    min_services: int,
    max_gap_seconds: float,
) -> tuple[str, str]:
    """Return (verdict, detail). verdict ∈ {clean, missing_parquet, no_engine_config,
    loadgen_only, gt_no_spans, large_gap, read_error}."""
    inj_path = case_dir / "injection.json"
    if not inj_path.exists():
        return ("missing_parquet", "injection.json")
    missing = [f for f in _FILTER_REQUIRED_PARQUETS if not (case_dir / f).exists()]
    if missing:
        return ("missing_parquet", missing[0])

    import json as _json

    try:
        inj = _json.loads(inj_path.read_text())
    except Exception as exc:
        return ("read_error", f"injection.json: {type(exc).__name__}")

    eng = inj.get("engine_config") or inj.get("engine_config_summary") or []
    if not eng:
        return ("no_engine_config", "")

    try:
        ab = pl.read_parquet(case_dir / "abnormal_traces.parquet")
        nm = pl.read_parquet(case_dir / "normal_traces.parquet")
    except Exception as exc:
        return ("read_error", f"traces: {type(exc).__name__}")

    ab_svcs = set(ab["service_name"].unique().to_list()) if len(ab) else set()
    if len(ab_svcs) < min_services:
        return ("loadgen_only", f"{len(ab_svcs)} services")

    gt: set[str] = set()
    for entry in inj.get("ground_truth", []) or []:
        for s in entry.get("service", []) or []:
            gt.add(s)
    gt_internal = (gt - _FILTER_EXTERNAL_SERVICES) or gt
    missing_gt = [s for s in gt_internal if s not in ab_svcs]
    if missing_gt:
        return ("gt_no_spans", ",".join(missing_gt))

    if len(nm) and len(ab):
        from datetime import datetime as _dt

        ab_min = ab["time"].min()
        nm_max = nm["time"].max()
        if isinstance(ab_min, _dt) and isinstance(nm_max, _dt):
            gap = (ab_min - nm_max).total_seconds()
            if gap > max_gap_seconds:
                return ("large_gap", f"{gap:.0f}s")

    return ("clean", "")
