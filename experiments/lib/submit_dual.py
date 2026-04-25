#!/usr/bin/env python3
"""Submit a candidate batch with optional dual-fault pairing (30% prob per pair).

Reads a candidates_round*.json file. For each candidate, either:
  - single submit (1 spec in 1 batch, 1 trace), OR
  - paired submit with the next unpaired candidate (2 specs in 1 batch, 1 trace,
    same ns) — chosen with --pair-prob (default 0.3).

When a pair's trace gets a +1 reward, both candidates inherit it (caller responsibility).

Output: one JSON line per submission to stdout (and runs file). For pairs, the
line carries `paired_with` with the partner's candidate_id and the pair share a
single `trace_id`.

Server submission goes via aegisctl inject guided's underlying envelope POSTed
to /api/v2/projects/<pid>/injections/inject (so we can craft multi-spec batches).

Usage:
  submit_dual.py --candidates <path> --runs-out <path> [--pair-prob 0.3] \
                 [--seed 42] [--token-file ~/.aegisctl/config.yaml]
"""
import argparse
import json
import os
import random
import re
import sys
import time
import urllib.request
import urllib.error
import yaml

DEFAULT_SERVER = "http://localhost:8082"


def load_token(path: str) -> str:
    with open(os.path.expanduser(path)) as f:
        cfg = yaml.safe_load(f)
    ctx = cfg.get("current-context", "default")
    return cfg["contexts"][ctx]["token"]


def get_project_id(token: str, project_name: str, server: str) -> int:
    req = urllib.request.Request(
        f"{server}/api/v2/projects?page=1&size=100",
        headers={"Authorization": f"Bearer {token}"},
    )
    with urllib.request.urlopen(req) as resp:
        body = json.load(resp)
    for p in body["data"]["items"]:
        if p["name"] == project_name:
            return p["id"]
    raise SystemExit(f"project {project_name} not found")


def build_guided_config(cand: dict, defaults: dict, system: str, system_type: str) -> dict:
    """Map candidate row + defaults to a GuidedConfig dict for the API."""
    out = {
        "system": system,
        "system_type": system_type,
        "app": cand["app"],
        "chaos_type": cand["chaos_type"],
    }
    # Direct API needs explicit duration; CLI default-fills 5 (chaos-exp resolver
    # success branch). Mirror that here.
    out["duration"] = cand.get("duration_override", 5)
    container = defaults.get("container")
    params = cand.get("params", {})
    ct = cand["chaos_type"]
    # Mirror submit_round*.sh per-chaos_type field placement
    if ct in ("PodFailure", "PodKill"):
        pass
    elif ct == "ContainerKill":
        out["container"] = container
    elif ct in ("CPUStress",):
        out["container"] = container
        for k in ("cpu_load", "cpu_worker"):
            if k in params: out[k] = params[k]
    elif ct in ("MemoryStress",):
        out["container"] = container
        for k in ("memory_size", "mem_worker", "mem_type"):
            if k in params: out[k] = params[k]
    elif ct in ("JVMException", "JVMReturn", "JVMLatency", "JVMCPUStress",
                "JVMMemoryStress", "JVMRuntimeMutator", "JVMGarbageCollector"):
        if container: out["container"] = container
        for k in ("class", "method", "exception_opt", "return_value_opt", "return_type",
                  "latency_duration", "memory_size", "mem_type", "cpu_count",
                  "mutator_config"):
            if k in params: out[k] = params[k]
    elif ct.startswith("HTTP"):
        for k in ("route", "http_method", "target_service", "delay_duration",
                  "status_code", "replace_method", "body_type"):
            if k in params: out[k] = params[k]
    elif ct == "NetworkDelay":
        for k in ("target_service", "latency", "jitter", "correlation", "direction"):
            if k in params: out[k] = params[k]
    elif ct == "NetworkLoss":
        for k in ("target_service", "loss", "correlation", "direction"):
            if k in params: out[k] = params[k]
    elif ct == "NetworkBandwidth":
        for k in ("target_service", "rate", "limit", "buffer", "direction"):
            if k in params: out[k] = params[k]
    elif ct in ("NetworkCorrupt", "NetworkDuplicate"):
        for k in ("target_service", "corrupt", "duplicate", "correlation", "direction"):
            if k in params: out[k] = params[k]
    elif ct == "NetworkPartition":
        for k in ("target_service", "direction"):
            if k in params: out[k] = params[k]
    elif ct == "DNSError" or ct == "DNSRandom":
        for k in ("domain",):
            if k in params: out[k] = params[k]
    elif ct == "TimeSkew":
        for k in ("time_offset",):
            if k in params: out[k] = params[k]
    return out


