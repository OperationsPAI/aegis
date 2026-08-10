"""Reasoning-pipeline configuration types.

Method inputs that callers can override (e.g. SLO surface) live here. Adapter-
internal thresholds remain inside their adapters; this module is for the
externally-facing knobs the paper §3 promotes to first-class arguments.
"""

from rcabench_platform.v3.internal.reasoning.config.slo_surface import SLOSurface

__all__ = ["SLOSurface"]
