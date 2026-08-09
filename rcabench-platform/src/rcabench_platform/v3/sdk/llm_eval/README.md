# LLM Eval SDK — Agent Evaluation Framework

A framework for evaluating LLM-based agents on RCA (Root Cause Analysis) benchmarks. Provides a unified CLI to run any registered agent through a standardized pipeline: **preprocess → rollout → judge → stat**.

## Quick Start

```bash
# List available agents
rca llm-eval agents

# Full pipeline with an agent
rca llm-eval run config.yaml \
  -a agentm \
  --ak scenario_dir=config/scenarios/rca_hypothesis \
  --ak config_path=config/system.yaml \
  --source-path /mnt/jfs/rcabench_dataset \
  -n 4 \
  --dashboard

# CLI agent (wrap any command)
rca llm-eval run config.yaml \
  -a cli \
  --ak 'command_template=claude -p -- "$(cat {instruction_file})"' \
  --source-path /mnt/jfs/rcabench_dataset

# Without --agent: legacy mode (preprocess + judge + stat only)
rca llm-eval run config.yaml

# Re-judge / show stats
rca llm-eval judge config.yaml
rca llm-eval stat config.yaml
```

## Adding a New Agent

### Step 1: Implement `BaseAgent`

Create a class that extends `BaseAgent` with two required methods: `name()` and `run()`.

```python
from rcabench_platform.v3.sdk.llm_eval.agents.base_agent import (
    AgentResult,
    BaseAgent,
    RunContext,
)
from rcabench_platform.v3.sdk.llm_eval.trajectory.schema import Trajectory


class MyAgent(BaseAgent):
    """My custom RCA agent."""

    def __init__(self, model: str = "gpt-4o", **kwargs):
        # Constructor receives --ak key=value pairs from the CLI.
        self._model = model

    @staticmethod
    def name() -> str:
        # Unique identifier used with `--agent my-agent`.
        return "my-agent"

    def version(self) -> str | None:
        # Optional. Shown in `rca llm-eval agents`.
        return "0.1.0"

    async def run(
        self,
        incident: str,
        data_dir: str,
        **kwargs,
    ) -> AgentResult:
        ctx: RunContext | None = kwargs.get("ctx")
        run_id = "my-run-abc123"

        # Notify the framework that execution has started.
        if ctx:
            ctx.emit({"type": "running", "run_id": run_id})

        # --- Your investigation logic here ---
        response = '{"root_cause": "service-a", "evidence": "..."}'

        return AgentResult(
            response=response,
            trajectory=None,     # or a Trajectory object (see below)
            metadata={"run_id": run_id},
        )
```

### Step 2: Register via Entry Point

Add to your project's `pyproject.toml`:

```toml
[project.entry-points."llm_eval.agents"]
my-agent = "my_package.agents:MyAgent"
```

Then install (e.g. `uv sync` or `pip install -e .`). The agent is auto-discovered at runtime — the platform never imports your package directly.

### Step 3: Verify

```bash
rca llm-eval agents
# ┌────────────┬──────────────────────────────┐
# │ Name       │ Module                       │
# ├────────────┼──────────────────────────────┤
# │ cli        │ ...cli_agent.CLIAgent        │
# │ my-agent   │ my_package.agents.MyAgent    │
# └────────────┴──────────────────────────────┘
```

## Agent Contract Reference

### `BaseAgent` (abstract)

| Method | Required | Signature | Description |
|--------|----------|-----------|-------------|
| `name()` | **Yes** | `@staticmethod -> str` | Unique agent identifier |
| `run()` | **Yes** | `async (incident, data_dir, **kwargs) -> AgentResult` | Execute investigation |
| `version()` | No | `-> str \| None` | Version string |
| `setup()` | No | `async (**kwargs) -> None` | One-time init before first `run()` |
| `teardown()` | No | `async () -> None` | Cleanup after all runs complete |

### `run()` Parameters

| Parameter | Type | Source |
|-----------|------|--------|
| `incident` | `str` | Incident description from the dataset preprocessor |
| `data_dir` | `str` | Absolute path to observability data (parquet files, etc.) |
| `ctx` | `RunContext` | Event emitter for progress reporting (always provided via `**kwargs`) |
| `max_steps` | `int` | From CLI `--max-steps` (via `**kwargs`) |
| `timeout` | `float` | From CLI `--timeout` in seconds (via `**kwargs`) |

### `AgentResult`

