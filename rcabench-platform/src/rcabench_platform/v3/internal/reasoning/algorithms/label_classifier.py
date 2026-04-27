"""Five-class label classifier over the (L, E, M) decomposition.

The classifier is a pure function — no I/O, no side effects. Input semantics
live on the dataclasses in ``models/propagation.py``; this module only
encodes the decision table.
"""

from rcabench_platform.v3.internal.reasoning.models.propagation import (
    LabelT,
    LocalEffect,
    SLOImpact,
)


def classify(L: LocalEffect, E: SLOImpact, has_path: bool) -> tuple[LabelT, str]:
    """Apply the 5-class decision table.

    Returns ``(label, reason)`` where ``reason`` cites the deciding condition.
    ``contaminated`` is reserved for multi-fault post-hoc analysis (Phase 5)
    and is unreachable here.
    """
    if not L.detected:
        return ("ineffective", "L=0: no observable effect at injection point")
    if not E.detected:
        return ("absorbed", "L=1, E=0: fault did not reach SLO surface")
    if has_path:
        return ("attributed", "L=1, E=1, M=path: full causal chain")
    return ("unexplained_impact", "L=1, E=1, M=None: SLO violated, no rule-admitted path")
