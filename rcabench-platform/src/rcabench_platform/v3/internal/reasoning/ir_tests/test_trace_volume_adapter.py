"""TraceVolumeAdapter — Class E (traffic isolation) adapter tests.

Synthetic baselines target the §11.2 calibrator's stability bound:
typical fixtures use ~3 spans/s over a 3600s baseline + 300s abnormal
window so the calibrator gets ~660 baseline subwindows at the 5s stride
default — enough for a stable q_0.01 estimate. (Lower-rate / shorter-
baseline configurations cause the calibrator to opt out, which is
correct §11.2 behaviour but not what these tests are exercising.)
"""

from __future__ import annotations

import math
from pathlib import Path

import numpy as np
import polars as pl
import pytest

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.trace_volume import (
    TraceVolumeAdapter,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="trace-volume-fixture")


def _synth_traces(
    *,
    service: str,
    start_ts: int,
    end_ts: int,
    rate_per_sec: float,
    rng: np.random.Generator,
    span_name: str = "GET /noop",
) -> list[dict]:
    """Build synthetic per-second Poisson-distributed spans for one service.

    ``end_ts`` is exclusive. Each second draws ``Poisson(rate_per_sec)``
    spans, each tagged with the same ``span_name``.
    """
    rows: list[dict] = []
    for ts in range(start_ts, end_ts):
        n = int(rng.poisson(rate_per_sec)) if rate_per_sec > 0 else 0
        for i in range(n):
            rows.append(
                {
                    "time": ts,
                    "service_name": service,
                    "span_id": f"{service}-{ts}-{i}",
                    "span_name": span_name,
                }
            )
    return rows


def _df(rows: list[dict]) -> pl.DataFrame:
    if not rows:
        return pl.DataFrame(
            schema={
                "time": pl.Int64,
                "service_name": pl.Utf8,
                "span_id": pl.Utf8,
                "span_name": pl.Utf8,
            }
        )
    return pl.DataFrame(rows).with_columns(
        [
            pl.col("time").cast(pl.Int64),
            pl.col("service_name").cast(pl.Utf8),
            pl.col("span_id").cast(pl.Utf8),
            pl.col("span_name").cast(pl.Utf8),
        ]
    )


# Baseline / abnormal layout that gives the calibrator ~660 subwindows per
# service at the default stride — well above the §11.4 N≳120 floor for a
# stable q_0.01.
BASELINE_START = 1_000_000
BASELINE_END = BASELINE_START + 3600  # 3600s baseline
ABNORMAL_START = BASELINE_END
ABNORMAL_END = ABNORMAL_START + 300  # 300s abnormal window
RATE_STEADY = 3.0  # ~3 spans/s gives mean ~900 / subwindow


