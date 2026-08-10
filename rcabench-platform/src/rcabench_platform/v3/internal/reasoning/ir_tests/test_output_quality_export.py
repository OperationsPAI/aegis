from __future__ import annotations

import json
from typing import Any

from rcabench_platform.v3.internal.reasoning import cli as reasoning_cli
from rcabench_platform.v3.internal.reasoning import runner as reasoning_runner
from rcabench_platform.v3.internal.reasoning.algorithms.starting_point_resolver import StartingPointResolver
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import (
    InjectionMetadata,
    InjectionNodeResolver,
    ResolvedInjection,
    ResolvedRootCandidate,
)
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationPath, PropagationResult
from rcabench_platform.v3.sdk.evaluation.causal_graph import CausalGraph, CausalNode


def _add_edge(g: HyperGraph, src: Node, dst: Node, kind: DepKind = DepKind.includes) -> None:
    assert src.id is not None
    assert dst.id is not None
    g.add_edge(
        Edge(
            src_id=src.id,
            dst_id=dst.id,
            src_name=src.uniq_name,
            dst_name=dst.uniq_name,
            kind=kind,
            data=None,
        )
    )


def _alarm_index(rows: list[dict]) -> dict:
    evidence_by_name = {reasoning_cli._ALARM_EVIDENCE_INDEX_KEY: reasoning_cli._new_alarm_index()}
    for row in rows:
        raw_name = row["SpanName"]
        evidence = reasoning_cli._classify_conclusion_alarm(row)
        evidence["conclusion_span_name"] = raw_name
        evidence_by_name[raw_name] = evidence
        normalized_name = reasoning_cli._normalize_conclusion_span_name(raw_name)
        evidence_by_name[normalized_name] = evidence
        reasoning_cli._append_alarm_index(
            evidence_by_name[reasoning_cli._ALARM_EVIDENCE_INDEX_KEY],
            reasoning_cli._parse_alarm_identity(raw_name),
            evidence,
        )
        if normalized_name != raw_name:
            reasoning_cli._append_alarm_index(
                evidence_by_name[reasoning_cli._ALARM_EVIDENCE_INDEX_KEY],
                reasoning_cli._parse_alarm_identity(normalized_name),
                evidence,
            )
    return evidence_by_name


def test_root_cause_export_reuses_stateful_graph_node() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="ts-root-service"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /api"))
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[root.id, alarm.id],
                states=[["degraded", "unavailable"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[123, 130],
            )
        ],
        visited_nodes={root.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm.id},
    )

    assert len(causal_graph.root_causes) == 1
    root_cause = causal_graph.root_causes[0]
    assert root_cause.timestamp == 123
    assert root_cause.state == frozenset({"degraded", "unavailable"})
    assert root_cause.state_resolution_reason is None

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)
    assert result.injection_states == ["unavailable"]
    assert result.injection_state_reasons == [None]


def test_hybrid_engine_config_falls_back_when_root_metadata_is_incomplete() -> None:
    g = HyperGraph()
    g.add_node(Node(kind=PlaceKind.service, self_name="currencyservice"))

    injection_json = {
        "fault_type": "hybrid",
        "engine_config": [
            {
                "chaos_type": "NetworkDelay",
                "target_service": "checkoutservice",
                "direction": "to",
            }
        ],
        "ground_truth": [{"service": ["currencyservice"]}],
    }

    metadata = InjectionMetadata.from_injection_json(injection_json)
    resolved = InjectionNodeResolver(g).resolve(injection_json)

    assert metadata.fault_type_name == "NetworkDelay"
    assert metadata.injection_point.source_service is None
    assert metadata.ground_truth_services == ["currencyservice"]
    assert resolved.fault_type_name == "NetworkDelay"
    assert resolved.category == "network"
    assert resolved.fault_category == "network"
    assert resolved.injection_nodes == ["service|currencyservice"]
    assert resolved.resolution_method == "fallback_to_internal_service"


class _FakeManifest:
    def __init__(self, multi_v_root: bool = False) -> None:
        self.multi_v_root = multi_v_root


class _FakeRegistry:
    def __init__(self, manifests: dict[str, _FakeManifest]) -> None:
        self.manifests = manifests

    def get(self, fault_type: str) -> _FakeManifest | None:
        return self.manifests.get(fault_type)


def _resolved_with_candidates(candidates: list[ResolvedRootCandidate]) -> ResolvedInjection:
    first = candidates[0]
    return ResolvedInjection(
        injection_nodes=list(dict.fromkeys(candidate.node for candidate in candidates)),
        start_kind=first.start_kind,
        category=first.category,
        fault_category=first.fault_category,
        fault_type_name=first.fault_type_name,
        resolution_method="hybrid-test",
        root_candidates=candidates,
    )


def test_hybrid_propagation_units_do_not_collapse_non_multi_manifest_roots() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    recommendation = g.add_node(Node(kind=PlaceKind.service, self_name="recommendationservice"))
    assert checkout.id is not None
    assert recommendation.id is not None

    candidates = [
        ResolvedRootCandidate(
            node=checkout.uniq_name,
            start_kind="service",
            category="container_resource",
            fault_category="container",
            fault_type_name="CPUStress",
            resolution_method="service_fallback_internal",
        ),
        ResolvedRootCandidate(
            node=recommendation.uniq_name,
            start_kind="service",
            category="container_resource",
            fault_category="container",
            fault_type_name="CPUStress",
            resolution_method="service_fallback_internal",
        ),
    ]

    units = reasoning_runner._build_propagation_units(
        graph=g,
        resolved=_resolved_with_candidates(candidates),
        registry=_FakeRegistry({"CPUStress": _FakeManifest(multi_v_root=False)}),
        rules=[],
        starting_resolver=StartingPointResolver(g),
    )

    assert len(units) == 2
    assert [unit.fault_type_name for unit in units] == ["CPUStress", "CPUStress"]
    assert [unit.starting_node_ids for unit in units] == [[checkout.id], [recommendation.id]]
    assert [unit.root_candidate_indices for unit in units] == [[0], [1]]


def test_merge_propagation_results_preserves_unit_root_states() -> None:
    merged = reasoning_runner._merge_propagation_results(
        [
            PropagationResult(
                injection_node_ids=[1],
                injection_states=["unavailable"],
                paths=[],
                visited_nodes={1},
                max_hops_reached=0,
                injection_state_reasons=[None],
            ),
            PropagationResult(
                injection_node_ids=[2],
                injection_states=["slow"],
                paths=[],
                visited_nodes={2},
                max_hops_reached=0,
                injection_state_reasons=[None],
            ),
        ],
        [1, 2],
    )

    assert merged.injection_states == ["unavailable", "slow"]
    assert merged.injection_state_reasons == [None, None]


