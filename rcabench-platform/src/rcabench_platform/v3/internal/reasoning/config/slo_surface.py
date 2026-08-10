"""SLO surface as an explicit method input.

The SLO surface ``S`` is the set of services whose anomalies count as
SLO violations for the ``E`` (SLOImpact) component of the (L, E, M)
fault decomposition. Until Phase 0.3 this surface was implicit in the
alarm-detection heuristic (root spans on non-loadgen, non-frontend
services), promoted here to a first-class method input the operator can
override per case.

Backward compatibility: ``SLOSurface.default()`` keeps the existing
heuristic in effect (no service filtering beyond what the alarm detector
already does). Callers that need a tighter surface can construct
``SLOSurface(services={...}, source="operator_input")``; the alarm-
filtering logic in ``cli.run_single_case`` then keeps only spans whose
owning service is in that set.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal


@dataclass(frozen=True, slots=True)
class SLOSurface:
    """Set of services whose anomalies count as SLO violations.

    ``services`` is the explicit surface; an empty set with
    ``source="default_heuristic"`` means "fall back to the alarm detector's
    built-in heuristic" (this is the default).

    ``source`` is recorded on every ``SLOImpact.evidence`` so the paper
    audit can distinguish operator-supplied surfaces from defaults.
    """

    services: frozenset[str]
    source: Literal["operator_input", "default_heuristic"] = "default_heuristic"

    @classmethod
    def default(cls) -> SLOSurface:
        return cls(services=frozenset(), source="default_heuristic")

    def is_default(self) -> bool:
        return self.source == "default_heuristic" and not self.services

    def filter_alarms(self, alarm_span_to_service: dict[str, str]) -> set[str]:
        """Filter a {span_name: service_name} mapping to spans on surface services.

        For ``is_default()`` the input mapping is returned unfiltered (the
        alarm detector's existing exclusion of loadgens / callers is already
        applied upstream). For an explicit surface, only spans whose service
        is in ``self.services`` survive.
        """
        if self.is_default():
            return set(alarm_span_to_service.keys())
        return {span for span, svc in alarm_span_to_service.items() if svc in self.services}
