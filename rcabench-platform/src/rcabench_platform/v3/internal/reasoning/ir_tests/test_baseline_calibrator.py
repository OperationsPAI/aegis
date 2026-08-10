"""Tests for the §11.2 baseline-quantile calibrator."""

from __future__ import annotations

import numpy as np

from rcabench_platform.v3.internal.reasoning.algorithms.baseline_calibrator import (
    calibrate_quantile_threshold,
)


def test_lower_tail_uniform_distribution() -> None:
    rng = np.random.default_rng(42)
    samples = rng.uniform(0.0, 1.0, size=1000).tolist()
    result = calibrate_quantile_threshold(samples, alpha=0.01, tail="lower", rng_seed=42)
    assert result.opt_out is False
    assert result.threshold is not None
    assert 0.0 <= result.threshold <= 0.05


def test_upper_tail_uniform_distribution() -> None:
    rng = np.random.default_rng(42)
    samples = rng.uniform(0.0, 1.0, size=1000).tolist()
    result = calibrate_quantile_threshold(samples, alpha=0.01, tail="upper", rng_seed=42)
    assert result.opt_out is False
    assert result.threshold is not None
    assert 0.95 <= result.threshold <= 1.0


def test_lower_vs_upper_complementary_on_same_data() -> None:
    rng = np.random.default_rng(123)
    samples = rng.uniform(0.0, 1.0, size=1000).tolist()
    lower = calibrate_quantile_threshold(samples, alpha=0.10, tail="lower", rng_seed=1)
    upper = calibrate_quantile_threshold(samples, alpha=0.10, tail="upper", rng_seed=1)
    assert lower.opt_out is False
    assert upper.opt_out is False
    assert lower.threshold is not None
    assert upper.threshold is not None
    assert lower.threshold < upper.threshold


def test_opt_out_on_empty() -> None:
    result = calibrate_quantile_threshold([], alpha=0.01, tail="lower")
    assert result.opt_out is True
    assert result.opt_out_reason == "empty"
    assert result.baseline_n == 0
    assert result.bootstrap_rel_std is None
    assert result.threshold is None


def test_opt_out_on_single_value() -> None:
    result = calibrate_quantile_threshold([3.14], alpha=0.01, tail="lower")
    assert result.opt_out is True
    assert result.opt_out_reason == "empty"
    assert result.baseline_n == 1
    assert result.bootstrap_rel_std is None
    assert result.threshold is None


def test_opt_out_on_unstable_small_sample() -> None:
    # N=4 with a wide value spread. Under IQR-scaled rel_std, very small
    # N makes the bootstrap quantile estimate's std a large fraction of
    # the input IQR. Verified: rng_seed=0 -> rel_std ~= 0.225 for
    # tail="lower" with samples [1, 2, 5, 10].
    result = calibrate_quantile_threshold(
        [1.0, 2.0, 5.0, 10.0],
        alpha=0.01,
        tail="lower",
        rng_seed=0,
    )
    assert result.opt_out is True
    assert result.opt_out_reason == "unstable"
    assert result.bootstrap_rel_std is not None
    assert result.bootstrap_rel_std > 0.10
    assert result.threshold is None
    assert result.baseline_n == 4


def test_seeded_reproducibility() -> None:
    rng = np.random.default_rng(99)
    samples = rng.uniform(0.0, 1.0, size=500).tolist()
    a = calibrate_quantile_threshold(samples, alpha=0.05, tail="lower", rng_seed=7)
    b = calibrate_quantile_threshold(samples, alpha=0.05, tail="lower", rng_seed=7)
    assert a.threshold == b.threshold
    assert a.bootstrap_rel_std == b.bootstrap_rel_std
    assert a.opt_out == b.opt_out
    assert a.opt_out_reason == b.opt_out_reason


def test_returns_baseline_n() -> None:
    # opt_out="empty" path
    r0 = calibrate_quantile_threshold([], alpha=0.01, tail="lower")
    assert r0.baseline_n == 0
    r1 = calibrate_quantile_threshold([2.0], alpha=0.01, tail="lower")
    assert r1.baseline_n == 1
    # opt_out="unstable" path (N=4 wide spread, see test_opt_out_on_unstable_small_sample)
    r2 = calibrate_quantile_threshold([1.0, 2.0, 5.0, 10.0], alpha=0.01, tail="lower", rng_seed=0)
    assert r2.baseline_n == 4
    # successful path
    rng = np.random.default_rng(11)
    s = rng.normal(0.0, 1.0, size=1000).tolist()
    r3 = calibrate_quantile_threshold(s, alpha=0.05, tail="lower", rng_seed=11)
    assert r3.baseline_n == 1000


def test_lower_tail_normal_distribution_threshold_in_expected_range() -> None:
    rng = np.random.default_rng(11)
    samples = rng.normal(0.0, 1.0, size=1000).tolist()
    result = calibrate_quantile_threshold(samples, alpha=0.05, tail="lower", rng_seed=11)
    assert result.opt_out is False
    assert result.threshold is not None
    assert -2.0 <= result.threshold <= -1.4


def test_lower_tail_near_zero_quantile_does_not_falsely_opt_out() -> None:
    # Regression test for the std/|mean| bug fixed in §11.2 step 3:
    # uniform[0,1] has q_0.01 ~= 0.01 (near-zero), but bootstrap std is
    # also small and IQR ~= 0.5, so rel_std stays well below 0.10.
    rng = np.random.default_rng(42)
    samples = rng.uniform(0.0, 1.0, size=1000).tolist()
    result = calibrate_quantile_threshold(samples, alpha=0.01, tail="lower", rng_seed=42)
    assert result.opt_out is False
    assert result.threshold is not None
    assert 0.0 <= result.threshold <= 0.05
