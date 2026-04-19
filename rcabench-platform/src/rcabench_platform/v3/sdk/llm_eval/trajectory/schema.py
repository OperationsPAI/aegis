"""Canonical trajectory data classes.

These are intentionally close to the OpenAI chat-completion message format
because that is what every RL / SFT framework expects as input.

The ``Trajectory`` object is the single source of truth that flows between
evaluation, storage, and training-data export.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Literal


@dataclass
class ToolCall:
    """A single tool/function call issued by the assistant."""

    id: str = ""
    name: str = ""
    arguments: str = ""  # JSON-encoded arguments

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "type": "function",
            "function": {
                "name": self.name,
                "arguments": self.arguments,
            },
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> ToolCall:
        func = d.get("function", {})
        return cls(
            id=d.get("id", ""),
            name=func.get("name", ""),
            arguments=func.get("arguments", ""),
        )


@dataclass
class SubAgentCall:
    """A sub-agent invocation issued by the orchestrator.

    Analogous to ``ToolCall`` but for dispatching work to a sub-agent.
    The sub-agent's full trajectory is stored as a separate
    ``AgentTrajectory`` entry linked by the same ``id``.
    """

    id: str = ""  # unique call id, e.g. task_id
    name: str = ""  # sub-agent type, e.g. "ScoutAgent"
    instructions: str = ""  # task instructions sent to the sub-agent

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "type": "sub_agent",
            "name": self.name,
            "instructions": self.instructions,
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> SubAgentCall:
        return cls(
            id=d.get("id", ""),
            name=d.get("name", ""),
            instructions=d.get("instructions", ""),
        )


@dataclass
class Message:
    """One message in a trajectory.

    Mirrors the OpenAI chat-completion message schema so that
    ``to_dict()`` produces a format consumable by any LLM API.

    Extended with ``sub_agent_calls`` / ``sub_agent_call_id`` to model
    nested multi-agent invocations, analogous to ``tool_calls`` /
    ``tool_call_id`` for tool use.
    """

    role: Literal["system", "user", "assistant", "tool", "sub_agent"] = "user"
    content: str = ""
    tool_calls: list[ToolCall] | None = None
    tool_call_id: str | None = None  # for role="tool"
    name: str | None = None  # optional: tool/function name
    sub_agent_calls: list[SubAgentCall] | None = None  # for role="assistant" dispatching sub-agents
    sub_agent_call_id: str | None = None  # for role="sub_agent" (return value)

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {"role": self.role, "content": self.content}
        if self.tool_calls:
            d["tool_calls"] = [tc.to_dict() for tc in self.tool_calls]
        if self.tool_call_id is not None:
            d["tool_call_id"] = self.tool_call_id
        if self.name is not None:
            d["name"] = self.name
        if self.sub_agent_calls:
            d["sub_agent_calls"] = [sc.to_dict() for sc in self.sub_agent_calls]
        if self.sub_agent_call_id is not None:
            d["sub_agent_call_id"] = self.sub_agent_call_id
        return d

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> Message:
        tool_calls = None
        if "tool_calls" in d and d["tool_calls"]:
            tool_calls = [ToolCall.from_dict(tc) for tc in d["tool_calls"]]
        sub_agent_calls = None
        if "sub_agent_calls" in d and d["sub_agent_calls"]:
            sub_agent_calls = [SubAgentCall.from_dict(sc) for sc in d["sub_agent_calls"]]
        return cls(
            role=d.get("role", "user"),
            content=d.get("content", ""),
            tool_calls=tool_calls,
            tool_call_id=d.get("tool_call_id"),
            name=d.get("name"),
            sub_agent_calls=sub_agent_calls,
            sub_agent_call_id=d.get("sub_agent_call_id"),
        )


@dataclass
class Turn:
    """One LLM call: the assistant response plus any tool calls and their responses.

    A turn always starts with an assistant message and optionally includes
    the tool call/response cycle that follows.
    """

    messages: list[Message] = field(default_factory=list)
    token_count: int | None = None  # input + output tokens for this turn

    def to_dicts(self) -> list[dict[str, Any]]:
        return [m.to_dict() for m in self.messages]


@dataclass
class AgentTrajectory:
    """Trajectory for one agent within a (possibly multi-agent) run.

    When this trajectory represents a sub-agent invocation, ``sub_agent_call_id``
    links it back to the ``SubAgentCall.id`` in the parent agent's message.
    This allows the parent's conversation to stay clean (dispatch → return)
    while the full sub-agent conversation lives separately.
    """

    agent_name: str = ""
    system_prompt: str = ""
    turns: list[Turn] = field(default_factory=list)
    sub_agent_call_id: str | None = None  # links to SubAgentCall.id in parent

    @property
    def trajectory_id(self) -> str:
        """Derived identifier: ``sub_agent_call_id`` or ``"main"``."""
        return self.sub_agent_call_id or "main"

    def to_messages(self) -> list[dict[str, Any]]:
        """Flatten all turns to a list of OpenAI message dicts."""
        msgs: list[dict[str, Any]] = []
        if self.system_prompt:
            msgs.append({"role": "system", "content": self.system_prompt})
        for turn in self.turns:
            msgs.extend(turn.to_dicts())
        return msgs


@dataclass
class Trajectory:
    """Complete run trajectory — the canonical data format for this framework.

    Contains one or more ``AgentTrajectory`` objects (for multi-agent runs)
    plus optional evaluation metadata.
    """

    agent_trajectories: list[AgentTrajectory] = field(default_factory=list)

    # Populated after evaluation
    reward: float | None = None
    correct: bool | None = None
    metadata: dict[str, Any] = field(default_factory=dict)

    # ---- Serialisation helpers ----

    def to_messages(self) -> list[dict[str, Any]]:
        """Flatten all agent trajectories into a single message list."""
        msgs: list[dict[str, Any]] = []
        for at in self.agent_trajectories:
            msgs.extend(at.to_messages())
        return msgs

    def to_json(self) -> str:
        """Serialise to a JSON string (for DB storage)."""
        return json.dumps(self._to_serializable(), ensure_ascii=False)

    @classmethod
    def from_json(cls, json_str: str) -> Trajectory:
        """Deserialise from a JSON string."""
        data = json.loads(json_str)
        return cls._from_serializable(data)

    # ---- Private ----

    def _to_serializable(self) -> dict[str, Any]:
        trajectories_data: list[dict[str, Any]] = []
        for at in self.agent_trajectories:
            entry: dict[str, Any] = {
                "trajectory_id": at.trajectory_id,
                "agent_name": at.agent_name,
                "messages": at.to_messages(),
            }
            if at.sub_agent_call_id is not None:
                entry["sub_agent_call_id"] = at.sub_agent_call_id
            trajectories_data.append(entry)
        result: dict[str, Any] = {"trajectories": trajectories_data}
        if self.reward is not None:
            result["reward"] = self.reward
        if self.correct is not None:
            result["correct"] = self.correct
        if self.metadata:
            result["metadata"] = self.metadata
        return result

    @classmethod
    def _from_serializable(cls, data: dict[str, Any]) -> Trajectory:
        agent_trajs: list[AgentTrajectory] = []

        for traj_data in data.get("trajectories", []):
            trajectory_id = traj_data.get("trajectory_id", "main")
            agent_name = traj_data.get("agent_name", trajectory_id)
            sub_agent_call_id = traj_data.get("sub_agent_call_id")
            messages = list(traj_data.get("messages", []))

            # Extract system prompt from leading system message
            system_prompt = ""
            if messages and messages[0].get("role") == "system":
                system_prompt = messages[0].get("content", "")
                messages = messages[1:]

            turns = _messages_to_turns(messages)
            if sub_agent_call_id is None and trajectory_id != "main":
                sub_agent_call_id = trajectory_id
            agent_trajs.append(
                AgentTrajectory(
                    agent_name=agent_name,
                    system_prompt=system_prompt,
                    turns=turns,
                    sub_agent_call_id=sub_agent_call_id,
                )
            )

        return cls(
            agent_trajectories=agent_trajs,
            reward=data.get("reward"),
            correct=data.get("correct"),
            metadata=data.get("metadata", {}),
        )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _messages_to_turns(messages: list[dict[str, Any]]) -> list[Turn]:
    """Group a flat message list into Turn objects.

    Each Turn starts with an assistant message and includes subsequent
    tool messages until the next assistant or user message.
    """
    turns: list[Turn] = []
    current_msgs: list[Message] = []

    for msg_dict in messages:
        msg = Message.from_dict(msg_dict)
        role = msg.role

        if role == "system":
            # System messages go into their own turn
            if current_msgs:
                turns.append(Turn(messages=current_msgs))
                current_msgs = []
            turns.append(Turn(messages=[msg]))
        elif role == "user":
            if current_msgs:
                turns.append(Turn(messages=current_msgs))
                current_msgs = []
            turns.append(Turn(messages=[msg]))
        elif role == "assistant":
            if current_msgs:
                turns.append(Turn(messages=current_msgs))
                current_msgs = []
            current_msgs.append(msg)
        elif role in ("tool", "sub_agent"):
            # Tool responses and sub-agent returns belong to the current assistant turn
            current_msgs.append(msg)
        else:
            current_msgs.append(msg)

    if current_msgs:
        turns.append(Turn(messages=current_msgs))

    return turns
