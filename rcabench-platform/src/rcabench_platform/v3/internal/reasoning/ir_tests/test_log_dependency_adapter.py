"""Log-dependency adapter — base kernel + TrainTicket subclass.

Each test builds a minimal HyperGraph + StateTimeline + log fixture and
asserts the expected edge-mutation outcome. No real parquet datapack is
loaded.
"""

from __future__ import annotations

from typing import ClassVar

import polars as pl
import pytest

from rcabench_platform.v3.internal.reasoning.ir.adapters.log_dependency import (
    LogDependencyAdapter,
    LogEvidenceMetadata,
    TrainTicketLogAdapter,
    _clear_log_registry_for_tests,
    dispatch_log_adapters,
    get_registered_log_adapters,
    register_log_adapter,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
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


def _make_timeline(
    node_key: str,
    kind: PlaceKind,
    state: str,
) -> StateTimeline:
    return StateTimeline(
        node_key=node_key,
        kind=kind,
        windows=(
            TimelineWindow(
                start=2000,
                end=2030,
                state=state,
                level=EvidenceLevel.observed,
                trigger="fixture",
                evidence={},
            ),
        ),
    )


def _make_logs_df(rows: list[dict[str, str]]) -> pl.DataFrame:
    if not rows:
        return pl.DataFrame(schema={"service_name": pl.Utf8, "level": pl.Utf8, "message": pl.Utf8})
    return pl.DataFrame(rows)


# -----------------------------------------------------------------------------
# Build a TT-flavoured fixture: caller (ts-train-service) and target (mysql).
# -----------------------------------------------------------------------------


def _build_tt_mysql_graph() -> tuple[HyperGraph, dict[str, Node]]:
    """Caller has an alarm span; mysql infra is unavailable; no trace edge
    between them (JDBC is not OTel-instrumented).

    ``service|ts-train-service`` -> ``span|ts-train-service::POST /train``
    ``service|mysql`` -> ``pod|mysql-0`` -> ``container|mysql``
    """
    g = HyperGraph()
    svc_caller = _add_node(g, PlaceKind.service, "ts-train-service")
    pod_caller = _add_node(g, PlaceKind.pod, "ts-train-service-abc")
    cont_caller = _add_node(g, PlaceKind.container, "ts-train-service")
    span_caller = _add_node(g, PlaceKind.span, "ts-train-service::POST /train")

    svc_db = _add_node(g, PlaceKind.service, "mysql")
    pod_db = _add_node(g, PlaceKind.pod, "mysql-0")
    cont_db = _add_node(g, PlaceKind.container, "mysql")

    _add_edge(g, svc_caller, pod_caller, DepKind.routes_to)
    _add_edge(g, pod_caller, cont_caller, DepKind.runs)
    _add_edge(g, svc_caller, span_caller, DepKind.includes)

    _add_edge(g, svc_db, pod_db, DepKind.routes_to)
    _add_edge(g, pod_db, cont_db, DepKind.runs)

    return g, {
        "svc_caller": svc_caller,
        "span_caller": span_caller,
        "svc_db": svc_db,
        "pod_db": pod_db,
        "cont_db": cont_db,
    }


def _tt_timelines() -> dict[str, StateTimeline]:
    return {
        "container|mysql": _make_timeline("container|mysql", PlaceKind.container, "unavailable"),
        "pod|mysql-0": _make_timeline("pod|mysql-0", PlaceKind.pod, "unavailable"),
        "span|ts-train-service::POST /train": _make_timeline(
            "span|ts-train-service::POST /train", PlaceKind.span, "erroring"
        ),
    }


# -----------------------------------------------------------------------------
# Tests
# -----------------------------------------------------------------------------


def test_trainticket_explicit_jdbc_class_emits_edge() -> None:
    """HikariPool error mentioning ``com.mysql.cj.jdbc.ConnectionImpl``
    extracts ``mysql`` directly and adds inferred edge to caller's alarm.
    """
    g, nodes = _build_tt_mysql_graph()
    abnormal = _make_logs_df(
        [
            {
                "service_name": "ts-train-service",
                "level": "WARN",
                "message": (
                    "HikariPool-1 - Failed to validate connection "
                    "com.mysql.cj.jdbc.ConnectionImpl@deadbeef "
                    "(No operations allowed after connection closed.)"
                ),
            },
        ]
    )
    normal = _make_logs_df([])

    n = TrainTicketLogAdapter().enrich(g, _tt_timelines(), abnormal, normal)
    assert n == 1

    src_id = nodes["svc_db"].id
    dst_id = nodes["span_caller"].id
    assert src_id is not None and dst_id is not None
    assert g._graph.has_edge(src_id, dst_id, DepKind.includes)
    metadata = g.data["log_inferred_edges"][(src_id, dst_id)]
    assert isinstance(metadata, LogEvidenceMetadata)
    assert metadata.via == "trainticket"
    assert metadata.explicit_target is True
    assert metadata.abnormal_match_count == 1


def test_trainticket_unique_suspect_fallback_when_no_explicit_name() -> None:
    """SQLException without naming a JDBC driver class falls back to the
    unique infra suspect (mysql is the only ``unavailable`` service).
    """
    g, nodes = _build_tt_mysql_graph()
    abnormal = _make_logs_df(
        [
            {
                "service_name": "ts-train-service",
                "level": "WARN",
                "message": "HikariPool-1 connection acquisition timed out",
            },
        ]
    )
    n = TrainTicketLogAdapter().enrich(g, _tt_timelines(), abnormal, _make_logs_df([]))
    assert n == 1
    metadata = g.data["log_inferred_edges"][(nodes["svc_db"].id, nodes["span_caller"].id)]
    assert metadata.explicit_target is False


def test_trainticket_baseline_subtraction_drops_noise_callers() -> None:
    """Caller has the same SQLException count abnormal+normal — baseline
    diff drops it (not abnormal-elevated). No edges added.
    """
    g, _ = _build_tt_mysql_graph()
    msg = "HikariPool-1 - SQLException reading metadata"
    rows = [{"service_name": "ts-train-service", "level": "WARN", "message": msg}] * 3
    n = TrainTicketLogAdapter().enrich(g, _tt_timelines(), _make_logs_df(rows), _make_logs_df(rows))
    assert n == 0


def test_trainticket_skips_caller_with_zero_anomalous_spans() -> None:
    """Caller has DB-pool errors but every span is HEALTHY — no alarm to
    attach the inferred edge to. Edge count should be 0.
    """
    g, _ = _build_tt_mysql_graph()
    abnormal = _make_logs_df(
        [
            {
                "service_name": "ts-train-service",
                "level": "WARN",
                "message": "HikariPool-1 - Failed to validate connection com.mysql.cj.jdbc.ConnectionImpl@x",
            },
        ]
    )
    healthy_timelines = {
        "container|mysql": _make_timeline("container|mysql", PlaceKind.container, "unavailable"),
        "span|ts-train-service::POST /train": _make_timeline(
            "span|ts-train-service::POST /train", PlaceKind.span, "healthy"
        ),
    }
    n = TrainTicketLogAdapter().enrich(g, healthy_timelines, abnormal, _make_logs_df([]))
    assert n == 0


def test_trainticket_skips_when_direct_call_edge_exists() -> None:
    """If the caller already has a span with a direct ``calls`` edge to
    the alarm span on the target side, we already have a traversable
    path — no inferred edge needed.
    """
    g, nodes = _build_tt_mysql_graph()
    # Add an existing call-graph dependency: svc_db has its own span and
    # there's a calls edge between caller's span and db's span.
    span_db = _add_node(g, PlaceKind.span, "mysql::query")
    _add_edge(g, nodes["svc_db"], span_db, DepKind.includes)
    _add_edge(g, span_db, nodes["span_caller"], DepKind.calls)

    abnormal = _make_logs_df(
        [
            {
                "service_name": "ts-train-service",
                "level": "WARN",
                "message": "HikariPool-1 - Failed to validate connection com.mysql.cj.jdbc.ConnectionImpl@x",
            },
        ]
    )
    n = TrainTicketLogAdapter().enrich(g, _tt_timelines(), abnormal, _make_logs_df([]))
    assert n == 0


def test_trainticket_applies_only_when_ts_prefixed_services_present() -> None:
    """``applies`` is the system-detection gate — sockshop-like logs (no
    ``ts-`` prefix) should make the TT adapter skip itself.
    """
    sockshop_logs = _make_logs_df(
        [
            {
                "service_name": "orders",
                "level": "ERROR",
                "message": "HikariPool-1 - SQLException",
            },
        ]
    )
    assert TrainTicketLogAdapter().applies(sockshop_logs) is False

    tt_logs = _make_logs_df(
        [
            {
                "service_name": "ts-train-service",
                "level": "WARN",
                "message": "HikariPool-1 - SQLException",
            },
        ]
    )
    assert TrainTicketLogAdapter().applies(tt_logs) is True


def test_dispatcher_runs_only_applicable_adapters() -> None:
    """Register a fake non-TT adapter; dispatcher should skip it on TT
    logs and only run TrainTicketLogAdapter.
    """
    saved_registry = get_registered_log_adapters()
    # Mutable counters in an outer scope avoid pyright's "unknown attribute"
    # complaints about dynamically-set class attrs.
    spy_calls: dict[str, int] = {"applies": 0, "enrich": 0}
    try:
        _clear_log_registry_for_tests()

        @register_log_adapter
        class FakeSockShopAdapter(LogDependencyAdapter):
            name: ClassVar[str] = "sockshop_test"

            def applies(self, abnormal_logs: pl.DataFrame) -> bool:
                spy_calls["applies"] += 1
                if "service_name" not in abnormal_logs.columns:
                    return False
                return abnormal_logs.filter(pl.col("service_name") == "orders").height > 0

            def db_pool_error_pattern(self) -> str:
                return r"never matches"

            def enrich(self, graph, timelines, abnormal_logs, normal_logs) -> int:  # type: ignore[override]
                spy_calls["enrich"] += 1
                return 0

        register_log_adapter(TrainTicketLogAdapter)

        g, _nodes = _build_tt_mysql_graph()
        abnormal = _make_logs_df(
            [
                {
                    "service_name": "ts-train-service",
                    "level": "WARN",
                    "message": "HikariPool-1 - Failed to validate connection com.mysql.cj.jdbc.ConnectionImpl@x",
                },
            ]
        )
        n = dispatch_log_adapters(g, _tt_timelines(), abnormal, _make_logs_df([]))
        assert n == 1
        assert spy_calls["applies"] == 1
        assert spy_calls["enrich"] == 0
    finally:
        _clear_log_registry_for_tests()
        for name, cls in saved_registry.items():
            if name not in get_registered_log_adapters():
                register_log_adapter(cls)


def test_dispatcher_returns_zero_on_empty_logs() -> None:
    """Empty abnormal logs short-circuits before invoking any adapter."""
    g, _ = _build_tt_mysql_graph()
    n = dispatch_log_adapters(g, _tt_timelines(), _make_logs_df([]), _make_logs_df([]))
    assert n == 0


def test_trainticket_synthesizes_service_for_unspanned_backend() -> None:
    """The mysql case in real datapacks: no ``service|mysql`` node exists
    (mysql has no OTel spans), but ``pod|mysql-0`` and ``container|mysql``
    do. The adapter must synthesise the missing service node when the
    log message explicitly names the backend.
    """
    g = HyperGraph()
    svc_caller = _add_node(g, PlaceKind.service, "ts-train-service")
    pod_caller = _add_node(g, PlaceKind.pod, "ts-train-service-abc")
    span_caller = _add_node(g, PlaceKind.span, "ts-train-service::POST /train")
    pod_db = _add_node(g, PlaceKind.pod, "mysql-0")
    cont_db = _add_node(g, PlaceKind.container, "mysql")

    _add_edge(g, svc_caller, pod_caller, DepKind.routes_to)
    _add_edge(g, svc_caller, span_caller, DepKind.includes)
    _add_edge(g, pod_db, cont_db, DepKind.runs)

    assert g.get_node_by_name("service|mysql") is None  # precondition

    timelines = {
        "container|mysql": _make_timeline("container|mysql", PlaceKind.container, "unavailable"),
        "span|ts-train-service::POST /train": _make_timeline(
            "span|ts-train-service::POST /train", PlaceKind.span, "erroring"
        ),
    }
    abnormal = _make_logs_df(
        [
            {
                "service_name": "ts-train-service",
                "level": "WARN",
                "message": (
                    "HikariPool-1 - Connection com.mysql.cj.jdbc.ConnectionImpl@x "
                    "marked as broken because of SQLSTATE(08S01)"
                ),
            },
        ]
    )

    n = TrainTicketLogAdapter().enrich(g, timelines, abnormal, _make_logs_df([]))
    assert n == 1

    svc_db = g.get_node_by_name("service|mysql")
    assert svc_db is not None and svc_db.id is not None
    assert g._graph.has_edge(svc_db.id, pod_db.id, DepKind.routes_to)
    assert g._graph.has_edge(svc_db.id, span_caller.id, DepKind.includes)


def test_register_log_adapter_rejects_duplicate() -> None:
    """Two adapters claiming the same ``name`` must error — silent
    masking would let one bug-shadow the other.
    """
    saved_registry = get_registered_log_adapters()
    try:
        _clear_log_registry_for_tests()

        class FirstDup(LogDependencyAdapter):
            name: ClassVar[str] = "dup"

            def applies(self, abnormal_logs: pl.DataFrame) -> bool:
                return False

            def db_pool_error_pattern(self) -> str:
                return r"x"

        class SecondDup(LogDependencyAdapter):
            name: ClassVar[str] = "dup"

            def applies(self, abnormal_logs: pl.DataFrame) -> bool:
                return False

            def db_pool_error_pattern(self) -> str:
                return r"x"

        register_log_adapter(FirstDup)  # type: ignore[type-abstract]
        with pytest.raises(ValueError, match="already registered"):
            register_log_adapter(SecondDup)  # type: ignore[type-abstract]
    finally:
        _clear_log_registry_for_tests()
        for name, cls in saved_registry.items():
            if name not in get_registered_log_adapters():
                register_log_adapter(cls)
