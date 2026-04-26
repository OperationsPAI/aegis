"""Evidence schema attached to every Transition.

``EvidenceLevel`` answers "how do we know?" — it propagates downstream so
the rule matcher / exporter can distinguish fact from hypothesis.

``Evidence`` itself is a closed ``TypedDict``. Keep it closed (no open
``dict[str, Any]``) until a concrete adapter proves we need more fields;
narrowing later is harder than extending.
"""

from __future__ import annotations

from enum import auto

from typing_extensions import NotRequired, TypedDict

from rcabench_platform.compat import StrEnum


class EvidenceLevel(StrEnum):
    """How a state transition was derived.

    - ``observed``: a signal adapter saw the metric/trace/log breach directly.
    - ``inferred``: an inference rule rewrote UNKNOWN from neighbour evidence.
    - ``structural``: asserted from datapack structure, e.g. injection.json
      fault declaration; no raw signal looked at.
    """

    observed = auto()
    inferred = auto()
    structural = auto()


class Evidence(TypedDict, total=False):
    """Structured evidence payload.

    All fields are ``NotRequired`` — structural-level transitions (e.g. seed
    from ``InjectionAdapter``) typically only populate ``specialization_labels``
    with the fault_type name, while observed-level transitions from a metric
    adapter will set ``trigger_metric`` / ``observed`` / ``threshold``.
    """

    trigger_metric: NotRequired[str]
    observed: NotRequired[float]
    threshold: NotRequired[float]
    specialization_labels: NotRequired[frozenset[str]]
    # Demoted same-(entity, time) transitions whose ``to_state`` lost the
    # intra-tier precedence tie-break (see ``states.intra_tier_precedence``
    # and ``docs/reasoning-feature-taxonomy.md`` §7.1). Each entry is a
    # ``(loser_to_state, loser_evidence)`` pair so downstream rules can still
    # see that the lower-precedence signal was observed.
    shadowed: NotRequired[tuple[tuple[str, "Evidence"], ...]]