| Field | Type | Description |
|-------|------|-------------|
| `response` | `str` | Structured output (e.g. JSON causal graph). **Required** — passed to the judge. |
| `trajectory` | `Trajectory \| None` | Standardized trajectory for storage and training export. |
| `metadata` | `dict` | Arbitrary extra info (run_id, token counts, cost, …). |

### `RunContext` Events

Agents emit events via `ctx.emit(event_dict)` to report progress. The framework handles lifecycle events (`started`, `completed`, `failed`, `skipped`) automatically — agents should only emit:

| Event Type | Fields | Purpose |
|------------|--------|---------|
| `running` | `{"type": "running", "run_id": "..."}` | Notify that execution started; triggers tracker to record trajectory path |
| `trajectory_update` | `{"type": "trajectory_update", "path": "/path/to/traj.jsonl"}` | Update the trajectory file path (for dashboard live view) |
| `progress` | `{"type": "progress", "message": "..."}` | Optional progress update |

## Trajectory Format

The `Trajectory` object is the canonical data format for agent execution traces. It supports both single-agent and multi-agent systems.

### Single-Agent Trajectory

```python
from rcabench_platform.v3.sdk.llm_eval.trajectory.schema import (
    AgentTrajectory,
    Message,
    ToolCall,
    Trajectory,
    Turn,
)

trajectory = Trajectory(
    agent_trajectories=[
        AgentTrajectory(
            agent_name="my-agent",
            system_prompt="You are an RCA assistant.",
            turns=[
                Turn(messages=[Message(role="user", content="Investigate...")]),
                Turn(messages=[
                    Message(
                        role="assistant",
                        content="",
                        tool_calls=[ToolCall(
                            id="call_1",
                            name="query_metrics",
                            arguments='{"service": "api-gateway"}',
                        )],
                    ),
                    Message(
                        role="tool",
                        content='{"latency_p99": 1200}',
                        tool_call_id="call_1",
                        name="query_metrics",
                    ),
                ]),
                Turn(messages=[
                    Message(role="assistant", content="Root cause is..."),
                ]),
            ],
        )
    ]
)

json_str = trajectory.to_json()  # serialize
restored = Trajectory.from_json(json_str)  # deserialize
```

**Serialized JSON:**

```json
{
  "trajectories": [
    {
      "trajectory_id": "main",
      "agent_name": "my-agent",
      "messages": [
        {"role": "system", "content": "You are an RCA assistant."},
        {"role": "user", "content": "Investigate..."},
        {"role": "assistant", "content": "", "tool_calls": [
          {"id": "call_1", "type": "function", "function": {"name": "query_metrics", "arguments": "..."}}
        ]},
        {"role": "tool", "content": "{\"latency_p99\": 1200}", "tool_call_id": "call_1", "name": "query_metrics"},
        {"role": "assistant", "content": "Root cause is..."}
      ]
    }
  ]
}
```

### Multi-Agent Trajectory

For multi-agent systems (e.g. orchestrator + workers), each agent gets its own `AgentTrajectory`. Sub-agent invocations are modeled with `SubAgentCall` / `sub_agent_call_id`, analogous to `ToolCall` / `tool_call_id`:

```python
trajectory = Trajectory(
    agent_trajectories=[
        # Orchestrator trajectory (trajectory_id="main")
        AgentTrajectory(
            agent_name="orchestrator",
            system_prompt="You coordinate RCA investigations.",
            turns=[
                Turn(messages=[Message(role="user", content="Investigate...")]),
                Turn(messages=[
                    Message(
                        role="assistant",
                        content="Dispatching scout to check metrics.",
                        sub_agent_calls=[SubAgentCall(
                            id="task-001",
                            name="ScoutAgent",
                            instructions="Check latency for api-gateway",
                        )],
                    ),
                    Message(
                        role="sub_agent",
                        content='{"finding": "p99 latency spike at 14:32"}',
                        sub_agent_call_id="task-001",
                    ),
                ]),
                Turn(messages=[
                    Message(role="assistant", content="Root cause identified."),
                ]),
            ],
        ),
        # Sub-agent trajectory (linked by sub_agent_call_id)
        AgentTrajectory(
            agent_name="scout",
            system_prompt="You are a metrics scout.",
            sub_agent_call_id="task-001",  # links to SubAgentCall.id above
            turns=[
                Turn(messages=[
                    Message(role="user", content="Check latency for api-gateway"),
                ]),
                Turn(messages=[
                    Message(
                        role="assistant",
                        content="",
                        tool_calls=[ToolCall(id="c1", name="query_prometheus", arguments="...")],
                    ),
                    Message(role="tool", content="...", tool_call_id="c1", name="query_prometheus"),
                ]),
                Turn(messages=[
                    Message(role="assistant", content='{"finding": "p99 latency spike at 14:32"}'),
                ]),
            ],
        ),
    ]
)
```

