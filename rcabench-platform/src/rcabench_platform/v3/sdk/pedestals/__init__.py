from . import train_ticket  # noqa: F401  # Auto-import to trigger registration
from .registry import (
    Pedestal,
    PedestalRegistry,
    extract_path,
    get_pedestal,
    global_pedestal_registry,
    register_pedestal,
)

__all__ = [
    # Core classes and interfaces
    "Pedestal",
    "PedestalRegistry",
    # Registry related
    "register_pedestal",
    "global_pedestal_registry",
    # Convenience functions
    "get_pedestal",
    "extract_path",
]
