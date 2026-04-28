from abc import ABC, abstractmethod
from collections.abc import Callable

import polars as pl


class Pedestal(ABC):
    """
    Pedestal base class - defines the processing interface for microservice systems

    Each specific system (e.g., train-ticket, sock-shop, etc.) needs to implement this abstract class.
    """

    @property
    @abstractmethod
    def black_list(self) -> list[str]:
        """List of substrings; datapacks containing these in their names will be ignored"""
        ...

    @property
    @abstractmethod
    def name(self) -> str:
        """System name, e.g., 'train-ticket', 'sock-shop'"""
        ...

    @property
    @abstractmethod
    def entrance_service(self) -> str:
        """Name of the entrance service for the system"""
        ...

    @abstractmethod
    def normalize_op_name(self, op_name: pl.Expr) -> pl.Expr:
        """
        Normalize op_name by replacing dynamic parameters with templates.

        Args:
            op_name: Polars expression for original op_name like "GET /api/v1/user/12345"

        Returns:
            Polars expression for normalized op_name like "GET /api/v1/user/{userId}"
        """
        ...

    @abstractmethod
    def normalize_path(self, path: str) -> str:
        """
        Normalize a single path

        Convert paths with dynamic parameters to template form.
        Example: /api/v1/user/12345 -> /api/v1/user/{userId}

        Args:
            path: Original path

        Returns:
            Normalized path
        """
        ...

    @abstractmethod
    def add_op_name(self, traces: pl.LazyFrame) -> pl.LazyFrame:
        """
        Add op_name column to trace data

        op_name is typically a combination of service_name + span_name,
        with path normalization applied.

        Args:
            traces: Original trace data (LazyFrame)

        Returns:
            LazyFrame with op_name column added
        """
        ...

    @property
    def success_codes(self) -> set[str]:
        """HTTP/RPC status codes that count as 'success' for SLO purposes.

        Default ``{"200"}`` matches systems whose REST endpoints uniformly return
        200. Override per system: e.g. sockshop's REST API returns 201 for POSTs,
        otel-demo's browser traffic legitimately returns 3xx redirects, etc.
        """
        return {"200"}

    @property
    def slo_latency_relative_ratio(self) -> float:
        """Abnormal/normal latency ratio above which the entrance SLO is considered
        violated, on top of any absolute thresholds.

        A ratio of 3.0 means 'p99 jumped to 3× of baseline' triggers a flag,
        regardless of whether the absolute latency exceeds the system's hard
        threshold. Combined with ``slo_latency_min_absolute`` to suppress noise on
        sub-millisecond endpoints.
        """
        return 3.0

    @property
    def slo_latency_min_absolute(self) -> float:
        """Minimum abnormal latency (seconds) for the relative-ratio rule to fire.

        Even a 100× ratio on a 0.1ms baseline isn't a user-visible SLO violation.
        Default 100ms — anything below the human-perceivable response-time floor
        is ignored by the relative-ratio detector.
        """
        return 0.1

    @abstractmethod
    def fix_client_spans(self, traces: pl.DataFrame) -> tuple[pl.DataFrame, dict[str, str], dict[str, str]]:
        """
        Fix op_name for client spans

        In distributed tracing, client spans usually only record HTTP methods (GET/POST, etc.),
        and need to extract complete path information from their child spans.

        Args:
            traces: Trace data (DataFrame)

        Returns:
            tuple of:
                - Fixed DataFrame
                - span_id -> op_name mapping
                - span_id -> parent_span_id mapping
        """
        ...


class PedestalRegistry(dict[str, Callable[[], Pedestal]]):
    """
    Pedestal registry

    Manages all registered system processors, supporting:
    - Register new systems
    - Get system processors
    - List all supported systems
    """

    def register(self, name: str, factory: Callable[[], Pedestal]) -> None:
        """
        Register a new Pedestal

        Args:
            name: System name (identifier)
            factory: Factory function to create Pedestal instance
        """
        if name in self:
            raise ValueError(f"Pedestal '{name}' is already registered")
        self[name] = factory

    def get_pedestal(self, name: str) -> Pedestal:
        """
        Get a specific Pedestal instance

        Args:
            name: System name

        Returns:
            Pedestal instance

        Raises:
            KeyError: If system is not registered
        """
        if name not in self:
            raise KeyError(f"Pedestal '{name}' not found. Available pedestals: {list(self.keys())}")
        factory = self[name]
        return factory()

    def list_available(self) -> list[str]:
        """Return all registered system names"""
        return list(self.keys())


# Global registry instance
_GLOBAL_REGISTRY: PedestalRegistry = PedestalRegistry()


def global_pedestal_registry() -> PedestalRegistry:
    """
    Get global Pedestal registry

    Returns:
        Global PedestalRegistry instance
    """
    global _GLOBAL_REGISTRY
    return _GLOBAL_REGISTRY


def register_pedestal(name: str) -> Callable[[type[Pedestal]], type[Pedestal]]:
    """
    Decorator: Register Pedestal class

    Usage:
        @register_pedestal("ts")
        class TrainTicketPedestal(Pedestal):
            ...

    Args:
        name: System name

    Returns:
        Class decorator function
    """

    def decorator(cls: type[Pedestal]) -> type[Pedestal]:
        global_pedestal_registry().register(name, lambda: cls())
        return cls

    return decorator


def get_pedestal(name: str = "ts") -> Pedestal:
    """
    Get Pedestal instance for specified system (convenience function)

    Args:
        name: System name, defaults to "ts"

    Returns:
        Pedestal instance
    """
    return global_pedestal_registry().get_pedestal(name)


# Compatibility API - for smooth migration of existing code
def extract_path(uri: str, system: str = "ts") -> str:
    """
    Extract and normalize path (compatibility API)

    Args:
        uri: Original URI
        system: System name, defaults to "ts"

    Returns:
        Normalized path
    """
    pedestal = get_pedestal(system)
    return pedestal.normalize_path(uri)
