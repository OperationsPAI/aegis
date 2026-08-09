"""Trace -> DB binding adapter tests.

Each test builds a minimal HyperGraph + polars trace fixture and asserts the
expected graph mutation. No parquet data is loaded.
"""

from __future__ import annotations

import polars as pl

from rcabench_platform.v3.internal.reasoning.ir.adapters.trace_db_binding import (
    TraceDbBindingMetadata,
    TrainTicketTraceDbBindingAdapter,
    dispatch_trace_db_binding_adapters,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)


def _add_node(graph: HyperGraph, kind: PlaceKind, name: str) -> Node:
    return graph.add_node(Node(kind=kind, self_name=name))


def _add_edge(graph: HyperGraph, src: Node, dst: Node, kind: DepKind) -> None:
    assert src.id is not None and dst.id is not None
    graph.add_edge(
        Edge(
            src_id=src.id,
            dst_id=dst.id,
            src_name=src.uniq_name,
            dst_name=dst.uniq_name,
            kind=kind,
            data=None,
        )
    )


def _trainticket_traces(rows: list[dict[str, object]]) -> pl.DataFrame:
    """Build an abnormal-traces DataFrame with the columns the adapter reads."""
    schema = {
        "service_name": pl.Utf8,
        "span_name": pl.Utf8,
        "attr.span_kind": pl.Utf8,
        "attr.http.response.status_code": pl.Int64,
    }
    if not rows:
        return pl.DataFrame(schema=schema)
    return pl.DataFrame(rows, schema=schema)


def _build_tt_graph_with_mysql() -> tuple[HyperGraph, dict[str, Node]]:
    """Minimal TT-shaped graph: one Java service + mysql infra (no service node yet)."""
    g = HyperGraph()
    sset = _add_node(g, PlaceKind.stateful_set, "mysql")
    pod_mysql = _add_node(g, PlaceKind.pod, "mysql-0")
    cont_mysql = _add_node(g, PlaceKind.container, "mysql")
    svc_auth = _add_node(g, PlaceKind.service, "ts-auth-service")
    pod_auth = _add_node(g, PlaceKind.pod, "ts-auth-service-1")
    cont_auth = _add_node(g, PlaceKind.container, "ts-auth-service")
    span_sql = _add_node(g, PlaceKind.span, "ts-auth-service::SELECT ts.users")
    span_root = _add_node(g, PlaceKind.span, "ts-auth-service::POST /api/v1/auth")
    _add_edge(g, pod_mysql, cont_mysql, DepKind.runs)
    _add_edge(g, svc_auth, pod_auth, DepKind.routes_to)
    _add_edge(g, pod_auth, cont_auth, DepKind.runs)
    _add_edge(g, svc_auth, span_sql, DepKind.includes)
    _add_edge(g, svc_auth, span_root, DepKind.includes)
    return g, {
        "sset_mysql": sset,
        "pod_mysql": pod_mysql,
        "cont_mysql": cont_mysql,
        "svc_auth": svc_auth,
        "span_sql": span_sql,
        "span_root": span_root,
    }


def test_applies_returns_true_for_trainticket_traces() -> None:
    adapter = TrainTicketTraceDbBindingAdapter()
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
        ]
    )
    assert adapter.applies(df) is True


def test_applies_returns_false_for_non_trainticket_systems() -> None:
    adapter = TrainTicketTraceDbBindingAdapter()
    df = _trainticket_traces(
        [
            {
                "service_name": "cartservice",
                "span_name": "GET /cart",
                "attr.span_kind": "Server",
                "attr.http.response.status_code": 200,
            },
            {
                "service_name": "loadgenerator",
                "span_name": "GET /",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": 200,
            },
        ]
    )
    assert adapter.applies(df) is False
    assert adapter.applies(_trainticket_traces([])) is False


def test_sql_pattern_detection_excludes_http_spans() -> None:
    adapter = TrainTicketTraceDbBindingAdapter()
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
            {
                "service_name": "ts-auth-service",
                "span_name": "INSERT ts.session",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
            {
                "service_name": "ts-auth-service",
                "span_name": "HTTP GET http://other/x",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": 200,
            },
            {
                "service_name": "ts-auth-service",
                "span_name": "GET",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": 200,
            },
            {
                "service_name": "ts-auth-service",
                "span_name": "POST",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": 200,
            },
            {
                "service_name": "ts-train-service",
                "span_name": "UPDATE ts.train",
                "attr.span_kind": "Internal",
                "attr.http.response.status_code": None,
            },
            {
                "service_name": "ts-train-service",
                "span_name": "delete ts.cache",
                "attr.span_kind": "Internal",
                "attr.http.response.status_code": None,
            },
        ]
    )
    detected = adapter.detect_db_client_spans(df)
    assert detected.height == 4
    detected_names = sorted(detected["span_name"].to_list())
    assert detected_names == sorted(["SELECT ts.users", "INSERT ts.session", "UPDATE ts.train", "delete ts.cache"])


