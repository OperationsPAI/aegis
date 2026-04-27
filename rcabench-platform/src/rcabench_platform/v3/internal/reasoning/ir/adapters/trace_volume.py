"""TraceVolumeAdapter — Class E (traffic isolation) state adapter.

The discriminator is a per-second rate
``Q = span_count_in_subwindow / subwindow_seconds`` (per §11.2 step 1 + 2).
Slicing is done on **fixed-length** subwindows of ``subwindow_seconds``
(default 30s per §11.4 — locked methodology decision, not an arbitrary
constant) with stride ``bucket_seconds``; the subwindow length is
**decoupled from the abnormal-window length** because real datasets
commonly use ``pre_duration ≈ duration``, and tying the two would
collapse the baseline distribution to a single sample (calibrator
opt-out → no SILENT emitted).

Per-(svc, case) calibration:

- The baseline is sliced into ``subwindow_seconds``-long sliding
  subwindows; per-second rates form the empirical healthy distribution
  of ``Q``.
- :func:`baseline_calibrator.calibrate_quantile_threshold` with
  ``tail="lower"`` produces a lower-tail threshold whose per-(svc, case)
  false-positive rate is bounded by ``alpha``.
- The abnormal window is sliced into the same fixed-length subwindows
  per service, and the abnormal aggregate is the *mean* of those
  per-second rates (§11.2 step 5). If the abnormal window is shorter
  than ``subwindow_seconds`` we fall back to a single sample
  ``abnormal_count / abnormal_window_length`` so legitimately short
  abnormals still produce one rate sample.
- When the calibrator returns ``opt_out=True`` (insufficient or unstable
  baseline) the service produces no transition for this case.

Emission is service-level only — span-level "this endpoint went quiet"
is too noisy to be useful per §3.E and is intentionally not produced
here.
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
DEFAULT_SUBWINDOW_SECONDS = 30


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
        subwindow_seconds: int = DEFAULT_SUBWINDOW_SECONDS,
        rng_seed: int | None = None,
    ) -> None:
        if abnormal_window_end <= abnormal_window_start:
            raise ValueError(
                "abnormal_window_end must be strictly greater than "
                f"abnormal_window_start (got start={abnormal_window_start}, "
                f"end={abnormal_window_end})"
            )
        if subwindow_seconds < 2 * bucket_seconds:
            raise ValueError(
                "subwindow_seconds must be at least 2 * bucket_seconds so "
                "more than one bucket-stride fits inside each subwindow "
                f"(got subwindow_seconds={subwindow_seconds}, "
                f"bucket_seconds={bucket_seconds})"
            )
        self._baseline = baseline_traces
        self._abnormal = abnormal_traces
        self._abnormal_window_start = abnormal_window_start
        self._abnormal_window_end = abnormal_window_end
        self._abnormal_window_length = abnormal_window_end - abnormal_window_start
        self._alpha = alpha
        self._bootstrap_n = bootstrap_n
        self._bucket_seconds = bucket_seconds
        self._subwindow_seconds = subwindow_seconds
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

        # The global baseline must be long enough to admit at least one
        # full subwindow.
        if global_ts_max - global_ts_min + 1 < self._subwindow_seconds:
            return

        # Pre-compute abnormal timestamps once for per-service slicing
        # below. Abnormal slicing happens per-service inside the loop —
        # there is no global per-service count aggregation up front.
        if len(self._abnormal) > 0:
            abnormal_ts = _ts_seconds(self._abnormal).with_columns(pl.col("_ts").cast(pl.Int64))
        else:
            abnormal_ts = None

        # Per-service slicing. The service's empirical healthy distribution
        # of Q is anchored to *its own* baseline-span time range (clamped
        # to the global baseline window), so a service whose baseline only
        # lights up briefly produces few subwindows and falls into the
        # calibrator's opt-out regime by construction.
        for (service_name,), svc_df in baseline_ts.group_by("service_name"):
            svc_name = str(service_name)
            svc_ts_min = svc_df.select(pl.col("_ts").min()).item()
            svc_ts_max = svc_df.select(pl.col("_ts").max()).item()
            if svc_ts_min is None or svc_ts_max is None:
                continue
            svc_ts_min = max(int(svc_ts_min), global_ts_min)
            svc_ts_max = min(int(svc_ts_max), global_ts_max)
            baseline_rates = self._subwindow_rates(svc_df, svc_ts_min, svc_ts_max)
            if len(baseline_rates) < 2:
                # Calibrator would opt-out on <2 samples anyway, but skip
                # eagerly to avoid the bootstrap call.
                continue

            result = calibrate_quantile_threshold(
                baseline_rates,
                alpha=self._alpha,
                tail="lower",
                bootstrap_n=self._bootstrap_n,
                rng_seed=self._rng_seed,
            )
            if result.opt_out or result.threshold is None:
                continue

            # Slice the abnormal window into the same fixed-length
            # subwindows for this service. Anchor to the abnormal-window
            # bounds (not the service's own baseline range) so a service
            # that disappears entirely from the abnormal window still gets
            # rate samples (zeros), letting the lower-tail threshold fire.
            q_abnormal = self._abnormal_rate_aggregate(abnormal_ts, svc_name)

            if q_abnormal >= result.threshold:
                continue

            # Two-step decision (L6e):
            #
            #   - The aggregate test (mean of abnormal sub-window rates vs
            #     T(svc, case)) decides *whether* to emit a SILENT
            #     transition for this service. The α-bound on per-(svc,
            #     case) FP from §11.2 step 5 is preserved by aggregate
            #     testing only.
            #   - Once the aggregate fires, *when* the transition occurred
            #     is taken from the first abnormal sub-window whose
            #     per-second rate falls below T(svc, case). This is a
            #     ranking signal for downstream consumers (e.g. the
            #     inferred-edges silent gate, which ranks Class E silent
            #     services by their first-silent timestamp to keep only
            #     the earliest cohort as root-cause candidates per §6.1)
            #     and does NOT change the FP guarantee.
            #
            # Edge cases:
            #   A. aggregate fires but no individual sub-window is fully
            #      below threshold (sub-window rates straddle T but mean
            #      is below) → fall back to abnormal_window_start.
            #   B. abnormal_window_length < subwindow_seconds (the
            #      single-sample fallback path) → no sub-window
            #      granularity is available, fall back to
            #      abnormal_window_start.
            transition_at = self._first_below_threshold_at(
                abnormal_ts,
                svc_name,
                result.threshold,
            )

            node_key = f"service|{svc_name}"
            yield Transition(
                node_key=node_key,
                kind=PlaceKind.service,
                at=transition_at,
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

    def _abnormal_rate_aggregate(
        self,
        abnormal_ts: pl.DataFrame | None,
        svc_name: str,
    ) -> float:
        """Return Q_abnormal for ``svc_name`` per §11.2 step 5.

        Slice the abnormal window into ``subwindow_seconds``-long
        subwindows (stride = ``bucket_seconds``) and return the mean
        per-second rate across them. If the abnormal window itself is
        shorter than ``subwindow_seconds`` we fall back to a single
        sample ``abnormal_count / abnormal_window_length`` — without the
        fallback a legitimately short abnormal would yield zero
        sub-windows and silently skip detection.
        """
        abn_ts_min = self._abnormal_window_start
        abn_ts_max = self._abnormal_window_end - 1  # inclusive convention

        if self._abnormal_window_length < self._subwindow_seconds:
            # Edge case: abnormal_window_length < subwindow_seconds.
            # Fall back to one aggregate rate over the whole abnormal
            # range. We still test it against the baseline-derived
            # threshold; the rate units are comparable since both are
            # per-second.
            if abnormal_ts is None:
                count = 0
            else:
                ts_col = abnormal_ts.filter(pl.col("service_name") == svc_name).select(pl.col("_ts")).to_series()
                mask = (ts_col >= abn_ts_min) & (ts_col <= abn_ts_max)
                count = int(mask.sum())
            return float(count) / float(self._abnormal_window_length)

        if abnormal_ts is None:
            svc_abn_df = pl.DataFrame(
                schema={
                    "_ts": pl.Int64,
                    "service_name": pl.Utf8,
                }
            )
        else:
            svc_abn_df = abnormal_ts.filter(pl.col("service_name") == svc_name)

        rates = self._subwindow_rates(svc_abn_df, abn_ts_min, abn_ts_max)
        if not rates:
            # Abnormal range is at least one subwindow long but yielded no
            # subwindows for this service — treat as zero.
            return 0.0
        return sum(rates) / len(rates)

    def _first_below_threshold_at(
        self,
        abnormal_ts: pl.DataFrame | None,
        svc_name: str,
        threshold: float,
    ) -> int:
        """Return the start timestamp of the first abnormal sub-window
        whose per-second rate is strictly below ``threshold``.

        Falls back to ``self._abnormal_window_start`` in two cases:

        - **Edge case B:** ``abnormal_window_length < subwindow_seconds``
          — no sub-window granularity available, the aggregate path used
          a single-sample fallback so there's no per-bucket timeline.
        - **Edge case A:** the aggregate test fired but no individual
          sub-window is fully below threshold (rare; happens when
          per-bucket rates straddle ``threshold`` while their mean sits
          below it).

        Per §11.2 step 5: the aggregate test owns *whether* the service
        emits SILENT (and thus the per-(svc, case) α-bounded FP rate);
        this helper only decides *when* the transition occurred for
        downstream causal-ordering consumers.
        """
        if self._abnormal_window_length < self._subwindow_seconds:
            return self._abnormal_window_start

        abn_ts_min = self._abnormal_window_start
        abn_ts_max = self._abnormal_window_end - 1  # inclusive convention

        if abnormal_ts is None:
            svc_abn_df = pl.DataFrame(
                schema={
                    "_ts": pl.Int64,
                    "service_name": pl.Utf8,
                }
            )
        else:
            svc_abn_df = abnormal_ts.filter(pl.col("service_name") == svc_name)

        last_start = abn_ts_max + 1 - self._subwindow_seconds
        if last_start < abn_ts_min:
            return self._abnormal_window_start

        ts_col = svc_abn_df.select(pl.col("_ts")).to_series()
        for w_start in range(abn_ts_min, last_start + 1, self._bucket_seconds):
            w_end = w_start + self._subwindow_seconds
            mask = (ts_col >= w_start) & (ts_col < w_end)
            rate = float(int(mask.sum())) / float(self._subwindow_seconds)
            if rate < threshold:
                return w_start
        # Edge case A: aggregate fires but no full sub-window crosses.
        return self._abnormal_window_start

    def _subwindow_rates(
        self,
        svc_df: pl.DataFrame,
        ts_min: int,
        ts_max: int,
    ) -> list[float]:
        """Per-second rates of this service's spans inside each fixed-length sliding subwindow.

        Subwindow length = ``self._subwindow_seconds``;
        stride = ``self._bucket_seconds``;
        each subwindow is ``[start, start + length)`` and must fully fit
        inside ``[ts_min, ts_max + 1)``.

        Returns a list of per-second rates (count / subwindow_seconds).
        """
        rates: list[float] = []
        last_start = ts_max + 1 - self._subwindow_seconds
        if last_start < ts_min:
            return rates
        ts_col = svc_df.select(pl.col("_ts")).to_series()
        for w_start in range(ts_min, last_start + 1, self._bucket_seconds):
            w_end = w_start + self._subwindow_seconds
            mask = (ts_col >= w_start) & (ts_col < w_end)
            rates.append(float(int(mask.sum())) / self._subwindow_seconds)
        return rates
