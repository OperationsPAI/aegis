#!/usr/bin/env python3
"""Launch or inspect multiple per-system Codex inject loops."""

from __future__ import annotations

import argparse
import json
import os
import signal
import subprocess
import sys
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[3]
RalphScript = REPO_ROOT / "ralph/ralph.sh"
TemplatePath = REPO_ROOT / "ralph/examples/inject-loop/prompt.template.md"
RenderScript = REPO_ROOT / "ralph/examples/inject-loop/render_prompt.py"


def load_manifest(path: Path) -> dict[str, Any]:
    with path.open() as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: expected JSON object")
    return data


def resolve(base: Path, value: str) -> Path:
    p = Path(value)
    if p.is_absolute():
        return p
    return (base / p).resolve()


def pid_is_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return True


def render_prompt(state_dir: Path) -> None:
    subprocess.run(
        [
            "python3",
            str(RenderScript),
            "--template",
            str(TemplatePath),
            "--campaign-file",
            str(state_dir / "campaign.json"),
            "--progress-file",
            str(state_dir / "progress.txt"),
            "--output",
            str(state_dir / "CODEX.md"),
        ],
        check=True,
        cwd=REPO_ROOT,
    )


def load_campaign_status(state_dir: Path) -> str:
    campaign_path = state_dir / "campaign.json"
    if not campaign_path.exists():
        return "unknown"
    with campaign_path.open() as f:
        data = json.load(f)
    return str(data.get("status", "unknown"))


def launch(manifest_path: Path, dry_run: bool, include_paused: bool) -> int:
    manifest = load_manifest(manifest_path)
    base = manifest_path.parent.resolve()
    workdir = resolve(base, manifest.get("workdir", str(REPO_ROOT)))
    default_iterations = int(manifest.get("default_max_iterations", 12))
    default_tool = manifest.get("tool", "codex")

    for item in manifest.get("campaigns", []):
        if not isinstance(item, dict):
            continue
        name = item.get("name") or Path(item["state_dir"]).name
        state_dir = resolve(workdir, item["state_dir"])
        state_dir.mkdir(parents=True, exist_ok=True)
        campaign_status = load_campaign_status(state_dir)
        if campaign_status != "running" and not include_paused:
            print(f"[skip] {name}: campaign status={campaign_status}")
            continue
        pid_file = state_dir / "codex.pid"
        log_file = state_dir / "codex.loop.log"
        if pid_file.exists():
            try:
                pid = int(pid_file.read_text().strip())
            except ValueError:
                pid = -1
            if pid > 0 and pid_is_alive(pid):
                print(f"[skip] {name}: already running with pid {pid}")
                continue

        render_prompt(state_dir)
        cmd = [
            str(RalphScript),
            "--tool",
            str(item.get("tool", default_tool)),
            "--state-dir",
            str(state_dir),
            "--prompt-file",
            str(state_dir / "CODEX.md"),
            str(item.get("max_iterations", default_iterations)),
        ]
        if dry_run:
            print(f"[dry-run] {name}: cwd={workdir} cmd={' '.join(cmd)}")
            continue

        log_handle = log_file.open("a")
        proc = subprocess.Popen(
            cmd,
            cwd=workdir,
            stdout=log_handle,
            stderr=subprocess.STDOUT,
        )
        pid_file.write_text(f"{proc.pid}\n")
        print(f"[launched] {name}: pid={proc.pid} state_dir={state_dir} log={log_file}")
    return 0


def status(manifest_path: Path) -> int:
    manifest = load_manifest(manifest_path)
    base = manifest_path.parent.resolve()
    workdir = resolve(base, manifest.get("workdir", str(REPO_ROOT)))
    for item in manifest.get("campaigns", []):
        if not isinstance(item, dict):
            continue
        name = item.get("name") or Path(item["state_dir"]).name
        state_dir = resolve(workdir, item["state_dir"])
        pid_file = state_dir / "codex.pid"
        log_file = state_dir / "codex.loop.log"
        if not pid_file.exists():
            print(f"[idle] {name}: no pid file")
            continue
        try:
            pid = int(pid_file.read_text().strip())
        except ValueError:
            print(f"[broken] {name}: invalid pid file")
            continue
        alive = pid_is_alive(pid)
        tag = "running" if alive else "stopped"
        print(f"[{tag}] {name}: pid={pid} state_dir={state_dir}")
        if log_file.exists():
            lines = log_file.read_text(errors="replace").splitlines()
            for line in lines[-3:]:
                print(f"  log: {line}")
    return 0


def stop(manifest_path: Path) -> int:
    manifest = load_manifest(manifest_path)
    base = manifest_path.parent.resolve()
    workdir = resolve(base, manifest.get("workdir", str(REPO_ROOT)))
    for item in manifest.get("campaigns", []):
        if not isinstance(item, dict):
            continue
        name = item.get("name") or Path(item["state_dir"]).name
        state_dir = resolve(workdir, item["state_dir"])
        pid_file = state_dir / "codex.pid"
        if not pid_file.exists():
            print(f"[idle] {name}: no pid file")
            continue
        try:
            pid = int(pid_file.read_text().strip())
        except ValueError:
            print(f"[broken] {name}: invalid pid file")
            continue
        if pid_is_alive(pid):
            os.kill(pid, signal.SIGTERM)
            print(f"[stopped] {name}: pid={pid}")
        else:
            print(f"[stale] {name}: pid={pid} already exited")
        pid_file.unlink(missing_ok=True)
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("command", choices=["launch", "status", "stop"])
    ap.add_argument("--manifest", required=True, type=Path)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--include-paused", action="store_true")
    args = ap.parse_args()
    if args.command == "launch":
        return launch(args.manifest, args.dry_run, args.include_paused)
    if args.command == "status":
        return status(args.manifest)
    return stop(args.manifest)


if __name__ == "__main__":
    sys.exit(main())
