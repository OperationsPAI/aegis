"""Agent abstractions and registry for the llm_eval evaluation framework.

Public API
----------
.. autoclass:: BaseAgent
.. autoclass:: AgentResult
.. autoclass:: CLIAgent
.. autoclass:: AgentRegistry
"""

from __future__ import annotations

from typing import Any

from .base_agent import AgentResult, BaseAgent, RunContext
from .cli_agent import CLIAgent

__all__ = [
    "AgentResult",
    "BaseAgent",
    "CLIAgent",
    "RunContext",
    "AgentRegistry",
    "AGENT_REGISTRY",
]


class AgentRegistry:
    """Simple agent registry with factory capabilities.

    Agents can register themselves in two ways:

    1. **Programmatic**: ``AGENT_REGISTRY.register(MyAgent)``
    2. **Entry point** (auto-discovered at first use): packages declare
       ``[project.entry-points."llm_eval.agents"]`` in their
       ``pyproject.toml``::

           [project.entry-points."llm_eval.agents"]
           agentm = "agentm.agents.eval_agent:AgentMAgent"

    Usage::

        AGENT_REGISTRY.list_agents()  # ['cli', 'agentm', ...]
        agent = AGENT_REGISTRY.get("agentm", scenario_dir="...")
    """

    def __init__(self) -> None:
        self._registry: dict[str, type[BaseAgent]] = {}
        self._discovered = False

    def _discover_entry_points(self) -> None:
        """Auto-discover agents declared via ``llm_eval.agents`` entry points."""
        if self._discovered:
            return
        self._discovered = True
        try:
            from importlib.metadata import entry_points

            eps = entry_points(group="llm_eval.agents")
            for ep in eps:
                if ep.name not in self._registry:
                    try:
                        agent_cls = ep.load()
                        self._registry[ep.name] = agent_cls
                    except Exception:
                        pass
        except Exception:
            pass

    def register(self, agent_cls: type[BaseAgent]) -> type[BaseAgent]:
        """Register an agent class.  Can also be used as a decorator."""
        self._registry[agent_cls.name()] = agent_cls
        return agent_cls

    def get(self, name: str, **kwargs: Any) -> BaseAgent:
        """Instantiate a registered agent by name.

        Raises:
            ValueError: If *name* is not registered.
        """
        self._discover_entry_points()
        if name not in self._registry:
            available = ", ".join(sorted(self._registry)) or "(none)"
            raise ValueError(f"Unknown agent: {name!r}. Available: {available}")
        return self._registry[name](**kwargs)

    def list_agents(self) -> list[str]:
        """Return sorted list of registered agent names."""
        self._discover_entry_points()
        return sorted(self._registry)

    def get_class(self, name: str) -> type[BaseAgent]:
        """Return the agent class for *name* without instantiating it.

        Raises:
            KeyError: If *name* is not registered.
        """
        self._discover_entry_points()
        return self._registry[name]


# Global singleton — importable from anywhere.
AGENT_REGISTRY = AgentRegistry()

# Register built-in agents.
AGENT_REGISTRY.register(CLIAgent)
