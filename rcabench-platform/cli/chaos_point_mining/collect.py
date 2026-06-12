#!/usr/bin/env python3
"""Collect raw NORMAL-phase datapack traces from the blob, ready for mine.py.

Pipeline:
  1. enumerate detector_success injections (per project) via `aegisctl inject list`
  2. classify each by system from its env.json NAMESPACE (cheap `blob cat`)
  3. select up to --per-system datapacks per system (optionally date-filtered)
  4. download only `normal_traces.parquet` for the selected (`blob cp`)

Then run:  mine.py --datapack-root <out>/dp --out <observed-dir>

Only the raw top-level `normal_traces.parquet` is fetched (not the `-r` full
datapack), so this stays light. Systems come from the env NAMESPACE with
trailing digits stripped (otel-demo1 -> otel-demo).
"""
from __future__ import annotations

import argparse
import json
import random
import re
import subprocess
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path


def aeg(args: list[str], binary: str, insecure: bool) -> str:
    cmd = [binary]
    if insecure:
        cmd.append("--insecure-skip-tls-verify")
    cmd += args
    return subprocess.run(cmd, capture_output=True, text=True).stdout


def enumerate_names(binary: str, insecure: bool, projects: list[str]) -> list[str]:
    names: set[str] = set()
    for proj in projects:
        out = aeg(["inject", "list", "--project", proj, "--state", "detector_success",
                   "--all", "-o", "ndjson"], binary, insecure)
        for line in out.splitlines():
            try:
                names.add(json.loads(line)["name"])
            except Exception:
                pass
    return sorted(names)


def classify(binary: str, insecure: bool, names: list[str], workers: int) -> dict[str, tuple[str, int]]:
    """name -> (system, normal_start_epoch)"""
    def one(name: str):
        out = aeg(["blob", "cat", f"datapack:{name}/env.json"], binary, insecure)
        try:
            d = json.loads(out)
            ns = str(d.get("NAMESPACE", ""))
            return name, re.sub(r"\d+$", "", ns), int(d.get("NORMAL_START", 0) or 0)
        except Exception:
            return name, "", 0
    res = {}
    with ThreadPoolExecutor(max_workers=workers) as ex:
        for name, system, start in ex.map(one, names):
            if system:
                res[name] = (system, start)
    return res


def download(binary: str, insecure: bool, names: list[str], dp_dir: Path, workers: int) -> int:
    def one(name: str) -> bool:
        dst = dp_dir / name
        dst.mkdir(parents=True, exist_ok=True)
        f = dst / "normal_traces.parquet"
        if f.exists() and f.stat().st_size > 0:
            return True
        aeg(["blob", "cp", f"datapack:{name}/normal_traces.parquet", str(f)], binary, insecure)
        return f.exists() and f.stat().st_size > 0
    with ThreadPoolExecutor(max_workers=workers) as ex:
        return sum(ex.map(one, names))


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True, type=Path, help="work dir (datapacks land under <out>/dp)")
    ap.add_argument("--projects", nargs="+", default=["pair_diagnosis", "session_aware_rca"])
    ap.add_argument("--per-system", type=int, default=100)
    ap.add_argument("--aegisctl", default="aegisctl")
    ap.add_argument("--insecure", action="store_true", help="pass --insecure-skip-tls-verify")
    ap.add_argument("--workers", type=int, default=24)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--ts-before-epoch", type=int, default=0,
                    help="if set, keep only ts datapacks with NORMAL_START < this epoch")
    args = ap.parse_args()

    names = enumerate_names(args.aegisctl, args.insecure, args.projects)
    print(f"enumerated {len(names)} detector_success injections")
    idx = classify(args.aegisctl, args.insecure, names, args.workers)
    print(f"classified {len(idx)} datapacks")

    pool: dict[str, list[str]] = defaultdict(list)
    for name, (system, start) in idx.items():
        if system == "ob":
            continue
        if system == "ts" and args.ts_before_epoch and not (0 < start < args.ts_before_epoch):
            continue
        pool[system].append(name)

    rng = random.Random(args.seed)
    selected: list[str] = []
    for system in sorted(pool):
        bucket = pool[system][:]
        rng.shuffle(bucket)
        pick = bucket[: args.per_system]
        selected += pick
        print(f"  {len(pick):4d}/{len(pool[system]):<5d}  {system}")

    dp_dir = args.out / "dp"
    print(f"downloading {len(selected)} normal_traces.parquet -> {dp_dir}")
    ok = download(args.aegisctl, args.insecure, selected, dp_dir, args.workers)
    print(f"downloaded {ok}/{len(selected)}")
    print(f"\nnext: python mine.py --datapack-root {dp_dir} --out <observed-dir>")


if __name__ == "__main__":
    main()