def test_merge_propagation_results_preserves_repeated_node_occurrences() -> None:
    merged = reasoning_runner._merge_propagation_results(
        [
            PropagationResult(
                injection_node_ids=[42],
                injection_states=["unavailable"],
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                injection_state_reasons=[None],
            ),
            PropagationResult(
                injection_node_ids=[42],
                injection_states=["degraded"],
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                injection_state_reasons=[None],
            ),
        ],
        [42, 42],
    )

    assert merged.injection_states == ["unavailable", "degraded"]
    assert merged.injection_state_reasons == [None, None]


def test_network_multi_v_root_propagation_units_remain_grouped() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    currency = g.add_node(Node(kind=PlaceKind.service, self_name="currencyservice"))
    assert checkout.id is not None
    assert currency.id is not None

    candidates = [
        ResolvedRootCandidate(
            node=checkout.uniq_name,
            start_kind="service",
            category="network",
            fault_category="network",
            fault_type_name="NetworkDelay",
            resolution_method="network_ground_truth",
        ),
        ResolvedRootCandidate(
            node=currency.uniq_name,
            start_kind="service",
            category="network",
            fault_category="network",
            fault_type_name="NetworkDelay",
            resolution_method="network_ground_truth",
        ),
    ]

    units = reasoning_runner._build_propagation_units(
        graph=g,
        resolved=_resolved_with_candidates(candidates),
        registry=_FakeRegistry({"NetworkDelay": _FakeManifest(multi_v_root=True)}),
        rules=[],
        starting_resolver=StartingPointResolver(g),
    )

    assert len(units) == 1
    assert units[0].fault_type_name == "NetworkDelay"
    assert units[0].starting_node_ids == [checkout.id, currency.id]
    assert units[0].root_candidate_indices == [0, 1]


def test_network_root_candidates_keep_missing_ground_truth_endpoint() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert checkout.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "hybrid",
            "engine_config": [{"chaos_type": "NetworkDelay"}],
            "ground_truth": [
                {"service": ["checkoutservice", "missingservice"]},
                {"service": ["checkoutservice"]},
            ],
        }
    )
    first_leg = [candidate for candidate in resolved.root_candidates if candidate.root_group_id == 0]

    assert [candidate.node for candidate in first_leg] == [
        "service|checkoutservice",
        "service|missingservice",
    ]
    assert resolved.injection_nodes == ["service|checkoutservice"]


def test_network_missing_ground_truth_endpoint_order_does_not_leak_into_injection_nodes() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert checkout.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": json.dumps(
                {
                    "injection_point": {
                        "source_service": "unknown-source",
                        "target_service": "unknown-target",
                    },
                    "direction": "from",
                }
            ),
            "ground_truth": {"service": ["missingservice", "checkoutservice"]},
        }
    )

    assert resolved.injection_nodes == ["service|checkoutservice"]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|missingservice",
        "service|checkoutservice",
    ]


def test_network_root_candidates_add_physical_fallback_when_all_gt_missing() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert checkout.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": json.dumps(
                {
                    "injection_point": {
                        "source_service": "checkoutservice",
                        "target_service": "currencyservice",
                    },
                    "direction": "from",
                }
            ),
            "ground_truth": {"service": ["missing-a", "missing-b"]},
        }
    )

    assert resolved.injection_nodes == ["service|checkoutservice"]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|missing-a",
        "service|missing-b",
        "service|checkoutservice",
    ]


def test_network_partial_stale_gt_keeps_physical_fallback_candidate() -> None:
    g = HyperGraph()
    payment = g.add_node(Node(kind=PlaceKind.service, self_name="paymentservice"))
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert payment.id is not None
    assert checkout.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": json.dumps(
                {
                    "injection_point": {
                        "source_service": "paymentservice",
                        "target_service": "currencyservice",
                    },
                    "direction": "from",
                }
            ),
            "ground_truth": {"service": ["missingservice", "checkoutservice"]},
        }
    )

    assert resolved.injection_nodes == ["service|paymentservice"]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|missingservice",
        "service|checkoutservice",
        "service|paymentservice",
    ]


def test_network_all_missing_gt_keeps_both_physical_endpoints() -> None:
    g = HyperGraph()
    for service in ["paymentservice", "currencyservice"]:
        node = g.add_node(Node(kind=PlaceKind.service, self_name=service))
        assert node.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": json.dumps(
                {
                    "injection_point": {
                        "source_service": "paymentservice",
                        "target_service": "currencyservice",
                    },
                    "direction": "from",
                }
            ),
            "ground_truth": {"service": ["missing-a", "missing-b"]},
        }
    )

    assert resolved.injection_nodes == ["service|paymentservice", "service|currencyservice"]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|missing-a",
        "service|missing-b",
        "service|paymentservice",
        "service|currencyservice",
    ]


def test_network_target_only_stale_gt_keeps_physical_endpoint() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert checkout.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": {
                "injection_point": {"target_service": "checkoutservice"},
                "direction": "from",
            },
            "ground_truth": {"service": ["missing-a", "missing-b"]},
        }
    )

    assert resolved.injection_nodes == ["service|checkoutservice"]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|missing-a",
        "service|missing-b",
        "service|checkoutservice",
    ]


def test_network_graph_resolvable_stale_gt_keeps_physical_candidate() -> None:
    g = HyperGraph()
    for service in ["paymentservice", "checkoutservice", "currencyservice"]:
        node = g.add_node(Node(kind=PlaceKind.service, self_name=service))
        assert node.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": json.dumps(
                {
                    "injection_point": {
                        "source_service": "paymentservice",
                        "target_service": "currencyservice",
                    },
                    "direction": "from",
                }
            ),
            "ground_truth": {"service": ["checkoutservice", "currencyservice"]},
        }
    )

    assert resolved.injection_nodes == [
        "service|checkoutservice",
        "service|currencyservice",
        "service|paymentservice",
    ]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|checkoutservice",
        "service|currencyservice",
        "service|paymentservice",
    ]