def test_dispatch_synthesises_calls_edge_with_evidence_count() -> None:
    g, nodes = _build_tt_graph_with_mysql()
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
            {
                "service_name": "ts-auth-service",
                "span_name": "INSERT ts.session",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": 500,
            },
        ]
    )
    n_added = dispatch_trace_db_binding_adapters(g, df)
    assert n_added > 0

    mysql_svc = g.get_node_by_name("service|mysql")
    assert mysql_svc is not None and mysql_svc.id is not None
    auth_svc = nodes["svc_auth"]
    assert auth_svc.id is not None
    assert g._graph.has_edge(auth_svc.id, mysql_svc.id, DepKind.calls)

    metadata_store = g.data["trace_db_bindings"]
    md = metadata_store[(auth_svc.id, mysql_svc.id, DepKind.calls)]
    assert isinstance(md, TraceDbBindingMetadata)
    assert md.caller_service == "ts-auth-service"
    assert md.db_target_service == "mysql"
    assert md.span_count == 3
    assert md.error_count == 1


def test_dispatch_wires_structural_edges_for_inheritance_cascade() -> None:
    g, nodes = _build_tt_graph_with_mysql()
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
        ]
    )
    dispatch_trace_db_binding_adapters(g, df)

    mysql_svc = g.get_node_by_name("service|mysql")
    assert mysql_svc is not None and mysql_svc.id is not None
    pod_mysql_id = nodes["pod_mysql"].id
    sset_mysql_id = nodes["sset_mysql"].id
    assert pod_mysql_id is not None and sset_mysql_id is not None
    # service|mysql --routes_to--> pod|mysql-0  (so structural inheritance can
    # walk pod -> service backwards via routes_to in_edges).
    assert g._graph.has_edge(mysql_svc.id, pod_mysql_id, DepKind.routes_to)
    # stateful_set|mysql --manages--> pod|mysql-0
    assert g._graph.has_edge(sset_mysql_id, pod_mysql_id, DepKind.manages)


def test_dispatch_adds_includes_edges_to_sql_span_nodes() -> None:
    """The synthesised includes edge is what makes RULE_SERVICE_TO_SPAN traverse."""
    g, nodes = _build_tt_graph_with_mysql()
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
        ]
    )
    dispatch_trace_db_binding_adapters(g, df)

    mysql_svc = g.get_node_by_name("service|mysql")
    assert mysql_svc is not None and mysql_svc.id is not None
    span_sql_id = nodes["span_sql"].id
    assert span_sql_id is not None
    assert g._graph.has_edge(mysql_svc.id, span_sql_id, DepKind.includes)


def test_dispatch_is_idempotent() -> None:
    g, _ = _build_tt_graph_with_mysql()
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
        ]
    )
    first = dispatch_trace_db_binding_adapters(g, df)
    second = dispatch_trace_db_binding_adapters(g, df)
    assert first > 0
    # Second pass must not add any edges; the metadata store must keep its
    # original entries unchanged.
    assert second == 0


def test_dispatch_noop_when_mysql_infra_absent() -> None:
    """Constraint: skip cleanly (no crash, no fabricated service node) when
    no mysql container/pod/stateful_set node exists in the graph."""
    g = HyperGraph()
    svc_auth = _add_node(g, PlaceKind.service, "ts-auth-service")
    span_sql = _add_node(g, PlaceKind.span, "ts-auth-service::SELECT ts.users")
    _add_edge(g, svc_auth, span_sql, DepKind.includes)
    df = _trainticket_traces(
        [
            {
                "service_name": "ts-auth-service",
                "span_name": "SELECT ts.users",
                "attr.span_kind": "Client",
                "attr.http.response.status_code": None,
            },
        ]
    )
    n_added = dispatch_trace_db_binding_adapters(g, df)
    assert n_added == 0
    assert g.get_node_by_name("service|mysql") is None


def test_end_to_end_smoke_real_dataset_dispatch() -> None:
    """End-to-end smoke against the actual failing dataset (skipped if absent).

    Loads the real ts0-mysql-container-kill-9t6n24 abnormal traces and runs
    dispatch against a freshly-built graph; asserts the auth -> mysql calls
    edge materialises. Uses ``pytest.skip`` when the dataset isn't mounted so
    CI on machines without JuiceFS still passes.
    """
    from pathlib import Path

    import pytest

    from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader

    case_dir = Path("/home/ddq/AoyangSpace/dataset/rca/ts0-mysql-container-kill-9t6n24")
    if not (case_dir / "abnormal_traces.parquet").exists():
        pytest.skip(f"dataset not mounted at {case_dir}")

    loader = ParquetDataLoader(case_dir, 2)
    graph = loader.build_graph_from_parquet()
    abnormal = loader.load_traces("abnormal")
    n_added = dispatch_trace_db_binding_adapters(graph, abnormal)
    assert n_added > 0
    auth = graph.get_node_by_name("service|ts-auth-service")
    mysql_svc = graph.get_node_by_name("service|mysql")
    assert auth is not None and mysql_svc is not None
    assert auth.id is not None and mysql_svc.id is not None
    assert graph._graph.has_edge(auth.id, mysql_svc.id, DepKind.calls)
