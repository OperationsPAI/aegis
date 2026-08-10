"""InjectionAdapter — seed emitter.

Structural-evidence adapter that turns a ``ResolvedInjection`` into one or
more seed ``Transition`` events. This guarantees the IR has at least one
non-UNKNOWN node even on datapacks where signal adapters produce nothing
(the "N2 / N3 / N4" cases from #163).

Phase 6 makes the seed-state assignment **deterministic and unconditional**
by delegating to ``models/fault_seed.FAULT_TYPE_TO_SEED_TIER``. The adapter
no longer infers a tier from ``fault_category``; the chaos-tool fault
catalog is the single source of truth.

Per-kind specificity is taken from ``ResolvedInjection.start_kind`` so the
adapter respects resolver choices (e.g. ContainerKill may resolve to a
container node even though the fault family is ``pod_lifecycle``).

Unknown fault types do **not** silently emit nothing. Instead we log a
warning and seed the documented default tier
(``UNKNOWN_FAULT_DEFAULT_TIER`` = ``degraded``) so propagation always has
a starting point.

See ``models/fault_seed.py`` for the full mapping table and rationale for
ambiguous cases.
"""

from __future__ import annotations

import logging
from collections.abc import Iterable

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
    START_KIND_TO_PLACE_KIND,
    canonical_seed_tier,
    pick_canonical_state,
)
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection

logger = logging.getLogger(__name__)


class InjectionAdapter:
    """Seed-emitting structural adapter. Always runs."""

    name = "injection"

    def __init__(self, resolved: ResolvedInjection, injection_at: int) -> None:
        self._resolved = resolved
        self._at = injection_at

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        r = self._resolved
        if r.root_candidates:
            for candidate in r.root_candidates:
                yield from self._emit_seed(
                    node_key=candidate.node,
                    start_kind=candidate.start_kind,
                    fault_type_name=candidate.fault_type_name,
                    fault_category=candidate.fault_category,
                )
            return

        for node_key in r.injection_nodes:
            yield from self._emit_seed(
                node_key=node_key,
                start_kind=r.start_kind,
                fault_type_name=r.fault_type_name,
                fault_category=r.fault_category,
            )

    def _emit_seed(
        self,
        *,
        node_key: str,
        start_kind: str,
        fault_type_name: str,
        fault_category: str,
    ) -> Iterable[Transition]:
        kind = START_KIND_TO_PLACE_KIND.get(start_kind)
        if kind is None:
            logger.warning(
                "InjectionAdapter: unknown start_kind=%r for fault %s; emitting no seed",
                start_kind,
                fault_type_name,
            )
            return

        tier, is_known = canonical_seed_tier(fault_type_name)
        if not is_known:
            logger.warning(
                "InjectionAdapter: unknown fault_type_name=%r (category=%s); "
                "seeding default tier %r so propagation has a starting point",
                fault_type_name,
                fault_category,
                tier,
            )

        to_state = pick_canonical_state(kind, tier)
        evidence: Evidence = {
            "specialization_labels": frozenset({fault_type_name}),
        }

        yield Transition(
            node_key=node_key,
            kind=kind,
            at=self._at,
            from_state="unknown",
            to_state=to_state,
            trigger=f"fault:{fault_type_name}",
            level=EvidenceLevel.structural,
            evidence=evidence,
        )