def test_no_path_state_sync_exports_missing_network_root_placeholders() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert checkout.id is not None
    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "NetworkPartition",
            "display_config": json.dumps(
                {
                    "injection_point": {
                        "source_service": "missing-source",
                        "target_service": "missing-target",
                    },
                    "direction": "from",
                }
            ),
            "ground_truth": {"service": ["missing-a", "missing-b"]},
        }
    )
    result = PropagationResult(
        injection_node_ids=[],
        injection_states=[],
        paths=[],
        visited_nodes=set(),
        max_hops_reached=0,
    )

    reasoning_runner._sync_no_path_root_states_from_candidates(result, resolved=resolved, graph=g)

    assert resolved.injection_nodes == ["service|missing-a"]
    assert [candidate.node for candidate in resolved.root_candidates] == [
        "service|missing-a",
        "service|missing-b",
    ]
    assert result.injection_node_ids == [-1, -1]
    assert result.injection_states == ["unknown", "unknown"]
    assert result.injection_state_reasons == [
        "root_component_not_in_causal_graph",
        "root_component_not_in_causal_graph",
    ]
    assert [detail["component"] for detail in result.injection_state_details] == [
        "service|missing-a",
        "service|missing-b",
    ]


def test_hybrid_extra_ground_truth_without_engine_does_not_create_unknown_candidate() -> None:
    g = HyperGraph()
    a = g.add_node(Node(kind=PlaceKind.service, self_name="a"))
    b = g.add_node(Node(kind=PlaceKind.service, self_name="b"))
    assert a.id is not None
    assert b.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "hybrid",
            "engine_config": [{"chaos_type": "PodFailure"}],
            "ground_truth": [{"service": ["a"]}, {"service": ["b"]}],
        }
    )

    assert resolved.injection_nodes == ["service|a"]
    assert [candidate.node for candidate in resolved.root_candidates] == ["service|a"]
    assert {candidate.fault_type_name for candidate in resolved.root_candidates} == {"PodFailure"}


def test_hybrid_without_engine_entries_does_not_fabricate_unknown_legs() -> None:
    g = HyperGraph()
    a = g.add_node(Node(kind=PlaceKind.service, self_name="a"))
    b = g.add_node(Node(kind=PlaceKind.service, self_name="b"))
    assert a.id is not None
    assert b.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "hybrid",
            "engine_config": [],
            "ground_truth": [{"service": ["a"]}, {"service": ["b"]}],
        }
    )

    assert resolved.fault_type_name == "hybrid"
    assert resolved.injection_nodes == []
    assert resolved.root_candidates == []


def test_hybrid_empty_engine_entry_is_not_executable_and_preserves_gt_alignment() -> None:
    g = HyperGraph()
    for service in ["a", "b"]:
        node = g.add_node(Node(kind=PlaceKind.service, self_name=service))
        assert node.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "hybrid",
            "engine_config": [{}, {"chaos_type": "PodFailure"}],
            "ground_truth": [{"service": ["a"]}, {"service": ["b"]}],
        }
    )

    assert resolved.injection_nodes == ["service|b"]
    assert [candidate.node for candidate in resolved.root_candidates] == ["service|b"]
    assert [candidate.fault_type_name for candidate in resolved.root_candidates] == ["PodFailure"]
    assert [candidate.root_group_id for candidate in resolved.root_candidates] == [1]


def test_hybrid_non_dict_engine_hole_preserves_gt_alignment() -> None:
    g = HyperGraph()
    for service in ["a", "b"]:
        node = g.add_node(Node(kind=PlaceKind.service, self_name=service))
        assert node.id is not None

    resolved = InjectionNodeResolver(g).resolve(
        {
            "fault_type": "hybrid",
            "engine_config": [None, {"chaos_type": "PodFailure"}],
            "ground_truth": [{"service": ["a"]}, {"service": ["b"]}],
        }
    )

    assert resolved.injection_nodes == ["service|b"]
    assert [candidate.node for candidate in resolved.root_candidates] == ["service|b"]
    assert [candidate.root_group_id for candidate in resolved.root_candidates] == [1]


def test_repeated_network_hybrid_legs_keep_separate_root_groups() -> None:
    g = HyperGraph()
    services = [g.add_node(Node(kind=PlaceKind.service, self_name=name)) for name in ["a", "b", "c", "d"]]
    assert all(service.id is not None for service in services)

    candidates = [
        ResolvedRootCandidate(
            node=service.uniq_name,
            root_group_id=idx // 2,
            start_kind="service",
            category="network",
            fault_category="network",
            fault_type_name="NetworkDelay",
            resolution_method="network_ground_truth",
        )
        for idx, service in enumerate(services)
    ]

    units = reasoning_runner._build_propagation_units(
        graph=g,
        resolved=_resolved_with_candidates(candidates),
        registry=_FakeRegistry({"NetworkDelay": _FakeManifest(multi_v_root=True)}),
        rules=[],
        starting_resolver=StartingPointResolver(g),
    )

    assert len(units) == 2
    assert [unit.root_group_id for unit in units] == [0, 1]
    assert [unit.root_candidate_indices for unit in units] == [[0, 1], [2, 3]]
    assert [unit.starting_node_ids for unit in units] == [
        [services[0].id, services[1].id],
        [services[2].id, services[3].id],
    ]


def test_root_cause_unknown_state_uses_concrete_graph_evidence() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    alarm_a = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /cart"))
    alarm_b = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /checkout"))
    _add_edge(g, root, alarm_a)
    _add_edge(g, root, alarm_b)
    assert root.id is not None
    assert alarm_a.id is not None
    assert alarm_b.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[root.id, alarm_a.id],
                states=[["unknown"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[200, 201],
            ),
            PropagationPath(
                nodes=[root.id, alarm_b.id],
                states=[["degraded"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[220, 221],
            ),
        ],
        visited_nodes={root.id, alarm_a.id, alarm_b.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm_a.id, alarm_b.id},
    )

    root_cause = causal_graph.root_causes[0]
    assert root_cause.component == root.uniq_name
    assert root_cause.state == frozenset({"degraded"})
    assert root_cause.timestamp == 220
    assert root_cause.state_resolution_reason is None

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)
    assert result.injection_states == ["degraded"]
    assert result.injection_state_reasons == [None]


