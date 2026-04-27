"""Resolver behavior for network faults with multi-service ground truth.

A NetworkPartition cuts traffic between two services and the GT lists both;
returning only one endpoint strands the corridor's forward BFS on whichever
side has fewer outgoing edges. The resolver therefore returns *all* GT
services that exist in the graph when GT is multi-service. Single-service
GT (typical of NetworkDelay/Loss/Bandwidth) keeps the existing
source-perceives-fault resolution.
"""

from __future__ import annotations

import json

from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import InjectionNodeResolver


def _graph_with(services: list[str]) -> HyperGraph:
    g = HyperGraph()
    for svc in services:
        g.add_node(Node(kind=PlaceKind.service, self_name=svc))
    return g


def _injection(
    fault_type_name: str,
    source: str,
    target: str,
    *,
    gt: list[str] | None = None,
    direction: str = "from",
) -> dict:
    return {
        "fault_type": fault_type_name,
        "display_config": json.dumps(
            {
                "injection_point": {
                    "source_service": source,
                    "target_service": target,
                },
                "direction": direction,
            }
        ),
        "ground_truth": {"service": gt if gt is not None else [source, target]},
    }


def test_partition_returns_all_gt_services_when_all_in_graph() -> None:
    g = _graph_with(["ts-verification-code-service", "ts-ui-dashboard"])
    resolver = InjectionNodeResolver(g, jvm_method_mapping=None)
    resolved = resolver.resolve(_injection("NetworkPartition", "ts-verification-code-service", "ts-ui-dashboard"))
    assert set(resolved.injection_nodes) == {
        "service|ts-verification-code-service",
        "service|ts-ui-dashboard",
    }
    assert resolved.resolution_method.startswith("network_ground_truth_")


def test_partition_falls_back_to_source_when_only_one_gt_in_graph() -> None:
    g = _graph_with(["ts-verification-code-service"])
    resolver = InjectionNodeResolver(g, jvm_method_mapping=None)
    resolved = resolver.resolve(_injection("NetworkPartition", "ts-verification-code-service", "ts-missing"))
    assert resolved.injection_nodes == ["service|ts-verification-code-service"]
    assert resolved.resolution_method.startswith("network_source_")


def test_single_gt_keeps_source_resolution() -> None:
    g = _graph_with(["ts-a", "ts-b"])
    resolver = InjectionNodeResolver(g, jvm_method_mapping=None)
    resolved = resolver.resolve(_injection("NetworkDelay", "ts-a", "ts-b", gt=["ts-a"]))
    assert resolved.injection_nodes == ["service|ts-a"]
    assert resolved.resolution_method.startswith("network_source_")
