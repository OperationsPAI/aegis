"""Every IR enum must define UNKNOWN and share pivot names where applicable."""

from __future__ import annotations

import pytest

from rcabench_platform.v3.internal.reasoning.ir.states import (
    ContainerStateIR,
    PodStateIR,
    ServiceStateIR,
    SpanStateIR,
    severity,
)

ALL_KINDS = [SpanStateIR, ServiceStateIR, PodStateIR, ContainerStateIR]


@pytest.mark.parametrize("enum_cls", ALL_KINDS)
def test_unknown_is_first_class(enum_cls: type) -> None:
    assert "UNKNOWN" in enum_cls.__members__
    assert enum_cls.UNKNOWN.value == "unknown"


@pytest.mark.parametrize("enum_cls", ALL_KINDS)
def test_healthy_present(enum_cls: type) -> None:
    assert "HEALTHY" in enum_cls.__members__


def test_span_has_missing() -> None:
    assert "MISSING" in SpanStateIR.__members__


def test_pod_has_restarting() -> None:
    assert "RESTARTING" in PodStateIR.__members__


def test_severity_monotonic() -> None:
    assert severity("unknown") < severity("healthy") < severity("slow")
    assert severity("slow") < severity("erroring") < severity("unavailable")
    assert severity("erroring") < severity("missing")
    assert severity("degraded") >= severity("slow")


def test_severity_unknown_state_is_lowest() -> None:
    assert severity("not-a-real-state") == 0


def test_span_has_silent() -> None:
    assert "SILENT" in SpanStateIR.__members__


def test_service_has_silent() -> None:
    assert "SILENT" in ServiceStateIR.__members__


def test_pod_has_no_silent() -> None:
    assert "SILENT" not in PodStateIR.__members__


def test_container_has_no_silent() -> None:
    assert "SILENT" not in ContainerStateIR.__members__


def test_silent_severity_equals_erroring() -> None:
    assert severity("silent") == severity("erroring") == 4


def test_severity_silent_ordering() -> None:
    assert severity("degraded") < severity("silent")
    assert severity("silent") < severity("unavailable")
    assert severity("silent") < severity("missing")
