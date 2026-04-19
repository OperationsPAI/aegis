from abc import ABC, abstractmethod
from collections.abc import Callable
from dataclasses import dataclass
from enum import Enum, auto
from pathlib import Path

from ...compat import StrEnum


class SamplingMode(StrEnum):
    """Sampling mode enumeration."""

    ONLINE = auto()  # Flexible sampling: uses sampling rate as guideline but algorithm can adjust
    OFFLINE = auto()  # Strict sampling: limited by exact sampling rate


@dataclass(kw_only=True, frozen=True, slots=True)
class SamplerArgs:
    """Arguments for trace sampler algorithms."""

    dataset: str
    datapack: str
    input_folder: Path
    output_folder: Path
    sampling_rate: float  # Sampling rate between 0.0 and 1.0
    mode: SamplingMode


@dataclass(kw_only=True, frozen=True, slots=True)
class SampleResult:
    """Result of trace sampling containing trace_id and its sample score."""

    trace_id: str
    sample_score: float  # Weight/score for sampling this trace


class TraceSampler(ABC):
    """Abstract base class for trace sampling algorithms."""

    @abstractmethod
    def needs_cpu_count(self) -> int | None:
        """
        Returns the number of CPU cores needed by the sampler.

        The return value must be positive or None.

        Examples:
        - 1:    the sampler needs a single core.
        - 4:    the sampler needs 4 cores.
        - None: the sampler needs all available cores.

        This is used to automatically determine how many sampler instances can be run in parallel.
        """
        ...

    @abstractmethod
    def __call__(self, args: SamplerArgs) -> list[SampleResult]:
        """
        Execute the trace sampling algorithm.

        Args:
            args: Sampler arguments containing dataset, datapack, folders, sampling rate and mode

        Returns:
            List of SampleResult containing trace_id and sample_score.

            For online mode: Uses sampling rate as guideline but algorithm can adjust count.
            For offline mode: Returns traces sorted by score, strictly limited by sampling_rate.
        """
        ...


class SamplerRegistry(dict[str, Callable[[], TraceSampler]]):
    """Registry for trace sampler algorithms."""

    pass


_GLOBAL_SAMPLER_REGISTRY: SamplerRegistry = SamplerRegistry()


def global_sampler_registry() -> SamplerRegistry:
    """
    Returns the global sampler registry.

    The registry is a dictionary mapping sampler names to their getter functions.
    """
    global _GLOBAL_SAMPLER_REGISTRY
    return _GLOBAL_SAMPLER_REGISTRY


def set_global_sampler_registry(registry: SamplerRegistry):
    """Set the global sampler registry."""
    global _GLOBAL_SAMPLER_REGISTRY
    _GLOBAL_SAMPLER_REGISTRY = registry


def register_sampler(name: str, sampler_factory: Callable[[], TraceSampler]):
    """Register a sampler algorithm."""
    registry = global_sampler_registry()
    registry[name] = sampler_factory
