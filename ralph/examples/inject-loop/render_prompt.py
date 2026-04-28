#!/usr/bin/env python3
"""Render a per-campaign Codex prompt from the inject-loop template."""

from __future__ import annotations

import argparse
from pathlib import Path


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--template", required=True, type=Path)
    ap.add_argument("--campaign-file", required=True, type=Path)
    ap.add_argument("--progress-file", required=True, type=Path)
    ap.add_argument("--output", required=True, type=Path)
    args = ap.parse_args()

    text = args.template.read_text()
    text = text.replace("{{CAMPAIGN_FILE}}", str(args.campaign_file))
    text = text.replace("{{PROGRESS_FILE}}", str(args.progress_file))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
