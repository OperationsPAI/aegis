"""Regression: pod-name drift between injection.json and parquet.

The JVMMemoryStress forensic audit raised the hypothesis that
``injection.json::ground_truth.pod`` (the pod hash captured at
injection time, e.g. ``ts-auth-service-77d85c69dd-sv9d4``) drifts away
from the actual pod hash in the chaos-window parquet (e.g. the
post-restart ``-rk2h5``), and that FORGE silently joins on the stale
hash and loses every k8s metric.

That hypothesis is **wrong**: the reasoning code never joins on the
GT pod hash. Pod nodes are constructed from
``parquet_loader._extract_resources``'s scan of ``attr.k8s.pod.name``
in the parquet itself (parquet_loader.py:1224), so feature extraction
operates on whatever pod hash the parquet actually carries. The GT
pod string is a fallback only used by ``InjectionNodeResolver`` for
``pod_lifecycle`` faults (PodKill/PodFailure/ContainerKill), and
even there a partial-match shim handles drift
(injection.py:622-625).

This test pins the contract: a synthetic graph whose pod node carries
a different hash from the GT pod string still has its k8s metrics
exposed via feature_samples, because feature extraction iterates
``graph.get_nodes_by_kind(PlaceKind.pod)`` and consults each node's
``abnormal_metrics`` map directly.
"""

from __future__ import annotations

import numpy as np

from rcabench_platform.v3.internal.reasoning.manifests.extractors.feature_extractor import (
    extract_feature_samples,
)
from rcabench_platform.v3.internal.reasoning.manifests.features import (
    Feature,
    FeatureKind,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    HyperGraph,
    Node,
    PlaceKind,
)


def test_pod_metrics_extracted_despite_pod_name_drift() -> None:
    """Pod feature_samples come from the pod node's metric maps, not from
    a join against ``injection.json::ground_truth.pod``.

    Ground-truth pod string: ``ts-auth-service-77d85c69dd-sv9d4`` (old
    hash, captured pre-chaos). Parquet/graph pod: ``-rk2h5`` (new hash,
    after pod restart). The extractor must still produce the
    cpu_throttle_ratio sample on the new-hash pod.
    """
    g = HyperGraph()
    pod_after_restart = "ts-auth-service-77d85c69dd-rk2h5"
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name=pod_after_restart))

    # CPU saturation series — peak well into the manifest's [3.0, inf) band.
    ts = np.arange(1000, 1060, 5, dtype=np.int64)
    abn_vals = np.full(len(ts), 0.85, dtype=float)  # 85% cgroup saturation
    base_vals = np.full(len(ts), 0.05, dtype=float)
    pod.abnormal_metrics["k8s.pod.cpu_limit_utilization"] = (ts, abn_vals)
    pod.baseline_metrics["k8s.pod.cpu_limit_utilization"] = (ts, base_vals)

    # Memory saturation series — within JVMMemoryStress entry band.
    abn_mem = np.full(len(ts), 0.82, dtype=float)
    pod.abnormal_metrics["k8s.pod.memory_limit_utilization"] = (ts, abn_mem)

    samples = extract_feature_samples(
        graph=g,
        baseline_traces=None,
        abnormal_traces=None,
        abnormal_window_start=int(ts[0]),
        abnormal_window_end=int(ts[-1]) + 1,
    )

    assert pod.id is not None
    cpu_key = (pod.id, FeatureKind.pod, Feature.cpu_throttle_ratio)
    mem_key = (pod.id, FeatureKind.pod, Feature.memory_usage_ratio)

    assert cpu_key in samples, (
        "cpu_throttle_ratio missing — extractor failed to attach metric to the post-drift pod node"
    )
    assert samples[cpu_key] >= 3.0, f"cpu_throttle_ratio={samples[cpu_key]} below manifest band lower bound"
    assert mem_key in samples
    assert 0.5 <= samples[mem_key] <= 1.0
