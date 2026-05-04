from __future__ import annotations

import json
from pathlib import Path

import pytest

DATASET_CASES = Path("/home/ddq/AoyangSpace/dataset/dataset_v3_500_2026-05-03/cases")

BAD_CASE_COUNTS = {
    "otel-demo15-currency-pod-kill-xvmpqx": (16, 2),
    "hs0-user-pod-failure-cnf9cr": (12, 6),
    "hs1-recommendation-pod-failure-frllxg": (6, 2),
    "hs31-recommendation-pod-failure-dn57db": (3, 2),
    "ts0-ts-preserve-service-response-replace-body-644lf4": (7, 1),
}

CALLER_SPAN_EXPECTATIONS = {
    "hs0-user-pod-failure-cnf9cr": "span|frontend::user.User/CheckUser",
    "hs1-recommendation-pod-failure-frllxg": "span|frontend::recommendation.Recommendation/GetRecommendations",
    "hs31-recommendation-pod-failure-dn57db": "span|frontend::recommendation.Recommendation/GetRecommendations",
}


def _case_json(case_name: str) -> tuple[dict, dict]:
    case_dir = DATASET_CASES / case_name
    return (
        json.loads((case_dir / "causal_graph.json").read_text()),
        json.loads((case_dir / "result.json").read_text()),
    )


@pytest.mark.skipif(not DATASET_CASES.exists(), reason="local v3 alpha dataset is not available")
def test_export_consistency_bad_case_alarm_accounting() -> None:
    for case_name, (candidate_count, explained_count) in BAD_CASE_COUNTS.items():
        causal_graph, result = _case_json(case_name)
        assert len(result["alarm_nodes"]) == candidate_count
        assert causal_graph["candidate_alarm_count"] == candidate_count
        assert causal_graph["explained_alarm_count"] == explained_count
        assert len(causal_graph["candidate_alarm_nodes"]) == candidate_count
        assert len(causal_graph["explained_alarm_nodes"]) == explained_count
        assert causal_graph["alarm_nodes_scope"] == "path_terminal_compat_alias"


@pytest.mark.skipif(not DATASET_CASES.exists(), reason="local v3 alpha dataset is not available")
def test_export_consistency_bad_case_root_states_and_caller_errors() -> None:
    for case_name in [
        "hs0-user-pod-failure-cnf9cr",
        "hs1-recommendation-pod-failure-frllxg",
        "hs31-recommendation-pod-failure-dn57db",
        "otel-demo15-currency-pod-kill-xvmpqx",
    ]:
        causal_graph, result = _case_json(case_name)
        assert "unknown" not in result["propagation_result"]["injection_states"]
        assert causal_graph["root_causes"][0]["state"]

    for case_name, component in CALLER_SPAN_EXPECTATIONS.items():
        causal_graph, _result = _case_json(case_name)
        matches = [node for node in causal_graph["nodes"] if node["component"] == component]
        assert matches, (case_name, component)
        assert "erroring" in matches[0]["state"]
        assert "healthy" not in matches[0]["state"]
        assert "missing" not in matches[0]["state"]