def test_multi_root_export_keeps_all_root_causes_and_fault_types() -> None:
    g = HyperGraph()
    pod_root = g.add_node(Node(kind=PlaceKind.service, self_name="geo"))
    stress_root = g.add_node(Node(kind=PlaceKind.service, self_name="search"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::HTTP /hotels"))
    _add_edge(g, pod_root, alarm)
    assert pod_root.id is not None
    assert stress_root.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[pod_root.id, stress_root.id],
        injection_states=["unknown", "unknown"],
        paths=[
            PropagationPath(
                nodes=[pod_root.id, alarm.id],
                states=[["unavailable"], ["erroring"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[100, 101],
            )
        ],
        visited_nodes={pod_root.id, stress_root.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=pod_root.uniq_name,
        injection_node_names=[pod_root.uniq_name, stress_root.uniq_name],
        root_fallback_states={stress_root.uniq_name: "degraded"},
        root_candidates=[
            {
                "node": pod_root.uniq_name,
                "fault_type_name": "PodFailure",
                "resolution_method": "service_fallback_internal",
            },
            {
                "node": stress_root.uniq_name,
                "fault_type_name": "CPUStress",
                "resolution_method": "service_fallback_internal",
                "expected_state": "degraded",
            },
        ],
        alarm_node_ids={alarm.id},
    )
    enriched = reasoning_cli._causal_graph_with_export_metadata(
        causal_graph,
        case_name="synthetic-hybrid-root",
        result=result,
        alarm_accounting={
            "candidate_alarm_nodes": [],
            "explained_alarm_nodes": [],
            "unexplained_alarm_nodes": [],
            "candidate_alarm_count": 0,
            "explained_alarm_count": 0,
            "unexplained_alarm_count": 0,
        },
        resolution_info={
            "fault_type": "PodFailure",
            "fault_types": ["CPUStress", "PodFailure"],
            "resolution_method": "hybrid[CPUStress,PodFailure]",
            "root_candidates": [
                {"node": pod_root.uniq_name, "fault_type_name": "PodFailure"},
                {"node": stress_root.uniq_name, "fault_type_name": "CPUStress"},
            ],
        },
    )

    assert [root.component for root in enriched.root_causes] == [pod_root.uniq_name, stress_root.uniq_name]
    assert [root.state for root in enriched.root_causes] == [frozenset({"unavailable"}), frozenset({"degraded"})]
    assert [root.fault_type for root in enriched.root_causes] == ["PodFailure", "CPUStress"]
    assert [root.root_candidate_index for root in enriched.root_causes] == [0, 1]
    assert enriched.fault_type == "hybrid"
    assert enriched.fault_types == ["CPUStress", "PodFailure"]
    assert len(enriched.root_candidates) == 2

    reasoning_cli._sync_injection_states_from_root_causes(result, enriched)
    assert result.injection_states == ["unavailable", "degraded"]
    assert result.injection_state_reasons == [None, None]


def test_same_component_hybrid_roots_keep_each_candidate() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /checkout"))
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert alarm.id is not None

    injection_json = {
        "fault_type": "hybrid",
        "engine_config": [{"chaos_type": "PodFailure"}, {"chaos_type": "CPUStress"}],
        "ground_truth": [
            {"service": ["checkoutservice"]},
            {"service": ["checkoutservice"]},
        ],
    }
    resolved = InjectionNodeResolver(g).resolve(injection_json)
    root_candidates = [candidate.model_dump(exclude_none=True) for candidate in resolved.root_candidates]
    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[root.id, alarm.id],
                states=[["unavailable"], ["erroring"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[100, 101],
            )
        ],
        visited_nodes={root.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        injection_node_names=resolved.injection_nodes,
        root_candidates=root_candidates,
        alarm_node_ids={alarm.id},
    )
    enriched = reasoning_cli._causal_graph_with_export_metadata(
        causal_graph,
        case_name="same-component-hybrid",
        result=result,
        alarm_accounting={
            "candidate_alarm_nodes": [],
            "explained_alarm_nodes": [],
            "unexplained_alarm_nodes": [],
            "candidate_alarm_count": 0,
            "explained_alarm_count": 0,
            "unexplained_alarm_count": 0,
        },
        resolution_info={
            "fault_type": resolved.fault_type_name,
            "fault_types": sorted({candidate["fault_type_name"] for candidate in root_candidates}),
            "resolution_method": resolved.resolution_method,
            "root_candidates": root_candidates,
        },
    )

    assert resolved.injection_nodes == [root.uniq_name]
    assert [candidate["node"] for candidate in root_candidates] == [root.uniq_name, root.uniq_name]
    assert [root_cause.component for root_cause in enriched.root_causes] == [root.uniq_name, root.uniq_name]
    assert [root_cause.fault_type for root_cause in enriched.root_causes] == ["PodFailure", "CPUStress"]
    assert [root_cause.root_candidate_index for root_cause in enriched.root_causes] == [0, 1]
    assert [root_cause.state for root_cause in enriched.root_causes] == [
        frozenset({"unavailable"}),
        frozenset({"degraded"}),
    ]
    assert enriched.fault_type == "hybrid"


def test_missing_root_candidate_exports_unknown_placeholder_root_cause() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert root.id is not None
    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[],
        visited_nodes={root.id},
        max_hops_reached=0,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids=set(),
        root_candidates=[
            {"node": root.uniq_name, "fault_type_name": "PodFailure"},
            {
                "node": "service|missingservice",
                "fault_type_name": "CPUStress",
                "expected_state": "degraded",
            },
        ],
    )

    assert len(causal_graph.root_causes) == 2
    assert [root_cause.component for root_cause in causal_graph.root_causes] == [
        root.uniq_name,
        "service|missingservice",
    ]
    assert causal_graph.root_causes[1].state == frozenset({"unknown"})
    assert causal_graph.root_causes[1].state_resolution_reason == "root_component_not_in_causal_graph"
    assert causal_graph.root_causes[1].root_candidate_index == 1


def test_root_candidate_index_preserves_exported_metadata_index_after_filtering() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert root.id is not None
    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[],
        visited_nodes={root.id},
        max_hops_reached=0,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids=set(),
        root_candidates=[
            {"node": root.uniq_name, "fault_type_name": "PodFailure"},
            {"fault_type_name": "CPUStress"},
            {"node": "service|missingservice", "fault_type_name": "CPUStress"},
        ],
    )

    assert [root_cause.root_candidate_index for root_cause in causal_graph.root_causes] == [0, 1]


def test_invalid_root_candidates_fall_back_to_primary_root_without_candidate_index() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert root.id is not None
    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["degraded"],
        paths=[],
        visited_nodes={root.id},
        max_hops_reached=0,
    )

    invalid_candidates: list[dict[str, Any]] = [{}, {"node": ""}, {"bad": "bad"}]
    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids=set(),
        root_candidates=invalid_candidates,
    )

    assert len(causal_graph.root_causes) == 1
    assert causal_graph.root_causes[0].component == root.uniq_name
    assert causal_graph.root_causes[0].root_candidate_index is None