**Key points for multi-agent trajectories:**

- The **orchestrator** trajectory has `trajectory_id="main"` (no `sub_agent_call_id`).
- Each **sub-agent** trajectory sets `sub_agent_call_id` to match the `SubAgentCall.id` in the orchestrator's message.
- The orchestrator uses `role="assistant"` with `sub_agent_calls` to dispatch, and receives results via `role="sub_agent"` messages.
- Sub-agents have their own independent conversation (system prompt, tool calls, etc.).
- Nesting is supported — a sub-agent can dispatch its own sub-agents.

**Serialized JSON (multi-agent):**

```json
{
  "trajectories": [
    {
      "trajectory_id": "main",
      "agent_name": "orchestrator",
      "messages": [
        {"role": "system", "content": "You coordinate RCA investigations."},
        {"role": "user", "content": "Investigate..."},
        {"role": "assistant", "content": "Dispatching scout.", "sub_agent_calls": [
          {"id": "task-001", "type": "sub_agent", "name": "ScoutAgent", "instructions": "Check latency..."}
        ]},
        {"role": "sub_agent", "content": "{\"finding\": \"...\"}", "sub_agent_call_id": "task-001"},
        {"role": "assistant", "content": "Root cause identified."}
      ]
    },
    {
      "trajectory_id": "task-001",
      "agent_name": "scout",
      "sub_agent_call_id": "task-001",
      "messages": [
        {"role": "system", "content": "You are a metrics scout."},
        {"role": "user", "content": "Check latency for api-gateway"},
        {"role": "assistant", "content": "", "tool_calls": [...]},
        {"role": "tool", "content": "...", "tool_call_id": "c1"},
        {"role": "assistant", "content": "{\"finding\": \"...\"}"}
      ]
    }
  ]
}
```

## Dashboard JSONL Format (Optional)

For real-time dashboard visualization, agents can write a JSONL trajectory file and report its path via the `trajectory_update` event. The dashboard reads this file incrementally.

**Format — one JSON object per line:**

```jsonl
{"_meta": {"run_id": "headless-abc123", "thread_id": "..."}}
{"run_id": "abc123", "seq": 1, "timestamp": "2025-01-15T14:32:01", "agent_path": ["orchestrator"], "event_type": "llm_end", "data": {"content": "I will investigate..."}}
{"run_id": "abc123", "seq": 2, "timestamp": "2025-01-15T14:32:03", "agent_path": ["orchestrator"], "event_type": "tool_call", "data": {"tool_name": "query_metrics", "args": {}}}
{"run_id": "abc123", "seq": 3, "timestamp": "2025-01-15T14:32:05", "agent_path": ["orchestrator"], "event_type": "tool_result", "data": {"tool_name": "query_metrics", "result": "..."}}
{"run_id": "abc123", "seq": 4, "timestamp": "2025-01-15T14:32:06", "agent_path": ["orchestrator"], "event_type": "task_dispatch", "data": {"agent_id": "scout-1", "task_id": "t1", "task_type": "metrics_check"}}
{"run_id": "abc123", "seq": 5, "timestamp": "2025-01-15T14:32:07", "agent_path": ["orchestrator", "scout-1", "t1"], "event_type": "tool_call", "data": {"tool_name": "query_prometheus"}}
{"run_id": "abc123", "seq": 6, "timestamp": "2025-01-15T14:32:10", "agent_path": ["orchestrator", "scout-1", "t1"], "event_type": "tool_result", "data": {"tool_name": "query_prometheus", "result": "..."}}
{"run_id": "abc123", "seq": 7, "timestamp": "2025-01-15T14:32:12", "agent_path": ["orchestrator", "scout-1", "t1"], "event_type": "task_complete", "data": {"agent_id": "scout-1", "duration_seconds": 6.0}}
{"run_id": "abc123", "seq": 8, "timestamp": "2025-01-15T14:32:13", "agent_path": ["orchestrator"], "event_type": "llm_end", "data": {"content": "Root cause is..."}}
```

**JSONL field reference:**

