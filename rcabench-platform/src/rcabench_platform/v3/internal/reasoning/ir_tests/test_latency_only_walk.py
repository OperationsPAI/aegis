"""Regression: manifest layer admits latency-only cascades (no errors).

The JVMMemoryStress forensic audit raised the hypothesis that
``ManifestAwarePathBuilder`` / ``ManifestLayerGate`` couples the
``layer.expected_features`` entries with AND semantics (i.e. requires
both ``latency_p99_ratio`` AND ``error_rate`` to match), and that
33/50 cases with elevated downstream p99 but zero error rate were
rejected for that reason.

That hypothesis is **wrong**: both the in-build admission predicate
(``manifest_path_builder._dst_features_match``) and the defensive
post-filter gate (``manifest_layer._node_matches_any_expected``)
already iterate the layer's expected_features and admit on the first
match — strict OR semantics. SCHEMA.md "expected_features" calls
this out explicitly: "admit iff ≥1 expected feature matches".

This test pins the contract: a span node with elevated p99 but zero
error_rate is admitted by a layer that lists both as expected
features.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_layer import (
    _node_matches_any_expected,
)
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.features import (
    Feature,
    FeatureKind,
)
from rcabench_platform.v3.internal.reasoning.manifests.schema import FeatureMatch


def test_layer_admits_latency_only_node() -> None:
    """A node with `latency_p99_ratio=7.87` and `error_rate=0.0` must be
    admitted by a layer whose ``expected_features`` lists both — OR,
    not AND.
    """
    node_id = 42
    feature_samples = {
        (node_id, FeatureKind.span, Feature.latency_p99_ratio): 7.87,
        (node_id, FeatureKind.span, Feature.error_rate): 0.0,
    }
    rctx = ReasoningContext(feature_samples=feature_samples)

    expected = [
        FeatureMatch(
            kind=FeatureKind.span,
            feature=Feature.latency_p99_ratio,
            band=(1.5, float("inf")),
        ),
        FeatureMatch(
            kind=FeatureKind.span,
            feature=Feature.error_rate,
            band=(0.05, 1.0),
        ),
    ]

    matched, evidence = _node_matches_any_expected(node_id, expected, rctx)
    assert matched, f"latency-only node was rejected; evidence: {evidence}"
    # Specifically, the latency entry should be the matching one and the
    # error_rate entry should report not-matched (0.0 < 0.05 lower bound).
    by_feature = {e["feature"]: e for e in evidence}
    assert by_feature["latency_p99_ratio"]["matched"] is True
    assert by_feature["error_rate"]["matched"] is False


def test_layer_rejects_when_no_expected_feature_matches() -> None:
    """Sanity polarity: if neither expected feature matches the node is
    not admitted (the OR semantics still excludes total non-matches)."""
    node_id = 99
    feature_samples = {
        (node_id, FeatureKind.span, Feature.latency_p99_ratio): 1.0,  # below band
        (node_id, FeatureKind.span, Feature.error_rate): 0.0,  # below band
    }
    rctx = ReasoningContext(feature_samples=feature_samples)
    expected = [
        FeatureMatch(
            kind=FeatureKind.span,
            feature=Feature.latency_p99_ratio,
            band=(1.5, float("inf")),
        ),
        FeatureMatch(
            kind=FeatureKind.span,
            feature=Feature.error_rate,
            band=(0.05, 1.0),
        ),
    ]
    matched, _ = _node_matches_any_expected(node_id, expected, rctx)
    assert not matched
