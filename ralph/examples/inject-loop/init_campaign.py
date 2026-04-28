#!/usr/bin/env python3
"""Initialize per-system inject-loop campaign state for Ralph/Codex."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[3]
SYSTEM_NAMESPACE_PREFIX = {
    "trainticket": "ts",
    "ts": "ts",
    "teastore": "tea",
    "tea": "tea",
    "otel-demo": "otel-demo",
    "sockshop": "sockshop",
    "hs": "hs",
    "mm": "mm",
    "sn": "sn",
}
SYSTEM_BACKEND_NAME = {
    "trainticket": "ts",
    "ts": "ts",
    "teastore": "teastore",
    "tea": "teastore",
    "otel-demo": "otel-demo",
    "sockshop": "sockshop",
    "hs": "hs",
    "mm": "media",
    "media": "media",
    "sn": "sn",
}
CLUSTER_HARD_EXCLUDED_CHAOS_TYPES = ["HTTP*"]
DEFAULT_REWARD_POLICY = {
    "success_terminals": ["algorithm.result.collection", "datapack.result.collection"],
    "negative_terminals": ["datapack.no_anomaly"],
    "neutral_prefixes": [
        "fault.injection.failed",
        "datapack.build.failed",
        "restart.pedestal.failed",
        "submit.error",
    ],
}


def round_number(path: Path) -> int | None:
    m = re.search(r"round(\d+)", path.stem)
    return int(m.group(1)) if m else None


def latest_round_doc(loop_dir: Path) -> tuple[Path, dict[str, Any]]:
    files = sorted(loop_dir.glob("candidates_round*.json"))
    if not files:
        raise SystemExit(f"no candidates_round*.json found under {loop_dir}")
    latest = max(files, key=lambda p: round_number(p) or -1)
    with latest.open() as f:
        data = json.load(f)
    return latest, data


def source_doc(loop_dir: Path, source_file: Path | None) -> tuple[Path, dict[str, Any]]:
    if source_file is not None:
        path = source_file
        if not path.is_absolute():
            path = (REPO_ROOT / path).resolve()
        with path.open() as f:
            data = json.load(f)
        return path, data
    return latest_round_doc(loop_dir)


def infer_duration(doc: dict[str, Any]) -> int | None:
    defaults = doc.get("defaults", {})
    if isinstance(defaults, dict) and isinstance(defaults.get("duration"), int):
        return defaults["duration"]
    durations = []
    for cand in doc.get("candidates", []):
        if isinstance(cand, dict) and isinstance(cand.get("duration_override"), int):
            durations.append(cand["duration_override"])
    if not durations:
        return None
    counts = {}
    for dur in durations:
        counts[dur] = counts.get(dur, 0) + 1
    best_duration, best_count = max(counts.items(), key=lambda item: (item[1], -item[0]))
    if best_count > 1 or len(set(durations)) == 1:
        return best_duration
    return None


def write_json(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, ensure_ascii=True) + "\n")


def run_json(cmd: list[str]) -> Any:
    res = subprocess.run(cmd, cwd=REPO_ROOT, text=True, capture_output=True)
    if res.returncode != 0:
        raise SystemExit(
            f"command failed ({res.returncode}): {' '.join(cmd)}\nSTDERR:\n{res.stderr}\nSTDOUT:\n{res.stdout}"
        )
    text = res.stdout.strip()
    if not text:
        return None
    return json.loads(text)


def namespace_prefixes(system: str | None) -> list[str]:
    if not system:
        return []
    prefixes: list[str] = []
    for candidate in [system, SYSTEM_NAMESPACE_PREFIX.get(system)]:
        if candidate and candidate not in prefixes:
            prefixes.append(candidate)
    return prefixes


def resolve_backend_system(system: str | None) -> str | None:
    if system is None:
        return None
    return SYSTEM_BACKEND_NAME.get(system, system)


def merge_excluded_chaos_types(extra: list[str]) -> list[str]:
    merged: list[str] = []
    for item in CLUSTER_HARD_EXCLUDED_CHAOS_TYPES + extra:
        value = str(item).strip()
        if value and value not in merged:
            merged.append(value)
    return merged


def namespace_sort_key(name: str, prefix: str) -> tuple[int, str]:
    suffix = name[len(prefix):]
    if suffix.isdigit():
        return (int(suffix), name)
    return (10**9, name)


def namespace_pod_count(namespace: str) -> int:
    res = subprocess.run(
        ["kubectl", "get", "pods", "-n", namespace, "--no-headers"],
        cwd=REPO_ROOT,
        text=True,
        capture_output=True,
    )
    if res.returncode != 0:
        return 0
    return len([line for line in res.stdout.splitlines() if line.strip()])


def resolve_namespace(explicit: str | None, system: str | None) -> str:
    if explicit:
        return explicit
    prefixes = namespace_prefixes(system)
    fallback = f"{prefixes[0]}0" if prefixes else f"{system}0"
    if not prefixes:
        return fallback
    try:
        data = run_json(["kubectl", "get", "ns", "-o", "json"])
    except SystemExit:
        return fallback
    items = data.get("items", []) if isinstance(data, dict) else []
    namespaces = [
        str(item.get("metadata", {}).get("name", ""))
        for item in items
        if isinstance(item, dict)
    ]
    live_candidates: list[tuple[int, str]] = []
    fallback_candidates: list[tuple[int, str]] = []
    for prefix in prefixes:
        matched = sorted(
            [name for name in namespaces if name == prefix or name.startswith(prefix)],
            key=lambda name: namespace_sort_key(name, prefix),
        )
        for name in matched:
            pod_count = namespace_pod_count(name)
            if pod_count > 0:
                live_candidates.append((namespace_sort_key(name, prefix)[0], name))
            else:
                fallback_candidates.append((namespace_sort_key(name, prefix)[0], name))
    if live_candidates:
        return min(live_candidates)[1]
    if fallback_candidates:
        return min(fallback_candidates)[1]
    return fallback


def render_prompt(state_dir: Path) -> None:
    template = REPO_ROOT / "ralph/examples/inject-loop/prompt.template.md"
    cmd = [
        "python3",
        str(REPO_ROOT / "ralph/examples/inject-loop/render_prompt.py"),
        "--template",
        str(template),
        "--campaign-file",
        str(state_dir / "campaign.json"),
        "--progress-file",
        str(state_dir / "progress.txt"),
        "--output",
        str(state_dir / "CODEX.md"),
    ]
    subprocess.run(cmd, check=True, cwd=REPO_ROOT)


def maybe_migrate(loop_dir: Path, duration: int) -> None:
    cmd = [
        "python3",
        str(REPO_ROOT / "experiments/lib/migrate_round_schema.py"),
        "--loop-dir",
        str(loop_dir),
        "--duration",
        str(duration),
        "--write",
    ]
    subprocess.run(cmd, check=True, cwd=REPO_ROOT)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--state-dir", required=True, type=Path)
    ap.add_argument("--loop-dir", required=True, type=Path)
    ap.add_argument("--source-file", type=Path)
    ap.add_argument("--duration-minutes", type=int)
    ap.add_argument("--batch-size", type=int, default=10)
    ap.add_argument("--pair-prob", type=float, default=0.3)
    ap.add_argument("--max-rounds", type=int, default=80)
    ap.add_argument("--history-window", type=int, default=3)
    ap.add_argument("--max-per-app-divisor", type=int, default=3)
    ap.add_argument("--project", default="pair_diagnosis")
    ap.add_argument("--namespace")
    ap.add_argument("--status")
    ap.add_argument("--exclude-chaos-type", action="append", default=[])
    ap.add_argument("--migrate-rounds", action="store_true")
    ap.add_argument("--force", action="store_true")
    args = ap.parse_args()

    latest_path, latest_doc = source_doc(args.loop_dir, args.source_file)
    duration = args.duration_minutes if args.duration_minutes is not None else infer_duration(latest_doc)
    if duration is None:
        raise SystemExit(
            "unable to infer fixed duration from latest round; pass --duration-minutes explicitly"
        )

    state_dir = args.state_dir
    state_dir.mkdir(parents=True, exist_ok=True)
    campaign_path = state_dir / "campaign.json"
    if campaign_path.exists() and not args.force:
        raise SystemExit(f"{campaign_path} already exists; pass --force to overwrite")

    if args.migrate_rounds:
        maybe_migrate(args.loop_dir, duration)
        latest_path, latest_doc = source_doc(args.loop_dir, args.source_file)

    inferred_round = latest_doc.get("round", round_number(latest_path) or 0)
    paused_note = args.loop_dir / "PAUSED.md"
    status = args.status
    if not status:
        status = "paused" if paused_note.exists() else "running"
    namespace = resolve_namespace(args.namespace, latest_doc.get("system"))

    defaults = latest_doc.get("defaults", {})
    campaign = {
        "campaign": state_dir.name,
        "system": latest_doc.get("system"),
        "backend_system": resolve_backend_system(latest_doc.get("system")),
        "project": args.project,
        "namespace": namespace,
        "loop_dir": str(args.loop_dir),
        "current_round": inferred_round,
        "max_rounds": args.max_rounds,
        "batch_size": args.batch_size,
        "history_window": args.history_window,
        "pair_prob": args.pair_prob,
        "status": status,
        "duration_policy": "fixed",
        "duration_minutes": duration,
        "forbid_duration_override": True,
        "max_per_app_divisor": args.max_per_app_divisor,
        "excluded_chaos_types": merge_excluded_chaos_types(args.exclude_chaos_type),
        "reward_policy": DEFAULT_REWARD_POLICY,
        "metadata": {
            "system_type": latest_doc.get("system_type"),
            "pedestal": latest_doc.get("pedestal"),
            "benchmark": latest_doc.get("benchmark"),
            "defaults": defaults,
            "latest_round_file": str(latest_path),
        },
    }
    write_json(campaign_path, campaign)

    progress_path = state_dir / "progress.txt"
    if not progress_path.exists():
        progress_path.write_text(
            "# Inject Loop Progress Log\n"
            f"Campaign: {campaign['campaign']}\n"
            f"Loop dir: {campaign['loop_dir']}\n"
            "---\n"
        )

    render_prompt(state_dir)

    print(f"initialized_campaign: {campaign_path}")
    print(f"rendered_prompt: {state_dir / 'CODEX.md'}")
    print(f"fixed_duration: {duration}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
