#!/usr/bin/env python3
"""Build a live per-system injection/trace mix snapshot for inject-loop planning."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[2]
AEGISCTL = REPO_ROOT / "AegisLab/bin/aegisctl"
POD_FAMILY = {"PodFailure", "PodKill", "ContainerKill"}
NETWORK_PREFIX = "Network"
HTTP_PREFIX = "HTTP"
JVM_PREFIX = "JVM"


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


def load_json(path: Path) -> dict[str, Any]:
    with path.open() as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: expected top-level object")
    return data


def write_json(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, ensure_ascii=True) + "\n")


def chaos_type_excluded(chaos_type: str, excluded_chaos_types: set[str]) -> bool:
    for pattern in excluded_chaos_types:
        if pattern.endswith("*"):
            if chaos_type.startswith(pattern[:-1]):
                return True
            continue
        if chaos_type == pattern:
            return True
    return False


def classify_family(chaos_type: str) -> str:
    if chaos_type in POD_FAMILY:
        return "pod"
    if chaos_type.startswith(NETWORK_PREFIX):
        return "network"
    if chaos_type.startswith(HTTP_PREFIX):
        return "http"
    if chaos_type.startswith(JVM_PREFIX):
        return "jvm"
    if chaos_type in {"CPUStress", "MemoryStress"}:
        return "stress"
    if chaos_type.startswith("DNS"):
        return "dns"
    if chaos_type == "TimeSkew":
        return "time"
    return "other"


def load_campaign(path: Path) -> dict[str, Any]:
    data = load_json(path)
    required = ["system", "loop_dir", "project"]
    missing = [key for key in required if key not in data]
    if missing:
        raise SystemExit(f"{path}: missing required campaign keys: {missing}")
    return data


def resolve_paths(campaign: dict[str, Any], campaign_path: Path) -> tuple[Path, Path]:
    loop_dir = Path(campaign["loop_dir"])
    if not loop_dir.is_absolute():
        loop_dir = (REPO_ROOT / loop_dir).resolve()
    state_dir = campaign_path.parent.resolve()
    return loop_dir, state_dir


def filtered_candidates(
    candidates: list[dict[str, Any]], excluded_chaos_types: set[str]
) -> list[dict[str, Any]]:
    if not excluded_chaos_types:
        return candidates
    out: list[dict[str, Any]] = []
    for cand in candidates:
        chaos_type = cand.get("chaos_type")
        if isinstance(chaos_type, str) and chaos_type_excluded(chaos_type, excluded_chaos_types):
            continue
        out.append(cand)
    return out


def refresh_supported(
    system: str, namespace: str, output_path: Path, excluded_chaos_types: set[str]
) -> list[dict[str, Any]]:
    data = run_json(
        [
            str(AEGISCTL),
            "inject",
            "candidates",
            "ls",
            "--system",
            system,
            "--namespace",
            namespace,
            "-o",
            "json",
        ]
    )
    if not isinstance(data, list):
        raise SystemExit("aegisctl inject candidates ls returned non-list JSON")
    filtered = filtered_candidates(data, excluded_chaos_types)
    write_json(
        output_path,
        {
            "candidates": filtered,
            "_meta": {
                "raw_candidate_count": len(data),
                "filtered_candidate_count": len(filtered),
                "excluded_chaos_types": sorted(excluded_chaos_types),
            },
        },
    )
    return filtered


def load_supported_candidates(path: Path) -> list[dict[str, Any]]:
    data = load_json(path)
    candidates = data.get("candidates")
    if not isinstance(candidates, list):
        raise SystemExit(f"{path}: expected .candidates list")
    return [cand for cand in candidates if isinstance(cand, dict)]


def summarize_candidates(candidates: list[dict[str, Any]]) -> dict[str, Any]:
    by_type = Counter()
    by_family = Counter()
    by_app = Counter()
    pairs = Counter()
    for cand in candidates:
        chaos_type = str(cand.get("chaos_type", ""))
        app = str(cand.get("app", ""))
        if not chaos_type:
            continue
        by_type[chaos_type] += 1
        by_family[classify_family(chaos_type)] += 1
        if app:
            by_app[app] += 1
            pairs[f"{app}::{chaos_type}"] += 1
    return {
        "candidate_count": len(candidates),
        "by_chaos_type": dict(sorted(by_type.items())),
        "by_family": dict(sorted(by_family.items())),
        "by_app": dict(sorted(by_app.items())),
        "app_chaos_pairs": dict(sorted(pairs.items())),
    }


def summarize_injections(details: list[dict[str, Any]]) -> dict[str, Any]:
    by_type = Counter()
    by_fault_type = Counter()
    by_state = Counter()
    by_family = Counter()
    for item in details:
        chaos_type = item.get("chaos_type") or ""
        fault_type = item.get("fault_type") or ""
        state = item.get("state") or ""
        if chaos_type:
            by_type[str(chaos_type)] += 1
            by_family[classify_family(str(chaos_type))] += 1
        if fault_type:
            by_fault_type[str(fault_type)] += 1
        if state:
            by_state[str(state)] += 1
    return {
        "injection_count": len(details),
        "by_chaos_type": dict(sorted(by_type.items())),
        "by_fault_type": dict(sorted(by_fault_type.items())),
        "by_state": dict(sorted(by_state.items())),
        "by_family": dict(sorted(by_family.items())),
    }


def load_round_candidates(loop_dir: Path) -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    for path in sorted(loop_dir.glob("candidates_round*.json")):
        doc = load_json(path)
        round_number = doc.get("round")
        for cand in doc.get("candidates", []):
            if not isinstance(cand, dict):
                continue
            cid = str(cand.get("id", ""))
            if not cid:
                continue
            out[cid] = {
                "round": round_number,
                "app": cand.get("app"),
                "chaos_type": cand.get("chaos_type"),
                "params": cand.get("params", {}),
            }
    legacy = loop_dir / "candidates.json"
    if legacy.exists():
        doc = load_json(legacy)
        for cand in doc.get("candidates", []):
            if not isinstance(cand, dict):
                continue
            cid = str(cand.get("id", ""))
            if cid:
                out[cid] = {
                    "round": 0,
                    "app": cand.get("app"),
                    "chaos_type": cand.get("chaos_type"),
                    "params": cand.get("params", {}),
                }
    return out


def load_runs(loop_dir: Path, candidate_map: dict[str, dict[str, Any]]) -> tuple[list[dict[str, Any]], dict[str, dict[str, Any]]]:
    runs: list[dict[str, Any]] = []
    trace_index: dict[str, dict[str, Any]] = {}
    for path in sorted(loop_dir.glob("runs_round*.jsonl")):
        with path.open() as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                item = json.loads(line)
                if not isinstance(item, dict):
                    continue
                cid = str(item.get("candidate_id", ""))
                meta = candidate_map.get(cid, {})
                merged = dict(item)
                merged.update(
                    {
                        "app": meta.get("app"),
                        "chaos_type": meta.get("chaos_type"),
                        "round": meta.get("round"),
                    }
                )
                runs.append(merged)
                trace_id = str(item.get("trace_id", "") or "")
                if trace_id:
                    trace_index.setdefault(
                        trace_id,
                        {
                            "trace_id": trace_id,
                            "app": meta.get("app"),
                            "chaos_type": meta.get("chaos_type"),
                            "round": meta.get("round"),
                            "group_id": item.get("group_id"),
                            "ns": item.get("ns"),
                        },
                    )
    legacy = loop_dir / "candidates.json"
    if legacy.exists():
        doc = load_json(legacy)
        for cand in doc.get("candidates", []):
            if not isinstance(cand, dict):
                continue
            cid = str(cand.get("id", ""))
            for hist in cand.get("history", []):
                if not isinstance(hist, dict):
                    continue
                trace_id = str(hist.get("trace_id", ""))
                if not trace_id:
                    continue
                trace_index.setdefault(
                    trace_id,
                    {
                        "trace_id": trace_id,
                        "app": cand.get("app"),
                        "chaos_type": cand.get("chaos_type"),
                        "round": 0,
                        "group_id": None,
                        "ns": hist.get("ns"),
                        "terminal": hist.get("terminal"),
                        "reward": hist.get("reward"),
                    },
                )
    return runs, trace_index


def trace_detail(trace_id: str, project: str) -> dict[str, Any]:
    data = run_json(
        [str(AEGISCTL), "trace", "get", trace_id, "--project", project, "-o", "json"]
    )
    if not isinstance(data, dict):
        raise SystemExit(f"trace get {trace_id} returned non-object JSON")
    return data


def fetch_trace_details(project: str, trace_ids: list[str]) -> tuple[list[dict[str, Any]], list[str]]:
    details: list[dict[str, Any]] = []
    failures: list[str] = []
    for trace_id in trace_ids:
        try:
            details.append(trace_detail(trace_id, project))
        except SystemExit:
            failures.append(trace_id)
    return details, failures


def summarize_runs(runs: list[dict[str, Any]]) -> dict[str, Any]:
    by_type = Counter()
    by_round = Counter()
    submit_errors = Counter()
    for item in runs:
        chaos_type = item.get("chaos_type")
        if chaos_type:
            by_type[str(chaos_type)] += 1
        if item.get("round") is not None:
            by_round[str(item.get("round"))] += 1
        err = item.get("error")
        if err:
            submit_errors[str(err)] += 1
    return {
        "submission_count": len(runs),
        "by_chaos_type": dict(sorted(by_type.items())),
        "by_round": dict(sorted(by_round.items())),
        "submit_errors": dict(sorted(submit_errors.items())),
    }


def summarize_live_candidate_mix(
    runs: list[dict[str, Any]],
    trace_lookup: dict[str, dict[str, Any]],
) -> dict[str, Any]:
    by_type = Counter()
    by_fault_type = Counter()
    by_state = Counter()
    by_last_event = Counter()
    by_family = Counter()
    missing_trace_ids: list[str] = []

    details: list[dict[str, Any]] = []
    for run in runs:
        trace_id = str(run.get("trace_id", "") or "")
        if not trace_id:
            continue
        trace = trace_lookup.get(trace_id)
        if trace is None:
            missing_trace_ids.append(trace_id)
            continue
        chaos_type = str(run.get("chaos_type", "") or "")
        state = str(trace.get("state", "") or "")
        last_event = str(trace.get("last_event", "") or "")
        fault_type = str(trace.get("type", "") or "")
        if chaos_type:
            by_type[chaos_type] += 1
            by_family[classify_family(chaos_type)] += 1
        if state:
            by_state[state] += 1
        if last_event:
            by_last_event[last_event] += 1
        if fault_type:
            by_fault_type[fault_type] += 1
        details.append(
            {
                "candidate_id": run.get("candidate_id"),
                "trace_id": trace_id,
                "chaos_type": chaos_type,
                "app": run.get("app"),
                "round": run.get("round"),
                "state": state,
                "last_event": last_event,
                "type": fault_type,
            }
        )

    return {
        "injection_count": len(details),
        "by_chaos_type": dict(sorted(by_type.items())),
        "by_fault_type": dict(sorted(by_fault_type.items())),
        "by_state": dict(sorted(by_state.items())),
        "by_last_event": dict(sorted(by_last_event.items())),
        "by_family": dict(sorted(by_family.items())),
        "missing_trace_ids": sorted(set(missing_trace_ids)),
        "sample": details[:25],
    }


def summarize_traces(
    traces: list[dict[str, Any]],
    trace_index: dict[str, dict[str, Any]],
) -> dict[str, Any]:
    by_state = Counter()
    by_last_event = Counter()
    by_type: dict[str, Counter[str]] = defaultdict(Counter)
    by_round = Counter()

    joined: list[dict[str, Any]] = []
    for trace in traces:
        trace_id = str(trace.get("id", trace.get("trace_id", "")))
        if trace_id not in trace_index:
            continue
        meta = trace_index[trace_id]
        chaos_type = str(meta.get("chaos_type", "") or "")
        state = str(trace.get("state", "") or "")
        last_event = str(trace.get("last_event", "") or "")
        if state:
            by_state[state] += 1
        if last_event:
            by_last_event[last_event] += 1
        if chaos_type:
            by_type[chaos_type]["total"] += 1
            if state:
                by_type[chaos_type][f"state:{state}"] += 1
            if last_event:
                by_type[chaos_type][f"event:{last_event}"] += 1
        if meta.get("round") is not None:
            by_round[str(meta.get("round"))] += 1
        joined.append(
            {
                "trace_id": trace_id,
                "chaos_type": chaos_type,
                "app": meta.get("app"),
                "round": meta.get("round"),
                "ns": meta.get("ns"),
                "group_id": meta.get("group_id"),
                "state": state,
                "last_event": last_event,
                "type": trace.get("type"),
            }
        )

    return {
        "joined_trace_count": len(joined),
        "by_state": dict(sorted(by_state.items())),
        "by_last_event": dict(sorted(by_last_event.items())),
        "by_chaos_type": {key: dict(sorted(value.items())) for key, value in sorted(by_type.items())},
        "by_round": dict(sorted(by_round.items())),
        "pending_trace_ids": [
            item["trace_id"] for item in joined if item.get("state") in {"Pending", "Running"}
        ],
        "sample": joined[:25],
    }


def compute_gap(
    supported_mix: dict[str, Any],
    injection_mix: dict[str, Any],
    trace_mix: dict[str, Any],
) -> dict[str, Any]:
    supported_types = set(supported_mix.get("by_chaos_type", {}).keys())
    injected_types = set(injection_mix.get("by_chaos_type", {}).keys())
    traced_types = set(trace_mix.get("by_chaos_type", {}).keys())
    return {
        "missing_from_injections": sorted(supported_types - injected_types),
        "missing_from_traces": sorted(supported_types - traced_types),
        "overrepresented_pod_family": injection_mix.get("by_family", {}).get("pod", 0),
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--campaign", type=Path, required=True)
    ap.add_argument("--write", type=Path)
    ap.add_argument("--refresh-supported", action="store_true")
    ap.add_argument("--page-size", type=int, default=100)
    args = ap.parse_args()

    campaign_path = args.campaign.resolve()
    campaign = load_campaign(campaign_path)
    loop_dir, state_dir = resolve_paths(campaign, campaign_path)
    project = str(campaign["project"])
    system = str(campaign["system"])
    backend_system = str(campaign.get("backend_system", system))
    excluded_chaos_types = {
        str(item) for item in campaign.get("excluded_chaos_types", []) if str(item).strip()
    }
    namespace = campaign.get("namespace")
    namespace_str = str(namespace) if namespace else None
    supported_path = loop_dir / "_supported_candidates.json"

    if args.refresh_supported:
        if not namespace_str:
            raise SystemExit("campaign namespace is required for --refresh-supported")
        supported_candidates = refresh_supported(
            backend_system, namespace_str, supported_path, excluded_chaos_types
        )
    else:
        if not supported_path.exists():
            raise SystemExit(
                f"{supported_path} missing; run live_mix with --refresh-supported once the system namespace is available"
            )
        supported_candidates = load_supported_candidates(supported_path)

    supported_candidates = filtered_candidates(supported_candidates, excluded_chaos_types)

    candidate_map = load_round_candidates(loop_dir)
    runs, trace_index = load_runs(loop_dir, candidate_map)
    trace_ids = sorted(trace_index.keys())
    trace_items, trace_fetch_failures = fetch_trace_details(project, trace_ids)
    trace_lookup = {
        str(item.get("id", item.get("trace_id", ""))): item
        for item in trace_items
        if isinstance(item, dict)
    }
    supported_mix = summarize_candidates(supported_candidates)
    injection_mix = summarize_live_candidate_mix(runs, trace_lookup)
    trace_mix = summarize_traces(trace_items, trace_index)
    runs_mix = summarize_runs(runs)

    report = {
        "system": system,
        "backend_system": backend_system,
        "project": project,
        "namespace": namespace_str,
        "loop_dir": str(loop_dir),
        "campaign": str(campaign_path),
        "supported": supported_mix,
        "injections": injection_mix,
        "runs": runs_mix,
        "traces": trace_mix,
        "coverage_gap": compute_gap(supported_mix, injection_mix, trace_mix),
        "notes": {
            "injections_source": "local loop submissions joined with backend trace state",
            "trace_source": "backend trace get on trace_ids from local runs/history",
            "trace_fetch_failures": trace_fetch_failures,
            "excluded_chaos_types": sorted(excluded_chaos_types),
        },
    }

    output_path = args.write or (state_dir / "live_mix.json")
    write_json(output_path, report)
    print(json.dumps(report, indent=2, ensure_ascii=True))
    print(f"\nwritten_report: {output_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