def test_emits_silent_when_service_disappears_from_abnormal() -> None:
    rng = np.random.default_rng(1)
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    base_rows += _synth_traces(
        service="B", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows = _synth_traces(
        service="A", start_ts=ABNORMAL_START, end_ts=ABNORMAL_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    # B disappears entirely from abnormal window.

    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    assert len(transitions) == 1, transitions
    t = transitions[0]
    assert t.node_key == "service|B"
    assert t.to_state == "silent"


def test_no_emission_when_abnormal_rate_within_baseline() -> None:
    rng = np.random.default_rng(2)
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows = _synth_traces(
        service="A", start_ts=ABNORMAL_START, end_ts=ABNORMAL_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    assert transitions == []


def test_skip_service_only_in_abnormal() -> None:
    rng = np.random.default_rng(3)
    # No baseline data for C; baseline only contains A.
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows = _synth_traces(
        service="A", start_ts=ABNORMAL_START, end_ts=ABNORMAL_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows += _synth_traces(
        service="C", start_ts=ABNORMAL_START, end_ts=ABNORMAL_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    # No transition for C (not in baseline) and no transition for A (steady).
    assert all(t.node_key != "service|C" for t in transitions)
    assert transitions == []


def test_baseline_too_short_skips_service() -> None:
    rng = np.random.default_rng(4)
    # Baseline window length (60s) < abnormal_window_length (300s)
    short_base_end = BASELINE_START + 60
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=short_base_end,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows: list[dict] = []  # no spans for A in abnormal -> would fire if we got that far
    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    assert transitions == []


def test_emit_transition_metadata() -> None:
    rng = np.random.default_rng(5)
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows: list[dict] = []  # A vanishes entirely in abnormal window
    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    assert len(transitions) == 1
    t = transitions[0]
    assert t.kind == PlaceKind.service
    assert t.level == EvidenceLevel.observed
    assert t.trigger == "trace_volume_drop"
    assert t.from_state == "healthy"
    assert t.to_state == "silent"
    assert t.at == ABNORMAL_START
    observed = t.evidence.get("observed")
    threshold = t.evidence.get("threshold")
    assert observed is not None and threshold is not None
    assert math.isfinite(observed) and math.isfinite(threshold)
    assert observed < threshold


def test_per_service_calibration() -> None:
    """A bursty service with sparse baseline opts out; a steady service emits.

    The "bursty" service has spans that exist only in a narrow ~310s slice
    of the baseline — wide enough to admit a couple of subwindows but with
    an extreme value spread. This drives the §11.2 calibrator into the
    `opt_out_reason="unstable"` path on a per-service basis. The steady
    service shares the same global baseline range and still calibrates
    cleanly, so when both go silent in the abnormal window only `steady`
    emits a transition.
    """

    rng = np.random.default_rng(6)
    base_rows: list[dict] = []
    # Steady service: rate ~3/s for the full baseline — calibrator stable.
    base_rows += _synth_traces(
        service="steady", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    # Bursty service: spans confined to a narrow [t0, t0+310] slice with
    # a spike in the middle. The slice is just long enough to yield a
    # handful of subwindows, but with a value spread that destabilises
    # the bootstrap quantile estimate.
    burst_start = BASELINE_START + 100
    bursty_rows: list[dict] = []
    bursty_rows += _synth_traces(
        service="bursty", start_ts=burst_start, end_ts=burst_start + 5,
        rate_per_sec=2.0, rng=rng, span_name="GET /burst",
    )
    bursty_rows += _synth_traces(
        service="bursty", start_ts=burst_start + 150, end_ts=burst_start + 160,
        rate_per_sec=200.0, rng=rng, span_name="GET /burst",
    )
    bursty_rows += _synth_traces(
        service="bursty", start_ts=burst_start + 305, end_ts=burst_start + 310,
        rate_per_sec=2.0, rng=rng, span_name="GET /burst",
    )
    base_rows += bursty_rows

    # Both services go silent in abnormal: no spans for either.
    abn_rows: list[dict] = []

    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    keys = {t.node_key for t in transitions}
    assert "service|steady" in keys, transitions
    assert "service|bursty" not in keys, transitions


def test_seeded_reproducibility() -> None:
    rng = np.random.default_rng(7)
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    base_rows += _synth_traces(
        service="B", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows = _synth_traces(
        service="A", start_ts=ABNORMAL_START, end_ts=ABNORMAL_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    base_df = _df(base_rows)
    abn_df = _df(abn_rows)

    def _run() -> list[tuple[str, str, float]]:
        adapter = TraceVolumeAdapter(
            base_df,
            abn_df,
            abnormal_window_start=ABNORMAL_START,
            abnormal_window_end=ABNORMAL_END,
            rng_seed=42,
        )
        out: list[tuple[str, str, float]] = []
        for t in adapter.emit(CTX):
            thr = t.evidence.get("threshold")
            assert thr is not None
            out.append((t.node_key, t.to_state, thr))
        return out

    first = _run()
    second = _run()
    assert first == second
    assert first  # we expect at least one SILENT (B disappears)


def test_zero_baseline_traffic_skipped() -> None:
    rng = np.random.default_rng(8)
    # Zero-traffic service Z is never present. We feed an unrelated baseline
    # plus an abnormal window in which Z also has zero spans. The adapter
    # cannot calibrate Z (it has no baseline rows) and must not raise.
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows = _synth_traces(
        service="A", start_ts=ABNORMAL_START, end_ts=ABNORMAL_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    assert all(t.node_key != "service|Z" for t in transitions)
    # A is steady -> no transition expected either.
    assert transitions == []


def test_invalid_window_raises() -> None:
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=np.random.default_rng(0),
    )
    with pytest.raises(ValueError):
        TraceVolumeAdapter(
            _df(base_rows),
            _df([]),
            abnormal_window_start=ABNORMAL_END,
            abnormal_window_end=ABNORMAL_START,
            rng_seed=42,
        )
    with pytest.raises(ValueError):
        TraceVolumeAdapter(
            _df(base_rows),
            _df([]),
            abnormal_window_start=ABNORMAL_START,
            abnormal_window_end=ABNORMAL_START,
            rng_seed=42,
        )


def test_silent_evidence_carries_specialization_label() -> None:
    rng = np.random.default_rng(9)
    base_rows = _synth_traces(
        service="A", start_ts=BASELINE_START, end_ts=BASELINE_END,
        rate_per_sec=RATE_STEADY, rng=rng,
    )
    abn_rows: list[dict] = []  # A goes silent
    adapter = TraceVolumeAdapter(
        _df(base_rows),
        _df(abn_rows),
        abnormal_window_start=ABNORMAL_START,
        abnormal_window_end=ABNORMAL_END,
        rng_seed=42,
    )
    transitions = list(adapter.emit(CTX))
    assert len(transitions) == 1
    labels = transitions[0].evidence.get("specialization_labels")
    assert labels is not None
    assert "silent_class_e" in labels