def test_root_cause_export_maps_service_root_to_pod_unavailable_state() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="recommendation"))
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="recommendation-0"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /recommendations"))
    _add_edge(g, root, pod, DepKind.routes_to)
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert pod.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[pod.id, alarm.id],
                states=[["unavailable"], ["erroring"]],
                edges=["routes_to"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[100, 110],
            )
        ],
        visited_nodes={root.id, pod.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm.id},
    )

    assert causal_graph.root_causes[0].component == root.uniq_name
    assert causal_graph.root_causes[0].state == frozenset({"unavailable"})
    assert causal_graph.root_causes[0].state_resolution_reason is None

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)
    assert result.injection_states == ["unavailable"]
    assert result.injection_state_reasons == [None]


def test_root_cause_export_maps_jvm_container_fallback_to_span_state() -> None:
    g = HyperGraph()
    service = g.add_node(Node(kind=PlaceKind.service, self_name="ts-order-service"))
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="ts-order-service-0"))
    container = g.add_node(Node(kind=PlaceKind.container, self_name="ts-order-service"))
    span = g.add_node(Node(kind=PlaceKind.span, self_name="ts-order-service::OrderController.create"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::POST /api/order"))
    _add_edge(g, service, pod, DepKind.routes_to)
    _add_edge(g, pod, container, DepKind.runs)
    _add_edge(g, service, span)
    _add_edge(g, span, alarm, DepKind.calls)
    assert container.id is not None
    assert span.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[container.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[span.id, alarm.id],
                states=[["erroring"], ["erroring"]],
                edges=["calls"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[200, 205],
            )
        ],
        visited_nodes={container.id, span.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=container.uniq_name,
        alarm_node_ids={alarm.id},
    )

    assert causal_graph.root_causes[0].component == span.uniq_name
    assert causal_graph.root_causes[0].state == frozenset({"erroring"})
    assert causal_graph.root_causes[0].state_resolution_reason is None

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)
    assert result.injection_states == ["erroring"]
    assert result.injection_state_reasons == [None]


def test_root_cause_export_prefers_topology_evidence_over_expected_fallback() -> None:
    g = HyperGraph()
    service = g.add_node(Node(kind=PlaceKind.service, self_name="ts-order-service"))
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="ts-order-service-0"))
    container = g.add_node(Node(kind=PlaceKind.container, self_name="ts-order-service"))
    span = g.add_node(Node(kind=PlaceKind.span, self_name="ts-order-service::OrderController.create"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::POST /api/order"))
    _add_edge(g, service, pod, DepKind.routes_to)
    _add_edge(g, pod, container, DepKind.runs)
    _add_edge(g, service, span)
    _add_edge(g, span, alarm, DepKind.calls)
    assert container.id is not None
    assert span.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[container.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[span.id, alarm.id],
                states=[["erroring"], ["erroring"]],
                edges=["calls"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[200, 205],
            )
        ],
        visited_nodes={container.id, span.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=container.uniq_name,
        alarm_node_ids={alarm.id},
        root_fallback_states={container.uniq_name: "degraded"},
    )

    assert causal_graph.root_causes[0].component == span.uniq_name
    assert causal_graph.root_causes[0].state == frozenset({"erroring"})


def test_root_cause_export_reports_reason_when_graph_root_has_no_mappable_state() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="ts-root-service"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /api"))
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[alarm.id],
                states=[["unknown"]],
                edges=[],
                rules=[],
                confidence=1.0,
                state_start_times=[300],
            )
        ],
        visited_nodes={root.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm.id},
    )

    root_cause = causal_graph.root_causes[0]
    assert root_cause.state == frozenset({"unknown"})
    assert root_cause.state_resolution_reason == "no_mappable_root_state"
    assert root_cause.state_resolution_reason != "root_component_not_in_causal_graph"

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)
    assert result.injection_states == ["unknown"]
    assert result.injection_state_reasons == ["no_mappable_root_state"]


def test_service_root_does_not_borrow_terminal_alarm_span_state() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="ts-root-service"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-root-service::GET /api"))
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["unknown"],
        paths=[
            PropagationPath(
                nodes=[root.id, alarm.id],
                states=[["unknown"], ["erroring"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[300, 301],
            )
        ],
        visited_nodes={root.id, alarm.id},
        max_hops_reached=1,
    )

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm.id},
    )

    assert causal_graph.root_causes[0].component == root.uniq_name
    assert causal_graph.root_causes[0].state == frozenset({"unknown"})
    assert causal_graph.root_causes[0].state_resolution_reason == "no_mappable_root_state"


def test_injection_state_sync_uses_root_cause_scope_not_starting_point_scope() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="callee"))
    caller_a = g.add_node(Node(kind=PlaceKind.service, self_name="caller-a"))
    caller_b = g.add_node(Node(kind=PlaceKind.service, self_name="caller-b"))
    assert root.id is not None
    assert caller_a.id is not None
    assert caller_b.id is not None
    result = PropagationResult(
        injection_node_ids=[caller_a.id, caller_b.id],
        injection_states=["unknown", "unknown"],
        paths=[],
        visited_nodes={caller_a.id, caller_b.id},
        max_hops_reached=0,
    )
    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids=set(),
        root_candidates=[
            {
                "node": root.uniq_name,
                "fault_type_name": "HTTPResponseDelay",
                "expected_state": "degraded",
            }
        ],
    )

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)

    assert len(causal_graph.root_causes) == 1
    assert result.injection_states == ["degraded"]
    assert result.injection_state_reasons == [None]
    assert len(result.injection_state_details) == 1


def test_injection_state_sync_repeats_shared_node_for_same_component_hybrid() -> None:
    result = PropagationResult(
        injection_node_ids=[42],
        injection_states=["unknown"],
        paths=[],
        visited_nodes=set(),
        max_hops_reached=0,
    )
    causal_graph = CausalGraph(
        root_causes=[
            CausalNode(component="service|checkoutservice", state=frozenset({"unavailable"}), injection_node_id=42),
            CausalNode(component="service|checkoutservice", state=frozenset({"degraded"}), injection_node_id=42),
        ]
    )

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)

    assert result.injection_node_ids == [42, 42]
    assert result.injection_states == ["unavailable", "degraded"]
    assert [detail["injection_node_id"] for detail in result.injection_state_details] == [42, 42]


