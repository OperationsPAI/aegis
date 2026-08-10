#!/usr/bin/env -S uv run -s
"""CLI wrapper mounting the reasoning sub-app alongside detector / eval / sample."""

from pathlib import Path

from dotenv import load_dotenv

from rcabench_platform.v3.cli.main import app
from rcabench_platform.v3.internal.reasoning.cli import app as reason_app

load_dotenv(Path.cwd() / ".env")

app.add_typer(reason_app, name="reason", help="Fault propagation reasoning / labeling")


if __name__ == "__main__":
    app()
