"""Regression test: detector `run` must refuse to silently default to system='ts'.

Before this fix, every non-train-ticket datapack (hs, otel-demo, sn, media,
sockshop, teastore) failed inside the detector with a misleading
"No entrance traffic found in normal or abnormal trace data" error, because
the entrypoint shipped with `--system ts` baked in. We now require the
pedestal to be provided either by `--system` or via the `BENCHMARK_SYSTEM`
env var, and fail-fast otherwise so the dispatch bug is loud.
"""

from __future__ import annotations

import os
from pathlib import Path

import pytest


@pytest.fixture(autouse=True)
def _clear_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("BENCHMARK_SYSTEM", raising=False)


def _import_run():
    # Imported lazily so pytest collection does not pull in heavy detector deps
    # when the suite is run for unrelated reasons.
    from cli.detector import run  # type: ignore[import-not-found]

    return run


def test_run_refuses_to_default_to_ts_when_system_not_provided(tmp_path: Path) -> None:
    run = _import_run()
    with pytest.raises(ValueError) as excinfo:
        run(in_p=tmp_path, ou_p=tmp_path, system=None)
    msg = str(excinfo.value)
    assert "BENCHMARK_SYSTEM" in msg or "system" in msg.lower(), msg


def test_run_reads_benchmark_system_env_var(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Sanity-check the env-var plumbing: BENCHMARK_SYSTEM must reach the run()
    body. We can't execute the full pipeline (needs parquet inputs), so we just
    assert the early-validation path no longer raises the missing-system error.
    A different error from setup_paths_and_validation is acceptable -- it means
    we passed the system-required gate."""
    monkeypatch.setenv("BENCHMARK_SYSTEM", "hs")
    run = _import_run()
    try:
        run(in_p=tmp_path, ou_p=tmp_path, system=None)
    except ValueError as e:
        assert "BENCHMARK_SYSTEM" not in str(e), (
            f"system gate still tripped despite BENCHMARK_SYSTEM=hs: {e}"
        )
    except Exception:
        # Any non-ValueError or downstream ValueError is fine -- it means the
        # system-required gate accepted the env var and execution proceeded.
        pass