def test_injection_state_sync_uses_sentinel_for_missing_distinct_root() -> None:
    result = PropagationResult(
        injection_node_ids=[42],
        injection_states=["unknown"],
        paths=[],
        visited_nodes=set(),
        max_hops_reached=0,
    )
    causal_graph = CausalGraph(
        root_causes=[
            CausalNode(component="service|checkoutservice", state=frozenset({"unavailable"}), injection_node_id=42),
            CausalNode(component="service|missingservice", state=frozenset({"unknown"}), injection_node_id=-1),
        ]
    )

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)

    assert result.injection_node_ids == [42, -1]
    assert result.injection_states == ["unavailable", "unknown"]
    assert [detail["injection_node_id"] for detail in result.injection_state_details] == [42, -1]


def test_injection_state_sync_aligns_missing_first_roots_by_component_id() -> None:
    g = HyperGraph()
    checkout = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    assert checkout.id is not None
    result = PropagationResult(
        injection_node_ids=[checkout.id],
        injection_states=["unknown"],
        paths=[],
        visited_nodes={checkout.id},
        max_hops_reached=0,
    )
    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=checkout.uniq_name,
        alarm_node_ids=set(),
        root_candidates=[
            {"node": "service|missingservice", "fault_type_name": "NetworkPartition"},
            {"node": checkout.uniq_name, "fault_type_name": "NetworkPartition"},
        ],
    )

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)

    assert result.injection_node_ids == [-1, checkout.id]
    assert [detail["component"] for detail in result.injection_state_details] == [
        "service|missingservice",
        checkout.uniq_name,
    ]
    assert [detail["injection_node_id"] for detail in result.injection_state_details] == [-1, checkout.id]


def test_injection_state_sync_keeps_parallel_arrays_when_node_ids_empty() -> None:
    result = PropagationResult(
        injection_node_ids=[],
        injection_states=[],
        paths=[],
        visited_nodes=set(),
        max_hops_reached=0,
    )
    causal_graph = CausalGraph(
        root_causes=[CausalNode(component="service|missingservice", state=frozenset({"unknown"}))]
    )

    reasoning_cli._sync_injection_states_from_root_causes(result, causal_graph)

    assert result.injection_node_ids == [-1]
    assert result.injection_states == ["unknown"]
    assert result.injection_state_details[0]["injection_node_id"] == -1


