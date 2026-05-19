"""Empty-parquet validation: default fails, allow_empty warns.

Covers the env-var → flag refactor (removal of RCABENCH_OPTIONAL_EMPTY_PARQUETS).
"""

from __future__ import annotations

import json
from pathlib import Path

import polars as pl
import pytest

from rcabench_platform.v3.sdk.datasets.rcabench import REQUIRED_FILES, valid


@pytest.fixture(autouse=True)
def _skip_stability(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("RCABENCH_SKIP_STABILITY_VALIDATION", "1")


def _build_minimal_datapack(root: Path, empty_files: set[str]) -> None:
    root.mkdir(parents=True, exist_ok=True)
    for filename in REQUIRED_FILES:
        path = root / filename
        if filename.endswith(".json"):
            path.write_text(json.dumps({"placeholder": True}))
        elif filename.endswith(".parquet"):
            if filename in empty_files:
                df = pl.DataFrame(schema={"x": pl.Int64})
            else:
                df = pl.DataFrame({"x": [1]})
            df.write_parquet(path)


def test_empty_required_parquet_fails_by_default(tmp_path: Path) -> None:
    dp = tmp_path / "dp"
    _build_minimal_datapack(dp, empty_files={"abnormal_metrics_histogram.parquet"})

    _, ok = valid(dp)

    assert ok is False
    invalid = dp / ".invalid"
    assert invalid.exists()
    assert "abnormal_metrics_histogram.parquet" in invalid.read_text()


def test_allow_empty_lets_empty_parquet_pass(tmp_path: Path) -> None:
    dp = tmp_path / "dp"
    _build_minimal_datapack(dp, empty_files={"abnormal_metrics_histogram.parquet"})

    _, ok = valid(dp, allow_empty=True)

    assert ok is True
    assert (dp / ".valid").exists()