def submit_batch(token: str, server: str, project_id: int, system: str, system_type: str,
                 specs: list, defaults: dict) -> dict:
    """Submit one batch (list of specs) to the inject API. specs share a ns
    when batch is allocated by --auto.
    """
    body = {
        "project_id": project_id,
        "system": system,
        "system_type": system_type,
        "pedestal": {
            "name": defaults["pedestal_name"],
            "tag": defaults["pedestal_tag"],
        },
        "benchmark": {
            "name": defaults["benchmark_name"],
            "tag": defaults["benchmark_tag"],
        },
        "interval": defaults["interval"],
        "pre_duration": defaults["pre_duration"],
        "specs": [specs],   # outer = batches, inner = specs in batch
        "auto_allocate": True,
        "allow_bootstrap": True,
        "skip_stale_check": True,
    }
    req = urllib.request.Request(
        f"{server}/api/v2/projects/{project_id}/injections/inject",
        data=json.dumps(body).encode(),
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        return {"error": e.read().decode(), "status": e.code}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--candidates", required=True)
    ap.add_argument("--runs-out", required=True)
    ap.add_argument("--pair-prob", type=float, default=0.3)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--token-file", default="~/.aegisctl/config.yaml")
    ap.add_argument("--server", default=DEFAULT_SERVER)
    ap.add_argument("--project", default="pair_diagnosis")
    args = ap.parse_args()
    rng = random.Random(args.seed)

    with open(args.candidates) as f:
        cdoc = json.load(f)
    cands = cdoc["candidates"]
    sysname = cdoc["system"]
    system_type = cdoc.get("system_type", sysname)
    pedestal = cdoc["pedestal"]
    bench = cdoc["benchmark"]
    defaults = {
        "pedestal_name": pedestal["name"], "pedestal_tag": pedestal["tag"],
        "benchmark_name": bench["name"], "benchmark_tag": bench["tag"],
        "interval": cdoc["defaults"]["interval"],
        "pre_duration": cdoc["defaults"]["pre_duration"],
        "container": cdoc["defaults"].get("container"),
    }

    token = load_token(args.token_file)
    project_id = get_project_id(token, args.project, args.server)
    print(f"project_id={project_id}", file=sys.stderr)

    # Group candidates: walk in order; with prob pair-prob, pair current with
    # next remaining; else solo.
    queue = list(cands)
    submissions = []  # list of (cid_list,)
    while queue:
        head = queue.pop(0)
        if queue and rng.random() < args.pair_prob:
            partner = queue.pop(0)
            submissions.append([head, partner])
        else:
            submissions.append([head])
    n_pairs = sum(1 for s in submissions if len(s) == 2)
    print(f"submissions: {len(submissions)} ({n_pairs} pairs, "
          f"{len(submissions)-n_pairs} singles)", file=sys.stderr)

    with open(args.runs_out, "a") as runs:
        for batch_cands in submissions:
            cids = [c["id"] for c in batch_cands]
            specs = [build_guided_config(c, defaults, sysname, system_type) for c in batch_cands]
            tag = "+".join(cids)
            print(f"=== submit {tag} ({len(specs)} specs) ===", file=sys.stderr)
            resp = submit_batch(token, args.server, project_id, sysname, system_type, specs, defaults)
            if "error" in resp:
                print(f"  FAIL: {resp['error']}", file=sys.stderr)
                for cid in cids:
                    runs.write(json.dumps({
                        "ts": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
                        "candidate_id": cid,
                        "paired_with": [c for c in cids if c != cid],
                        "error": resp.get("error"),
                    }) + "\n")
                continue
            data = resp.get("data", resp)
            items = data.get("items", [])
            # In a multi-spec batch, items[0] is the (single) batch result.
            if items:
                trace_id = items[0].get("trace_id")
                ns = items[0].get("allocated_namespace")
            else:
                trace_id = None
                ns = None
            print(f"  -> ns={ns} trace={trace_id}", file=sys.stderr)
            for cid in cids:
                runs.write(json.dumps({
                    "ts": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
                    "candidate_id": cid,
                    "paired_with": [c for c in cids if c != cid],
                    "group_id": data.get("group_id"),
                    "trace_id": trace_id,
                    "ns": ns,
                }) + "\n")
            runs.flush()
            time.sleep(2)


if __name__ == "__main__":
    main()