def test_alarm_accounting_separates_unexplained_strong_and_penalizes_weak_path() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="ts-root-service"))
    weak_alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /weak"))
    strong_alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /strong-ok"))
    strong_unexplained = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::POST /strong"))
    _add_edge(g, root, weak_alarm)
    _add_edge(g, root, strong_alarm)
    _add_edge(g, root, strong_unexplained)
    assert root.id is not None
    assert weak_alarm.id is not None
    assert strong_alarm.id is not None
    assert strong_unexplained.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["degraded"],
        paths=[
            PropagationPath(
                nodes=[root.id, weak_alarm.id],
                states=[["degraded"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[100, 101],
            ),
            PropagationPath(
                nodes=[root.id, strong_alarm.id],
                states=[["degraded"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[100, 101],
            ),
        ],
        visited_nodes={root.id, weak_alarm.id, strong_alarm.id, strong_unexplained.id},
        max_hops_reached=1,
    )
    evidence_by_name = {
        weak_alarm.self_name: {
            "issue_strength": "weak",
            "issue_strength_reason": "weak_latency_signal",
            "has_issues": False,
        },
        strong_alarm.self_name: {
            "issue_strength": "strong",
            "issue_strength_reason": "conclusion_issues",
            "has_issues": True,
        },
        strong_unexplained.self_name: {
            "issue_strength": "strong",
            "issue_strength_reason": "conclusion_issues",
            "has_issues": True,
        },
    }

    reasoning_cli._apply_terminal_alarm_confidence_caps(
        result=result,
        graph=g,
        alarm_nodes={weak_alarm.id, strong_alarm.id, strong_unexplained.id},
        evidence_by_name=evidence_by_name,
    )
    default_paths, weak_paths = reasoning_cli._split_default_and_weak_paths(
        result=result,
        graph=g,
        alarm_nodes={weak_alarm.id, strong_alarm.id, strong_unexplained.id},
        evidence_by_name=evidence_by_name,
    )
    accounting = reasoning_cli._build_alarm_accounting(
        result=result,
        graph=g,
        alarm_nodes={weak_alarm.id, strong_alarm.id, strong_unexplained.id},
        evidence_by_name=evidence_by_name,
    )

    assert result.paths[0].confidence == 0.65
    assert result.paths[1].confidence == 1.0
    assert default_paths == [result.paths[1]]
    assert weak_paths == [result.paths[0]]
    assert accounting["candidate_alarm_node_ids"] == sorted([weak_alarm.id, strong_alarm.id, strong_unexplained.id])
    assert accounting["explained_alarm_node_ids"] == sorted([weak_alarm.id, strong_alarm.id])
    assert accounting["unexplained_alarm_node_ids"] == [strong_unexplained.id]
    assert accounting["candidate_alarm_count"] == 3
    assert accounting["explained_alarm_count"] == 2
    assert accounting["unexplained_alarm_count"] == 1
    assert accounting["candidate_alarm_count"] == (
        accounting["explained_alarm_count"] + accounting["unexplained_alarm_count"]
    )
    assert accounting["path_terminal_alarm_count"] == accounting["explained_alarm_count"]
    assert accounting["path_terminal_alarm_node_ids"] == accounting["explained_alarm_node_ids"]
    assert accounting["strong_alarm_coverage"] == 0.5
    assert accounting["unexplained_strong_alarm_count"] == 1
    assert accounting["explained_alarm_nodes"][0]["path_ids"]
    assert accounting["unexplained_alarm_nodes"][0]["drop_reason"] == "no_path_found"
    assert accounting["unexplained_alarm_nodes"][0]["path_status"] == "strong_unexplained"

    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=reasoning_cli._result_with_paths(result, [result.paths[1]]),
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={weak_alarm.id, strong_alarm.id, strong_unexplained.id},
    )
    enriched = reasoning_cli._causal_graph_with_export_metadata(
        causal_graph,
        case_name="synthetic-case",
        result=result,
        alarm_accounting=accounting,
        resolution_info={"fault_type": "PodFailure", "resolution_method": "service"},
    )
    assert enriched.case_name == "synthetic-case"
    assert enriched.fault_type == "PodFailure"
    assert enriched.alarm_nodes_scope == "path_terminal_alarm_nodes"
    assert enriched.candidate_alarm_count == 3
    assert enriched.explained_alarm_count == 2
    assert enriched.unexplained_alarm_count == 1
    assert enriched.path_terminal_alarm_count == 1
    assert len(enriched.path_terminal_alarm_nodes) == len(enriched.alarm_nodes) == 1
    dumped = enriched.model_dump(exclude_none=True)
    assert dumped["candidate_alarm_count"] == dumped["explained_alarm_count"] + dumped["unexplained_alarm_count"]
    assert dumped["path_terminal_alarm_count"] == len(dumped["path_terminal_alarm_nodes"])
    assert CausalGraph.from_dict(dumped).path_terminal_alarm_count == 1
    assert enriched.strong_alarm_coverage == 0.5
    assert enriched.confidence_breakdown["rule_admission_confidence"] == 1.0


def test_alarm_export_schema_is_self_consistent_with_unexplained_candidates() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    explained = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /explained"))
    unexplained_a = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /unexplained-a"))
    unexplained_b = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /unexplained-b"))
    _add_edge(g, root, explained)
    _add_edge(g, root, unexplained_a)
    _add_edge(g, root, unexplained_b)
    assert root.id is not None
    assert explained.id is not None
    assert unexplained_a.id is not None
    assert unexplained_b.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["degraded"],
        paths=[
            PropagationPath(
                nodes=[root.id, explained.id],
                states=[["degraded"], ["erroring"]],
                edges=["includes"],
                rules=["test"],
                confidence=0.9,
                state_start_times=[300, 301],
            )
        ],
        visited_nodes={root.id, explained.id, unexplained_a.id, unexplained_b.id},
        max_hops_reached=1,
    )
    alarm_nodes = {explained.id, unexplained_a.id, unexplained_b.id}
    evidence_by_name = {
        explained.self_name: {
            "issue_strength": "strong",
            "issue_strength_reason": "conclusion_issues",
            "has_issues": True,
        },
        unexplained_a.self_name: {
            "issue_strength": "strong",
            "issue_strength_reason": "conclusion_issues",
            "has_issues": True,
        },
        unexplained_b.self_name: {
            "issue_strength": "weak",
            "issue_strength_reason": "weak_latency_signal",
            "has_issues": False,
        },
    }

    accounting = reasoning_cli._build_alarm_accounting(
        result=result,
        graph=g,
        alarm_nodes=alarm_nodes,
        evidence_by_name=evidence_by_name,
    )
    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids=alarm_nodes,
    )
    enriched = reasoning_cli._causal_graph_with_export_metadata(
        causal_graph,
        case_name="synthetic-alarm-counts",
        result=result,
        alarm_accounting=accounting,
        resolution_info={"fault_type": "PodFailure", "resolution_method": "service"},
    )
    round_trip = CausalGraph.from_dict(enriched.model_dump())

    assert round_trip.alarm_nodes_scope == "path_terminal_alarm_nodes"
    assert round_trip.candidate_alarm_count == 3
    assert round_trip.explained_alarm_count == 1
    assert round_trip.unexplained_alarm_count == 2
    assert round_trip.candidate_alarm_count == round_trip.explained_alarm_count + round_trip.unexplained_alarm_count
    assert len(round_trip.candidate_alarm_nodes) == round_trip.candidate_alarm_count
    assert len(round_trip.explained_alarm_nodes) == round_trip.explained_alarm_count
    assert len(round_trip.unexplained_alarm_nodes) == round_trip.unexplained_alarm_count
    assert len(round_trip.alarm_nodes) == round_trip.explained_alarm_count
    assert len(round_trip.path_terminal_alarm_nodes) == round_trip.explained_alarm_count
    assert {node.component for node in round_trip.path_terminal_alarm_nodes} == {
        item["component"] for item in round_trip.explained_alarm_nodes
    }

    candidate_ids = {item["node_id"] for item in round_trip.candidate_alarm_nodes}
    explained_ids = {item["node_id"] for item in round_trip.explained_alarm_nodes}
    unexplained_ids = {item["node_id"] for item in round_trip.unexplained_alarm_nodes}
    assert candidate_ids == explained_ids | unexplained_ids
    assert explained_ids.isdisjoint(unexplained_ids)


def test_path_terminal_alarm_export_dedupes_same_alarm_with_multiple_states() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /checkout"))
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["degraded"],
        paths=[
            PropagationPath(
                nodes=[root.id, alarm.id],
                states=[["degraded"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=0.9,
                state_start_times=[400, 401],
            ),
            PropagationPath(
                nodes=[root.id, alarm.id],
                states=[["degraded"], ["erroring"]],
                edges=["includes"],
                rules=["test"],
                confidence=0.9,
                state_start_times=[405, 406],
            ),
        ],
        visited_nodes={root.id, alarm.id},
        max_hops_reached=1,
    )

    accounting = reasoning_cli._build_alarm_accounting(
        result=result,
        graph=g,
        alarm_nodes={alarm.id},
        evidence_by_name={
            alarm.self_name: {
                "issue_strength": "strong",
                "issue_strength_reason": "conclusion_issues",
                "has_issues": True,
            }
        },
    )
    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm.id},
    )
    enriched = reasoning_cli._causal_graph_with_export_metadata(
        causal_graph,
        case_name="duplicate-terminal-state",
        result=result,
        alarm_accounting=accounting,
        resolution_info={"fault_type": "PodFailure", "resolution_method": "service"},
    )

    assert accounting["candidate_alarm_count"] == 1
    assert accounting["explained_alarm_count"] == 1
    assert enriched.path_terminal_alarm_count == 1
    assert len(enriched.path_terminal_alarm_nodes) == 1
    assert len(enriched.alarm_nodes) == 1
    assert enriched.path_terminal_alarm_nodes[0].state == frozenset({"slow", "erroring"})