| Field | Type | Description |
|-------|------|-------------|
| `_meta` | `object` | Metadata line (skipped by the dashboard). |
| `run_id` | `str` | Run identifier. |
| `seq` | `int` | Monotonically increasing sequence number. |
| `timestamp` | `str` | ISO 8601 timestamp. |
| `agent_path` | `str[]` | Hierarchy path. `["orchestrator"]` for root, `["orchestrator", "agent-id", "task-id"]` for sub-agents. The dashboard uses this to group events into collapsible agent sections. |
| `event_type` | `str` | One of: `llm_start`, `llm_end`, `tool_call`, `tool_result`, `task_dispatch`, `task_complete`, `task_fail`, `task_abort`, `hypothesis_update`. |
| `data` | `object` | Event-specific payload (tool name, content, args, etc.). |

**Multi-agent grouping:**

The dashboard's `buildEventTree()` function uses `agent_path` and `task_dispatch` / `task_complete` events to build a hierarchical view:

- Events with `agent_path: ["orchestrator"]` appear at the root level.
- A `task_dispatch` event with `data.agent_id` creates a collapsible group.
- Subsequent events whose `agent_path` ends with that `agent_id` are nested inside the group.
- `task_complete` / `task_fail` / `task_abort` events close the group and show duration/status.

This is optional — agents that don't write JSONL files simply won't have real-time trajectory visualization in the dashboard, but all other functionality (rollout, judge, stat) works normally.

## Architecture

```
pyproject.toml (entry-point)          CLI
  ┌──────────────────┐        ┌──────────────────────┐
  │ [llm_eval.agents]│        │ rca llm-eval run     │
  │ my = pkg:MyAgent │───────▶│   --agent my         │
  └──────────────────┘        │   --ak key=value     │
                              └──────────┬───────────┘
                                         │
         ┌───────────────────────────────┐│
         │ AgentRegistry                 ││
         │  _discover_entry_points()     │◀┘
         │  .get("my", key="value")      │
         └──────────┬────────────────────┘
                    │ instantiate
                    ▼
         ┌──────────────────────────────┐
         │ MyAgent(key="value")         │
         │  .run(incident, data_dir,    │
         │       ctx=RunContext)         │
         └──────────┬───────────────────┘
                    │ returns AgentResult
                    ▼
         ┌──────────────────────────────┐
         │ BaseBenchmark                │
         │  preprocess()                │
         │  rollout(agent, on_event)    │──▶ EvalTracker ──▶ Dashboard (WebSocket)
         │  judge()                     │
         │  stat()                      │
         └──────────────────────────────┘
```

## Custom Source Path Resolution

By default, when `source_path` is set (via config or CLI `--source-path`), the framework resolves each sample's data directory as:

```
{source_path}/{source}/converted
```

You can customize this in two ways: **YAML pattern** (config-driven) or **SDK function** (code-driven).

### Option 1: YAML `source_path_pattern` (config-driven)

Set `source_path_pattern` in your YAML config. Available placeholders: `{source_path}`, `{source}`.

```yaml
# config.yaml
source_path: /mnt/jfs/rcabench_dataset
source_path_pattern: "{source_path}/{source}/processed"
# resolved as: /mnt/jfs/rcabench_dataset/RCABench/processed
```

If `source_path_pattern` is omitted, the default `{source_path}/{source}/converted` is used.

### Option 2: SDK `source_path_fn` (code-driven)

Pass a callable to `EvalConfig` for full control. This cannot be set from YAML.

```python
from rcabench_platform.v3.sdk.llm_eval.config import EvalConfig
from rcabench_platform.v3.sdk.llm_eval.eval import BaseBenchmark

# Simple pattern
config = EvalConfig(
    source_path_fn=lambda source: f"/my/data/{source}/processed",
)

# Complex logic
def resolve(source: str) -> str:
    mapping = {"RCABench": "/data/v2/rca", "OtherBench": "/data/v1/other"}
    return mapping[source]

config = EvalConfig(source_path_fn=resolve)

benchmark = BaseBenchmark(config)
```

### Priority

```
BaseBenchmark(config, source_path_fn=fn)   # 1. explicit constructor arg (highest)
config.source_path_fn                       # 2. SDK callable
config.source_path_pattern + source_path    # 3. YAML pattern template
config.source_path (default pattern)        # 4. auto-built: {source_path}/{source}/converted
None                                        # 5. fallback (uses sample meta fields)
```

### CLI

From the CLI, `--source-path` sets `config.source_path`. Combine with `source_path_pattern` in YAML to customize:

```yaml
# config.yaml — CLI --source-path overrides source_path here
source_path: /mnt/jfs/rcabench_dataset
source_path_pattern: "{source_path}/{source}/processed"
```

```bash
# Uses pattern from YAML with path from CLI
rca llm-eval run config.yaml -a my-agent --source-path /other/data
# resolved as: /other/data/RCABench/processed
```
