"""Typer CLI facade for the reasoning engine."""

from __future__ import annotations

import json
import logging
import time
from datetime import datetime
from pathlib import Path
from typing import Any

import typer

from rcabench_platform.v3.internal.reasoning import runner as _runner_module
from rcabench_platform.v3.internal.reasoning._util import setup_logging
from rcabench_platform.v3.internal.reasoning.alarm_evidence import (  # noqa: F401
    _ALARM_EVIDENCE_INDEX_KEY,
    _alarm_evidence_for_node,
    _append_alarm_index,
    _apply_terminal_alarm_confidence_caps,
    _build_alarm_accounting,
    _classify_conclusion_alarm,
    _load_alarm_evidence_index,
    _new_alarm_index,
    _normalize_conclusion_span_name,
    _parse_alarm_identity,
    _split_default_and_weak_paths,
)
from rcabench_platform.v3.internal.reasoning.batch import (
    _classify_case,
    _collect_batch_tasks,
    _extract_services_from_injection,
    _log_batch_header,
    _log_batch_summary,
    _run_batch_tasks,
)
from rcabench_platform.v3.internal.reasoning.export.causal_graph import (  # noqa: F401
    _canonical_export_states,
    _causal_graph_with_export_metadata,
    _sync_injection_states_from_root_causes,
    propagation_result_to_causal_graph,
)
from rcabench_platform.v3.internal.reasoning.export.result_writer import (  # noqa: F401
    _result_with_paths,
    _save_case_result,
)
from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
from rcabench_platform.v3.internal.reasoning.manifests.registry import (
    ManifestRegistry,
    set_default_registry,
)
from rcabench_platform.v3.internal.reasoning.runner import (  # noqa: F401
    _compute_local_effect,
    _compute_slo_impact,
    _earliest_abnormal_seconds,
    _filter_alarms_by_surface,
    _latest_abnormal_seconds,
    _resolve_alarm_nodes,
)


def run_single_case(*args: Any, **kwargs: Any) -> dict[str, Any]:
    """Compatibility wrapper around runner.run_single_case for existing tests."""
    _runner_module.ParquetDataLoader = ParquetDataLoader
    _runner_module._save_case_result = _save_case_result
    return _runner_module.run_single_case(*args, **kwargs)


logger = logging.getLogger(__name__)
app = typer.Typer(name="reason", help="Fault propagation reasoning engine CLI")


# Default manifest directory: package-relative ``manifests/fault_types/``.
# Phase 1 ships zero manifests (apart from the example referenced in tests),
# so the default is "registry empty -> fall back to generic rules everywhere".
_DEFAULT_MANIFEST_DIR = Path(__file__).resolve().parent / "manifests" / "fault_types"


def _init_manifest_registry(manifest_dir: str | None) -> None:
    """Build and install the process-wide manifest registry."""
    target = Path(manifest_dir) if manifest_dir else _DEFAULT_MANIFEST_DIR
    if not target.exists():
        logger.info(
            "manifest dir %s does not exist; using empty registry (generic rules everywhere)",
            target,
        )
        set_default_registry(ManifestRegistry({}))
        return
    registry = ManifestRegistry.from_directory(target, strict=True)
    logger.info(
        "loaded %d manifest(s) from %s: %s",
        len(registry),
        target,
        ", ".join(registry.names()) or "(none)",
    )
    set_default_registry(registry)


@app.command("run")
def run(
    data_dir: str = typer.Option(..., help="Directory containing parquet data files"),
    max_hops: int = typer.Option(15, help="Maximum propagation hops"),
    manifest_dir: str | None = typer.Option(
        None,
        "--manifest-dir",
        help=(
            "Directory of fault manifest YAMLs. Defaults to the package-shipped "
            "``manifests/fault_types/``. An empty / missing directory keeps the "
            "generic-rule fallback for every fault type."
        ),
    ),
) -> int:
    """Run fault propagation analysis for a single case."""
    setup_logging(verbose=True)
    _init_manifest_registry(manifest_dir)
    total_start = time.time()

    data_path = Path(data_dir)
    output_path = Path("output")
    output_path.mkdir(parents=True, exist_ok=True)

    injection_file = data_path / "injection.json"
    if not injection_file.exists():
        logger.error(f"injection.json not found in {data_path}")
        return 1

    with open(injection_file, encoding="utf-8") as f:
        injection_data = json.load(f)

    services = _extract_services_from_injection(injection_data)
    if not services:
        logger.error("No services found in injection.json ground_truth")
        return 1

    result = run_single_case(
        data_path,
        max_hops,
        return_graph=False,
        injection_data=injection_data,
    )

    status = result["status"]
    exit_code = 0

    if status == "success":
        resolution_info = result.get("resolution_info", {})
        if resolution_info:
            logger.info(f"\n[OK] Success: {result['paths']} paths")
            logger.info(f"  Fault type: {resolution_info.get('fault_type', 'unknown')}")
            logger.info(f"  Resolved to: {resolution_info.get('start_kind', 'service')}")
            logger.info(f"  Method: {resolution_info.get('resolution_method', 'unknown')}")
        else:
            logger.info(f"\n[OK] Success: {result['paths']} paths")
    elif status == "error":
        logger.error(f"\n[ERR] Error: {result.get('error', 'Unknown error')}")
        exit_code = 1
    else:
        logger.warning(f"\n[WARN] Status: {status}")

    total_time = time.time() - total_start
    logger.info(f"\n{'=' * 60}")
    logger.info(f"Total execution time: {total_time:.2f}s")
    logger.info(f"{'=' * 60}\n")

    return exit_code