def test_success_result_json_declares_alarm_nodes_candidate_scope(tmp_path) -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="checkoutservice"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /checkout"))
    _add_edge(g, root, alarm)
    assert root.id is not None
    assert alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["degraded"],
        paths=[
            PropagationPath(
                nodes=[root.id, alarm.id],
                states=[["degraded"], ["erroring"]],
                edges=["includes"],
                rules=["test"],
                confidence=0.9,
                state_start_times=[400, 401],
            )
        ],
        visited_nodes={root.id, alarm.id},
        max_hops_reached=1,
    )
    evidence_by_name = {
        alarm.self_name: {
            "issue_strength": "strong",
            "issue_strength_reason": "conclusion_issues",
            "has_issues": True,
        }
    }
    accounting = reasoning_cli._build_alarm_accounting(
        result=result,
        graph=g,
        alarm_nodes={alarm.id},
        evidence_by_name=evidence_by_name,
    )
    causal_graph = reasoning_cli.propagation_result_to_causal_graph(
        result=result,
        graph=g,
        injection_node_name=root.uniq_name,
        alarm_node_ids={alarm.id},
    )
    causal_graph = reasoning_cli._causal_graph_with_export_metadata(
        causal_graph,
        case_name="synthetic-result-scope",
        result=result,
        alarm_accounting=accounting,
        resolution_info=None,
    )

    reasoning_cli._save_case_result(
        data_dir=tmp_path,
        case_name="synthetic-result-scope",
        status="success",
        causal_graph=causal_graph,
        injection_nodes=[root.uniq_name],
        alarm_nodes={alarm.id},
        result=result,
        alarm_accounting=accounting,
    )

    result_data = json.loads((tmp_path / "result.json").read_text())
    graph_data = json.loads((tmp_path / "causal_graph.json").read_text())
    assert result_data["alarm_nodes_scope"] == "candidate_alarm_nodes"
    assert result_data["candidate_alarm_count"] == 1
    assert result_data["alarm_nodes"] == [alarm.id]
    assert graph_data["alarm_nodes_scope"] == "path_terminal_alarm_nodes"
    assert graph_data["path_terminal_alarm_count"] == len(graph_data["alarm_nodes"])
    assert graph_data["candidate_alarm_node_ids"] == [alarm.id]
    assert graph_data["explained_alarm_node_ids"] == [alarm.id]
    assert graph_data["unexplained_alarm_node_ids"] == []
    assert graph_data["path_terminal_alarm_node_ids"] == [alarm.id]

    round_trip = CausalGraph.from_dict(graph_data)
    assert round_trip.candidate_alarm_node_ids == [alarm.id]
    assert round_trip.explained_alarm_node_ids == [alarm.id]
    assert round_trip.unexplained_alarm_node_ids == []
    assert round_trip.path_terminal_alarm_node_ids == [alarm.id]


def test_alarm_accounting_zero_strong_denominator_is_null() -> None:
    g = HyperGraph()
    root = g.add_node(Node(kind=PlaceKind.service, self_name="ts-root-service"))
    weak_alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /weak"))
    _add_edge(g, root, weak_alarm)
    assert root.id is not None
    assert weak_alarm.id is not None

    result = PropagationResult(
        injection_node_ids=[root.id],
        injection_states=["degraded"],
        paths=[
            PropagationPath(
                nodes=[root.id, weak_alarm.id],
                states=[["degraded"], ["slow"]],
                edges=["includes"],
                rules=["test"],
                confidence=1.0,
                state_start_times=[100, 101],
            )
        ],
        visited_nodes={root.id, weak_alarm.id},
        max_hops_reached=1,
    )
    accounting = reasoning_cli._build_alarm_accounting(
        result=result,
        graph=g,
        alarm_nodes={weak_alarm.id},
        evidence_by_name={
            weak_alarm.self_name: {
                "issue_strength": "weak",
                "issue_strength_reason": "weak_latency_signal",
            }
        },
    )

    assert accounting["candidate_strong_alarm_count"] == 0
    assert accounting["path_terminal_alarm_count"] == 1
    assert accounting["strong_alarm_coverage"] is None
    assert accounting["strong_alarm_coverage_reason"] == "no_candidate_strong_alarms"


def test_erroring_export_state_removes_healthy_and_missing_noise() -> None:
    assert reasoning_cli._canonical_export_states(["healthy", "missing", "erroring"]) == frozenset({"erroring"})


def test_alarm_evidence_matches_bare_conclusion_span_to_graph_component() -> None:
    g = HyperGraph()
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::HTTP /recommendations"))
    assert alarm.id is not None
    evidence_by_name = _alarm_index(
        [
            {
                "SpanName": "HTTP /recommendations",
                "Issues": "{}",
                "NormalSuccRate": 1.0,
                "AbnormalSuccRate": 0.001,
            }
        ]
    )

    evidence = reasoning_cli._alarm_evidence_for_node(alarm.id, g, evidence_by_name)

    assert evidence["issue_strength"] == "strong"
    assert evidence["issue_strength_reason"] == "success_rate_drop"
    assert evidence["conclusion_match"]["status"] == "matched"
    assert evidence["conclusion_match"]["method"] == "bare_operation_unique"
    assert evidence["conclusion_span_name"] == "HTTP /recommendations"


def test_alarm_evidence_marks_ambiguous_bare_operation_without_service_match() -> None:
    g = HyperGraph()
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /shared"))
    assert alarm.id is not None
    evidence_by_name = _alarm_index(
        [
            {"SpanName": "service-a::GET /shared", "Issues": '{"errors": 10}'},
            {"SpanName": "service-b::GET /shared", "Issues": '{"errors": 11}'},
        ]
    )

    evidence = reasoning_cli._alarm_evidence_for_node(alarm.id, g, evidence_by_name)

    assert evidence["issue_strength"] == "unknown"
    assert evidence["issue_strength_reason"] == "ambiguous_conclusion_match"
    assert evidence["conclusion_match"]["status"] == "ambiguous"
    assert evidence["conclusion_match"]["method"] == "bare_operation_unique"


def test_alarm_evidence_matches_full_url_to_service_operation() -> None:
    g = HyperGraph()
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /api/v1/foodservice/foods"))
    assert alarm.id is not None
    evidence_by_name = _alarm_index(
        [
            {
                "SpanName": "HTTP GET http://ts-ui-dashboard:8080/api/v1/foodservice/foods",
                "Issues": "{}",
                "NormalSuccRate": 1.0,
                "AbnormalSuccRate": 0.2,
            }
        ]
    )

    evidence = reasoning_cli._alarm_evidence_for_node(alarm.id, g, evidence_by_name)

    assert evidence["issue_strength"] == "strong"
    assert evidence["conclusion_match"]["status"] == "matched"
    assert evidence["conclusion_match"]["method"] == "service_operation"
