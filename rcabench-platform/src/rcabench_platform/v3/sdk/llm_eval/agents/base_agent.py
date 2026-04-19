"""Base agent abstraction for RCA evaluation.

All agents implement :class:`BaseAgent` so that :class:`BaseBenchmark` can
drive rollout without per-agent glue code.  The framework handles sample
parsing, error handling, trajectory serialisation, and stage transitions;
the agent only implements :meth:`run`.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from ..trajectory.schema import Trajectory


@dataclass
class AgentResult:
    """Result of a single agent execution.

    Attributes:
        response: Structured output (e.g. CausalGraph JSON string).
        trajectory: Standardised trajectory for the run.
        metadata: Arbitrary extra information (token counts, cost, …).
    """

    response: str = ""
    trajectory: Trajectory | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


class RunContext:
    """Lightweight event emitter passed to :meth:`BaseAgent.run`.

    Agents use this to signal progress and stream real-time metadata
    (trajectory path, intermediate status, …) back to the framework.
    Agents that don't need it can simply ignore the parameter.

    The framework (or caller) attaches listeners before calling
    ``agent.run()``.  Events are plain dicts — no fixed schema so that
    agents can emit whatever is useful.

    Built-in event types (by convention):

    * ``{"type": "running", "run_id": "..."}``
    * ``{"type": "trajectory_update", "path": "/tmp/..."}``
    * ``{"type": "progress", "message": "Querying metrics..."}``

    Example (inside an agent)::

        async def run(self, incident, data_dir, *, ctx, **kw):
            ctx.emit({"type": "running", "run_id": my_run_id})
            # … do work …
            ctx.emit({"type": "trajectory_update", "path": traj_path})
            return AgentResult(...)

    Example (caller side)::

        ctx = RunContext()
        ctx.add_listener(lambda evt: tracker.on_event(evt))
        await agent.run(incident, data_dir, ctx=ctx)
    """

    def __init__(self) -> None:
        self._listeners: list[Callable[[dict[str, Any]], Any]] = []

    def add_listener(self, callback: Callable[[dict[str, Any]], Any]) -> None:
        """Register a callback invoked on every :meth:`emit`."""
        self._listeners.append(callback)

    def emit(self, event: dict[str, Any]) -> None:
        """Broadcast *event* to all registered listeners."""
        for listener in self._listeners:
            try:
                listener(event)
            except Exception:  # noqa: BLE001
                pass


class BaseAgent(ABC):
    """Abstract base class for RCA evaluation agents.

    Subclasses must implement :meth:`name` and :meth:`run`.  Optional
    lifecycle hooks :meth:`setup` / :meth:`teardown` are available for
    one-time initialisation and cleanup.

    Example::

        class MyAgent(BaseAgent):
            @staticmethod
            def name() -> str:
                return "my-agent"

            async def run(self, incident, data_dir, **kw) -> AgentResult:
                # investigate the incident …
                return AgentResult(response=causal_graph_json)
    """

    @staticmethod
    @abstractmethod
    def name() -> str:
        """Unique agent identifier, e.g. ``'agentm'``, ``'claude-code'``."""

    def version(self) -> str | None:
        """Agent version string (optional)."""
        return None

    def model_name(self) -> str | None:
        """LLM model name used by this agent (optional).

        When the agent knows which model it uses (e.g. from its own config
        or environment variables), it should return the model name here.
        The framework uses this to auto-fill ``EvalConfig.model_name`` when
        the config doesn't specify one, so that DB records are correctly
        labelled without requiring YAML duplication.
        """
        return None

    @abstractmethod
    async def run(
        self,
        incident: str,
        data_dir: str,
        **kwargs: Any,
    ) -> AgentResult:
        """Execute an RCA investigation.

        Args:
            incident: Incident description including alarm endpoints and
                any other contextual information produced by the preprocessor.
            data_dir: Absolute path to the observability data directory
                containing parquet files, ``causal_graph.json``, etc.
            **kwargs: Agent-specific parameters.  The framework always passes
                ``ctx: RunContext`` — agents may use it to emit real-time
                events (progress, trajectory updates, …) or ignore it.

        Returns:
            An :class:`AgentResult` with the structured response and,
            optionally, a :class:`Trajectory`.
        """

    async def setup(self, **kwargs: Any) -> None:
        """One-time initialisation (optional).

        Called once before the first :meth:`run` invocation within a
        benchmark session.  Override to install dependencies, warm up
        models, etc.
        """

    async def teardown(self) -> None:
        """Cleanup (optional).

        Called once after all :meth:`run` invocations have completed.
        Override to release resources.
        """
