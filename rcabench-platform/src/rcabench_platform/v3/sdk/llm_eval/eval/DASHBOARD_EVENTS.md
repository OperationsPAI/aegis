# Dashboard WebSocket Event Protocol

The eval dashboard frontend communicates with the backend via a single WebSocket
connection (`/ws`). All real-time data flows through this channel.

## Connection lifecycle

1. Client connects to `ws://{host}/ws`
2. Server sends **initial snapshot** (eval state + buffered sample events)
3. Server pushes events in real-time as they occur
4. Client auto-reconnects on disconnect (exponential backoff)

## Event envelope

Every WebSocket message is a JSON object with these common fields:

```json
{
  "channel": "eval",
  "event_type": "<type>",
  "data": { ... },
  "timestamp": "2025-01-01T00:00:00.000000"
}
```

| Field        | Type   | Description                           |
|--------------|--------|---------------------------------------|
| `channel`    | string | Always `"eval"` for eval dashboard    |
| `event_type` | string | Event type (see below)                |
| `data`       | object | Event-specific payload                |
| `timestamp`  | string | ISO 8601 timestamp                    |

## Event types

### `eval_snapshot`

Sent once on WebSocket connect. Contains the full current state.

```json
{
  "channel": "eval",
  "event_type": "eval_snapshot",
  "data": {
    "summary": { "total": 10, "completed": 3, "running": 1, ... },
    "samples": [ { "sample_id": "...", "status": "running", ... } ],
    "total": 10
  }
}
```

### `sample_status`

Pushed when a sample's status changes (pending -> running -> completed/failed).

```json
{
  "channel": "eval",
  "event_type": "sample_status",
  "data": {
    "sample_id": "case-001",
    "status": "running",
    "started_at": "...",
    "run_id": "...",
    "trajectory_path": "..."
  },
  "summary": { "total": 10, "completed": 3, "running": 2, ... }
}
```

Source: `EvalTracker.mark_running()` / `mark_completed()` / `mark_failed()`

### `sample_trajectory_event`

Pushed in real-time as the agent executes. Contains individual trajectory events
(LLM calls, tool calls, etc.) for a specific sample.

```json
{
  "channel": "eval",
  "event_type": "sample_trajectory_event",
  "sample_id": "case-001",
  "data": {
    "event_type": "tool_call",
    "agent_path": ["orchestrator"],
    "data": { "tool_name": "duckdb_sql", "args": { ... } },
    "timestamp": "..."
  }
}
```

The inner `data` object has the same schema as trajectory JSONL events:

| Inner field    | Description                                    |
|----------------|------------------------------------------------|
| `event_type`   | `llm_start`, `llm_end`, `tool_call`, `tool_result`, `task_dispatch`, `task_complete`, etc. |
| `agent_path`   | Agent hierarchy path, e.g. `["orchestrator", "worker-1"]` |
| `data`         | Event-specific payload (tool args, LLM content, etc.) |
| `timestamp`    | When the event occurred                        |

## Integration guide

### Backend: wiring trajectory events to the dashboard

To push agent execution events to the dashboard, broadcast them through the
`Broadcaster` with the unified `channel: "eval"` format:

```python
async def _traj_to_ws(event: dict) -> None:
    traj_event = {
        "event_type": event.get("event_type", ""),
        "agent_path": event.get("agent_path", []),
        "data": event.get("data", {}),
        "timestamp": event.get("timestamp", ""),
    }
    await broadcaster.broadcast({
        "channel": "eval",
        "event_type": "sample_trajectory_event",
        "sample_id": sample_id,
        "data": traj_event,
    })

system.trajectory.add_listener(_traj_to_ws)
```

The `Broadcaster` automatically buffers `sample_trajectory_event` events in
memory. When a new client connects, the REST fallback endpoint
(`/api/eval/samples/{id}/events`) returns buffered events so the client catches
up immediately.

### Frontend: consuming events

The `EvalSampleDetail` component receives trajectory events via the
`eval_event` CustomEvent on `window`:

```javascript
// Dispatched by the app-level WebSocket handler for all channel="eval" events
window.addEventListener('eval_event', (e) => {
  const event = e.detail;
  if (event.event_type === 'sample_trajectory_event' && event.sample_id === currentSampleId) {
    // Append event.data to the timeline
  }
});
```

On mount, it also makes a one-time REST fetch to `/api/eval/samples/{id}/events`
for catching up on events that were broadcast before the component mounted.

### REST fallback endpoints (read-only)

These endpoints remain available for debugging and catch-up:

| Endpoint                                | Description                   |
|-----------------------------------------|-------------------------------|
| `GET /api/eval/status`                  | Summary counts                |
| `GET /api/eval/samples`                 | Paginated sample list         |
| `GET /api/eval/samples/{id}`            | Sample detail info            |
| `GET /api/eval/samples/{id}/events`     | Trajectory events (from memory buffer, fallback to JSONL file) |
