#!/usr/bin/env python3
"""For each missing frontend path in the index, search the current tree
for a file with the same basename and emit a mapping: stale → candidate(s).

Output categories:
  exact     — exactly one basename match in AegisLab-frontend/src/
  multi     — multiple basename matches (need human pick)
  missing   — no basename match (really gone)
"""
from __future__ import annotations
import json
import os
import sys

import yaml

WS = "/home/ddq/AoyangSpace/aegis"
FRONTEND_SRC = os.path.join(WS, "AegisLab-frontend", "src")


def build_basename_index() -> dict[str, list[str]]:
    idx: dict[str, list[str]] = {}
    for root, _, files in os.walk(FRONTEND_SRC):
        for f in files:
            full = os.path.join(root, f)
            rel = os.path.relpath(full, WS)   # workspace-relative
            idx.setdefault(f, []).append(rel)
    return idx


def main() -> int:
    doc = yaml.safe_load(open(os.path.join(WS, "project-index.yaml")))
    basename_idx = build_basename_index()

    buckets: dict[str, list[dict]] = {"exact": [], "multi": [], "missing": []}

    for req in doc.get("requirements", []):
        rid = req.get("id")
        for entry in req.get("frontend") or []:
            p = entry["path"] if isinstance(entry, dict) else entry
            if not p:
                continue
            if os.path.exists(os.path.join(WS, p)):
                continue
            basename = os.path.basename(p)
            candidates = basename_idx.get(basename, [])
            # Exclude self (already known missing)
            candidates = [c for c in candidates if c != p]
            record = {"req": rid, "stale": p, "candidates": candidates}
            if len(candidates) == 0:
                buckets["missing"].append(record)
            elif len(candidates) == 1:
                buckets["exact"].append(record)
            else:
                buckets["multi"].append(record)

    print(json.dumps(buckets, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
