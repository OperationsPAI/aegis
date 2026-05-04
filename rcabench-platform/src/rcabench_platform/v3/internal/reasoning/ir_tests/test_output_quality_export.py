from __future__ import annotations

from rcabench_platform.v3.internal.reasoning import cli as reasoning_cli
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, HyperGraph, Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationPath, PropagationResult


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
    assert enriched.strong_alarm_coverage == 0.5
    assert enriched.confidence_breakdown["rule_admission_confidence"] == 1.0


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
