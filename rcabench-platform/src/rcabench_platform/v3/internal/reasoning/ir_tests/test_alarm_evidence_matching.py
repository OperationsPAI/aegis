from __future__ import annotations

from rcabench_platform.v3.internal.reasoning import cli as reasoning_cli
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node, PlaceKind


def _alarm_index(rows: list[dict]) -> dict:
    evidence_by_name = {reasoning_cli._ALARM_EVIDENCE_INDEX_KEY: reasoning_cli._new_alarm_index()}
    for row in rows:
        raw_name = row["SpanName"]
        evidence = reasoning_cli._classify_conclusion_alarm(row)
        evidence["conclusion_span_name"] = raw_name
        reasoning_cli._append_alarm_index(
            evidence_by_name[reasoning_cli._ALARM_EVIDENCE_INDEX_KEY],
            reasoning_cli._parse_alarm_identity(raw_name),
            evidence,
        )
    return evidence_by_name


def test_alarm_evidence_matches_unique_templated_path_signature() -> None:
    graph = HyperGraph()
    alarm = graph.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /api/v1/orderservice/order/123"))
    assert alarm.id is not None

    evidence = _alarm_index(
        [
            {
                "SpanName": "HTTP GET http://ts-ui-dashboard:8080/api/v1/orderservice/order/{orderId}",
                "Issues": '{"endpoint_disappeared": {"slo_violated": true}}',
                "NormalSuccRate": 1.0,
                "AbnormalSuccRate": 1.0,
            }
        ]
    )

    matched = reasoning_cli._alarm_evidence_for_node(alarm.id, graph, evidence)

    assert matched["issue_strength"] == "strong"
    assert matched["conclusion_match"]["method"] == "http_endpoint_signature_unique"


def test_alarm_evidence_keeps_ambiguous_path_signature_unknown() -> None:
    graph = HyperGraph()
    alarm = graph.add_node(Node(kind=PlaceKind.span, self_name="ts-ui-dashboard::GET /api/v1/orderservice/order/123"))
    assert alarm.id is not None

    evidence = _alarm_index(
        [
            {
                "SpanName": "HTTP GET http://ts-ui-dashboard:8080/api/v1/orderservice/order/{orderId}",
                "Issues": '{"endpoint_disappeared": {"slo_violated": true}}',
                "NormalSuccRate": 1.0,
                "AbnormalSuccRate": 1.0,
            },
            {
                "SpanName": "HTTP GET http://ts-ui-dashboard:8080/api/v1/orderservice/order/{otherId}",
                "Issues": '{"endpoint_disappeared": {"slo_violated": true}}',
                "NormalSuccRate": 1.0,
                "AbnormalSuccRate": 1.0,
            },
        ]
    )

    matched = reasoning_cli._alarm_evidence_for_node(alarm.id, graph, evidence)

    assert matched["issue_strength"] == "unknown"
    assert matched["conclusion_match"]["status"] == "ambiguous"
