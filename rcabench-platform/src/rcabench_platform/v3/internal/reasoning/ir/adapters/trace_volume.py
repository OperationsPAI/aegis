"""TraceVolumeAdapter — Class E (traffic isolation) state adapter.

The discriminator is ``Q = span_count_in_window / mean(span_count_per_baseline_subwindow)``.
The healthy distribution of ``Q`` is computed by sliding sub-windows of the
same length as the abnormal window over the case's baseline traces, with
stride ``bucket_seconds``; the abnormal ``Q`` is the same ratio computed
once for the abnormal window itself.

Per-service detection threshold is computed by
:func:`baseline_calibrator.calibrate_quantile_threshold` with ``tail="lower"``,
so the per-(svc, case) false-positive rate is bounded by ``alpha`` per §11.2.
Emission is service-level only — span-level "this endpoint went quiet" is
too noisy to be useful per §3.E and is intentionally not produced here.
When the calibrator returns ``opt_out=True`` (insufficient or unstable
baseline), the service produces no transition for this case.
"""

from __future__ import annotations

from collections.abc import Iterable

import polars as pl

from rcabench_platform.v3.internal.reasoning.algorithms.baseline_calibrator import (
    calibrate_quantile_threshold,
)
from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.traces import _ts_seconds
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

__all__ = ["TraceVolumeAdapter"]

DEFAULT_ALPHA = 0.01
DEFAULT_BOOTSTRAP_N = 200
DEFAULT_BUCKET_SECONDS = 5


class TraceVolumeAdapter:
    """Per-service span-rate drop detector for Class E (traffic isolation)."""

    name = "trace_volume"

    def __init__(
        self,
        baseline_traces: pl.DataFrame,
        abnormal_traces: pl.DataFrame,
        *,
        abnormal_window_start: int,
        abnormal_window_end: int,
        alpha: float = DEFAULT_ALPHA,
        bootstrap_n: int = DEFAULT_BOOTSTRAP_N,
        bucket_seconds: int = DEFAULT_BUCKET_SECONDS,
        rng_seed: int | None = None,
    ) -> None:
        if abnormal_window_end <= abnormal_window_start:
            raise ValueError(
                "abnormal_window_end must be strictly greater than "
                f"abnormal_window_start (got start={abnormal_window_start}, "
                f"end={abnormal_window_end})"
            )
        self._baseline = baseline_traces
        self._abnormal = abnormal_traces
        self._abnormal_window_start = abnormal_window_start
        self._abnormal_window_end = abnormal_window_end
        self._abnormal_window_length = abnormal_window_end - abnormal_window_start
        self._alpha = alpha
        self._bootstrap_n = bootstrap_n
        self._bucket_seconds = bucket_seconds
        self._rng_seed = rng_seed

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        return list(self._emit_all())

    def _emit_all(self) -> Iterable[Transition]:
        if len(self._baseline) == 0:
            return

        baseline_ts = _ts_seconds(self._baseline)
        baseline_ts = baseline_ts.with_columns(pl.col("_ts").cast(pl.Int64))

        global_ts_min = baseline_ts.select(pl.col("_ts").min()).item()
        global_ts_max = baseline_ts.select(pl.col("_ts").max()).item()
        if global_ts_min is None or global_ts_max is None:
            return
        global_ts_min = int(global_ts_min)
        global_ts_max = int(global_ts_max)

        # If the global baseline span isn't long enough to fit even one full
        # subwindow of the abnormal length, no service can be calibrated.
        if global_ts_max - global_ts_min + 1 < self._abnormal_window_length:
            return

        # Pre-compute abnormal counts per service (one number each).
        if len(self._abnormal) > 0:
            abnormal_ts = _ts_seconds(self._abnormal).with_columns(
                pl.col("_ts").cast(pl.Int64)
            )
            abnormal_counts_df = (
                abnormal_ts.filter(
                    (pl.col("_ts") >= self._abnormal_window_start)
                    & (pl.col("_ts") < self._abnormal_window_end)
                )
                .group_by("service_name")
                .agg(pl.len().alias("abnormal_count"))
            )
            abnormal_counts: dict[str, int] = {
                row["service_name"]: int(row["abnormal_count"])
                for row in abnormal_counts_df.iter_rows(named=True)
            }
        else:
            abnormal_counts = {}

        # Per-service subwindow extraction. The service's empirical healthy
        # distribution of Q is anchored to *its own* baseline-span time range
        # (clamped to the global baseline window), so a service whose baseline
        # only lights up briefly produces few subwindows and falls into the
        # calibrator's opt-out regime by construction.
        for (service_name,), svc_df in baseline_ts.group_by("service_name"):
            svc_name = str(service_name)
            svc_ts_min = svc_df.select(pl.col("_ts").min()).item()
            svc_ts_max = svc_df.select(pl.col("_ts").max()).item()
            if svc_ts_min is None or svc_ts_max is None:
                continue
            svc_ts_min = max(int(svc_ts_min), global_ts_min)
            svc_ts_max = min(int(svc_ts_max), global_ts_max)
            counts = self._subwindow_counts(svc_df, svc_ts_min, svc_ts_max)
            if len(counts) < 2:
                continue
            mean_count = sum(counts) / len(counts)
            if mean_count <= 0:
                continue
            q_baseline = [c / mean_count for c in counts]

            result = calibrate_quantile_threshold(
                q_baseline,
                alpha=self._alpha,
                tail="lower",
                bootstrap_n=self._bootstrap_n,
                rng_seed=self._rng_seed,
            )
            if result.opt_out or result.threshold is None:
                continue

            abnormal_count = abnormal_counts.get(svc_name, 0)
            q_abnormal = abnormal_count / mean_count
            if q_abnormal >= result.threshold:
                continue

            node_key = f"service|{svc_name}"
            yield Transition(
                node_key=node_key,
                kind=PlaceKind.service,
                at=self._abnormal_window_start,
                from_state="healthy",
                to_state="silent",
                trigger="trace_volume_drop",
                level=EvidenceLevel.observed,
                evidence={
                    "trigger_metric": "trace_volume_drop",
                    "observed": q_abnormal,
                    "threshold": result.threshold,
                    "specialization_labels": frozenset({"silent_class_e"}),
                },
            )

    def _subwindow_counts(
        self,
        svc_df: pl.DataFrame,
        ts_min: int,
        ts_max: int,
    ) -> list[int]:
        """Counts of this service's spans inside each sliding subwindow.

        Subwindow length = ``self._abnormal_window_length``;
        stride = ``self._bucket_seconds``;
        each subwindow is ``[start, start + length)`` and must fully fit
        inside ``[ts_min, ts_max + 1)`` (the +1 mirrors the inclusive-max
        seen in baseline timestamps).
        """
        counts: list[int] = []
        last_start = ts_max + 1 - self._abnormal_window_length
        if last_start < ts_min:
            return counts
        ts_col = svc_df.select(pl.col("_ts")).to_series()
        # Materialise once; per-window filtering is cheap on a small Series.
        for w_start in range(ts_min, last_start + 1, self._bucket_seconds):
            w_end = w_start + self._abnormal_window_length
            mask = (ts_col >= w_start) & (ts_col < w_end)
            counts.append(int(mask.sum()))
        return counts
