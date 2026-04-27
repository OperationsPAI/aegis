"""One-class baseline-quantile calibrator (reasoning-feature-taxonomy §11.2).

This module implements the reusable, pure-function piece of the §11.2
"Adapter threshold methodology": every adapter migrating to the new
methodology computes its per-(svc, case) detection threshold by calling
:func:`calibrate_quantile_threshold` on a baseline distribution of its
discriminator ``Q``.

The calibrator is one-class: **no labeled fault data is needed** to set
the threshold. Calibration treats the case's own baseline window as the
healthy distribution and bounds the per-(svc, case) false-positive rate
by ``alpha`` by construction.

Two failure modes are reported via ``opt_out``:

- ``"empty"`` — fewer than two baseline samples; no quantile or
  bootstrap is meaningful.
- ``"unstable"`` — the quantile estimate's bootstrap relative standard
  deviation exceeds ``stability_rel_std_max`` (default 0.10), so the
  baseline is too sparse / too wide-tailed to support a stable
  threshold and the service opts out of detection for this case.

Stability is judged against the data's natural scale (IQR), not the
quantile's own magnitude — see §11.2 step 3.

Tail directions:

- ``tail="lower"`` is for "value too low" detectors such as a
  SILENT-style trace-volume drop (``Q = rate(abnormal) / mean(baseline)``
  falling below the lower tail of its baseline distribution).
- ``tail="upper"`` is for "value too high" detectors such as SLOW (span
  latency) or error-rate adapters whose ``Q`` rises above the upper
  tail of its baseline distribution.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from typing import Literal

import numpy as np

__all__ = ["CalibrationResult", "calibrate_quantile_threshold"]


@dataclass(frozen=True, slots=True)
class CalibrationResult:
    """Outcome of one §11.2 calibration call.

    ``threshold`` is ``None`` iff ``opt_out`` is ``True``.
    ``bootstrap_rel_std`` is ``None`` only when ``opt_out_reason == "empty"``
    (with fewer than two samples no bootstrap is computed).
    """

    threshold: float | None
    opt_out: bool
    opt_out_reason: str | None  # "empty" | "unstable" | None
    baseline_n: int
    bootstrap_rel_std: float | None
    alpha: float
    tail: Literal["lower", "upper"]


def calibrate_quantile_threshold(
    baseline_values: Iterable[float],
    *,
    alpha: float = 0.01,
    tail: Literal["lower", "upper"] = "lower",
    bootstrap_n: int = 200,
    stability_rel_std_max: float = 0.10,
    rng_seed: int | None = None,
) -> CalibrationResult:
    """Calibrate a one-class baseline-quantile threshold per §11.2.

    Parameters
    ----------
    baseline_values:
        Empirical healthy distribution of the discriminator ``Q`` for one
        (service, case) pair. Materialised into a float64 array.
    alpha:
        Per-(svc, case) false-positive budget. ``tail="lower"`` uses
        ``q_target = alpha``; ``tail="upper"`` uses ``q_target = 1 - alpha``.
    tail:
        ``"lower"`` for "Q below threshold is anomalous" detectors (e.g.
        trace-volume drop). ``"upper"`` for "Q above threshold is anomalous"
        detectors (e.g. latency / error-rate spikes).
    bootstrap_n:
        Number of bootstrap resamples used to assess stability of the
        quantile estimate.
    stability_rel_std_max:
        If the bootstrap relative standard deviation of the quantile
        estimate exceeds this bound, the service opts out for this case.
    rng_seed:
        Seed for ``numpy.random.default_rng``. Same seed + same input
        yields identical results.
    """

    arr = np.asarray(list(baseline_values), dtype=np.float64)
    n = int(arr.size)

    if n < 2:
        return CalibrationResult(
            threshold=None,
            opt_out=True,
            opt_out_reason="empty",
            baseline_n=n,
            bootstrap_rel_std=None,
            alpha=alpha,
            tail=tail,
        )

    q_target = alpha if tail == "lower" else 1.0 - alpha
    threshold = float(np.quantile(arr, q_target))

    rng = np.random.default_rng(rng_seed)
    # Each resample has the same size as the input; quantile computed per row.
    resamples = rng.choice(arr, size=(bootstrap_n, n), replace=True)
    bootstrap_quantiles = np.quantile(resamples, q_target, axis=1)

    iqr = float(np.quantile(arr, 0.75) - np.quantile(arr, 0.25))
    denom = max(iqr, 1e-9)
    std_q = float(np.std(bootstrap_quantiles, ddof=1))
    rel_std = std_q / denom

    if rel_std > stability_rel_std_max:
        return CalibrationResult(
            threshold=None,
            opt_out=True,
            opt_out_reason="unstable",
            baseline_n=n,
            bootstrap_rel_std=rel_std,
            alpha=alpha,
            tail=tail,
        )

    return CalibrationResult(
        threshold=threshold,
        opt_out=False,
        opt_out_reason=None,
        baseline_n=n,
        bootstrap_rel_std=rel_std,
        alpha=alpha,
        tail=tail,
    )