@app.command()
def batch(
    data_base_dir: str = typer.Option(
        "data/jfs/rcabench_dataset",
        help="Base directory containing case folders",
    ),
    max_cases: int = typer.Option(0, help="Maximum number of cases to run (0 = all)"),
    max_workers: int = typer.Option(12, help="Maximum number of parallel workers"),
    max_hops: int = typer.Option(15, help="Maximum propagation hops"),
    force: bool = typer.Option(False, "--force", help="Force reprocess all cases"),
    retry_no_paths: bool = typer.Option(False, "--retry-no-paths", help="Only retry no_paths cases"),
    manifest_dir: str | None = typer.Option(
        None,
        "--manifest-dir",
        help=(
            "Directory of fault manifest YAMLs. Defaults to the package-shipped "
            "``manifests/fault_types/``. An empty / missing directory keeps the "
            "generic-rule fallback for every fault type."
        ),
    ),
) -> int:
    _init_manifest_registry(manifest_dir)
    output_path = Path("output/batch_runs")
    output_path.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    log_path = output_path / f"batch_{timestamp}.log"

    setup_logging(verbose=True, log_file=log_path)
    logging.getLogger("rcabench_platform.v3.internal.reasoning").setLevel(logging.WARNING)

    total_start = time.time()
    base_path = Path(data_base_dir)

    _log_batch_header(base_path, output_path, max_workers, max_cases)

    tasks, skipped = _collect_batch_tasks(
        base_path,
        max_cases,
        skip_existing=not force,
        retry_no_paths_only=retry_no_paths,
    )
    logger.info(f"Collected {len(tasks)} tasks to run")
    if skipped > 0:
        logger.info(f"Skipped {skipped} already processed cases\n")

    stats = _run_batch_tasks(tasks, max_hops, output_path, max_workers, log_path)

    total_time = time.time() - total_start
    _log_batch_summary(stats, total_time)

    return 0


@app.command()
def filter_clean(
    data_base_dir: str = typer.Option(..., help="Base directory containing case folders"),
    min_services: int = typer.Option(3, help="Minimum distinct services in abnormal_traces"),
    max_gap_seconds: float = typer.Option(30.0, help="Max normal_end -> abnormal_start gap (seconds)"),
    output: str = typer.Option("-", help="Output path for clean case names ('-' = stdout)"),
    summary: bool = typer.Option(True, help="Print dirty-reason breakdown to stderr"),
) -> int:
    """Filter datapacks by data quality and print clean case names."""
    import sys
    from collections import Counter

    base_path = Path(data_base_dir)
    if not base_path.is_dir():
        typer.echo(f"error: {base_path} is not a directory", err=True)
        raise typer.Exit(2)

    clean: list[str] = []
    reasons: Counter[str] = Counter()

    for case_dir in sorted(base_path.iterdir()):
        if not case_dir.is_dir():
            continue
        verdict, _detail = _classify_case(case_dir, min_services, max_gap_seconds)
        if verdict == "clean":
            clean.append(case_dir.name)
        else:
            reasons[verdict] += 1

    out_stream = sys.stdout if output == "-" else open(output, "w")
    try:
        for name in clean:
            out_stream.write(name + "\n")
    finally:
        if out_stream is not sys.stdout:
            out_stream.close()

    if summary:
        total = len(clean) + sum(reasons.values())
        print(f"clean: {len(clean)}/{total}", file=sys.stderr)
        for reason, n in reasons.most_common():
            print(f"  {n:4} {reason}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    app()
