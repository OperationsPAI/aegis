"""FastAPI application factory for the eval dashboard.

Provides:
- WebSocket endpoint for real-time event streaming
- REST API for batch evaluation monitoring
- Static HTML dashboard served from ``eval/static/``
"""

from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path
from typing import Any

from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles

from .tracker import EvalTracker

STATIC_DIR = Path(__file__).resolve().parent / "static"


class Broadcaster:
    """Manages WebSocket clients and broadcasts events."""

    def __init__(self) -> None:
        self._clients: set[WebSocket] = set()

    def add(self, ws: WebSocket) -> None:
        self._clients.add(ws)

    def remove(self, ws: WebSocket) -> None:
        self._clients.discard(ws)

    async def broadcast(self, event: dict[str, Any]) -> None:
        to_send = {**event}  # avoid mutating caller's dict
        if "timestamp" not in to_send:
            to_send["timestamp"] = datetime.now().isoformat()

        payload = json.dumps(to_send, default=str)
        dead: list[WebSocket] = []
        for ws in list(self._clients):
            try:
                await ws.send_text(payload)
            except (WebSocketDisconnect, ConnectionError, RuntimeError):
                dead.append(ws)
        for ws in dead:
            self._clients.discard(ws)


def load_ground_truth(data_dir: str) -> dict[str, Any] | None:
    """Load ground truth payload from a sample data directory if available."""
    if not data_dir:
        return None
    injection_path = Path(data_dir) / "injection.json"
    if not injection_path.exists():
        return None
    try:
        payload = json.loads(injection_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, TypeError):
        return None
    ground_truth = payload.get("ground_truth")
    if not isinstance(ground_truth, dict):
        return None
    return ground_truth


def create_eval_dashboard(
    eval_tracker: EvalTracker,
    broadcaster: Broadcaster | None = None,
    static_dir: Path | None = None,
) -> FastAPI:
    """Create an eval-only dashboard FastAPI application.

    Endpoints:
    - ``GET /``                         — static HTML dashboard
    - ``WS  /ws``                       — WebSocket real-time push
    - ``GET /api/eval/status``          — summary counts
    - ``GET /api/eval/samples``         — paginated sample list
    - ``GET /api/eval/samples/{id}``    — sample detail + ground truth
    - ``GET /api/eval/samples/{id}/events`` — trajectory JSONL events

    Args:
        eval_tracker: EvalTracker instance for state queries.
        broadcaster: Optional Broadcaster; one is created if not provided.
        static_dir: Override the default static files directory.
    """
    if broadcaster is None:
        broadcaster = Broadcaster()
    if static_dir is None:
        static_dir = STATIC_DIR

    app = FastAPI(title="RCABench Eval Dashboard")
    app.state.eval_tracker = eval_tracker
    app.state.broadcaster = broadcaster

    # ── HTML dashboard ─────────────────────────────────────────────────

    @app.get("/", response_class=HTMLResponse)
    async def serve_dashboard() -> HTMLResponse:
        html_path = static_dir / "index.html"
        return HTMLResponse(html_path.read_text(encoding="utf-8"))

    app.mount("/static", StaticFiles(directory=str(static_dir)), name="static")

    # ── WebSocket ──────────────────────────────────────────────────────

    @app.websocket("/ws")
    async def websocket_endpoint(websocket: WebSocket) -> None:
        await websocket.accept()
        bc = app.state.broadcaster
        bc.add(websocket)
        # Send current eval snapshot on connect
        et = app.state.eval_tracker
        try:
            summary = et.get_summary()
            samples, total = et.get_samples(offset=0, limit=50)
            snapshot = json.dumps(
                {
                    "channel": "eval",
                    "event_type": "eval_snapshot",
                    "data": {"summary": summary, "samples": samples, "total": total},
                    "timestamp": datetime.now().isoformat(),
                },
                default=str,
            )
            await websocket.send_text(snapshot)
        except (WebSocketDisconnect, ConnectionError, RuntimeError):
            bc.remove(websocket)
            return
        try:
            while True:
                await websocket.receive_text()
        except WebSocketDisconnect:
            bc.remove(websocket)

    # ── Eval endpoints ────────────────────────────────────────────────

    @app.get("/api/eval/status")
    async def eval_status() -> dict[str, Any]:
        et = app.state.eval_tracker
        summary = et.get_summary()
        return {"enabled": True, **summary}

    @app.get("/api/eval/samples")
    async def eval_samples(
        offset: int = 0,
        limit: int = 50,
        status: str | None = None,
        search: str | None = None,
    ) -> dict[str, Any]:
        et = app.state.eval_tracker
        samples, total = et.get_samples(offset=offset, limit=limit, status_filter=status, search=search)
        return {"samples": samples, "total": total, "offset": offset, "limit": limit}

    @app.get("/api/eval/samples/{sample_id}")
    async def eval_sample_detail(sample_id: str) -> dict[str, Any]:
        et = app.state.eval_tracker
        info = et.get_sample(sample_id)
        if info is None:
            return {"error": "Sample not found"}
        ground_truth = load_ground_truth(info.get("data_dir", ""))
        if ground_truth is not None:
            info = {**info, "ground_truth": ground_truth}
            service = ground_truth.get("service")
            if isinstance(service, list):
                info["root_cause_services"] = [str(s) for s in service if str(s).strip()]
        return info

    @app.get("/api/eval/samples/{sample_id}/events")
    async def eval_sample_events(sample_id: str, after: int = 0) -> dict[str, Any]:
        """Read trajectory events from JSONL file for a specific sample."""
        et = app.state.eval_tracker
        info = et.get_sample(sample_id)
        if info is None:
            return {"error": "Sample not found", "events": [], "total": 0}
        traj_path = info.get("trajectory_path")
        if not traj_path:
            return {"events": [], "total": 0, "status": info.get("status", "unknown")}
        path = Path(traj_path)
        if not path.exists():
            return {"events": [], "total": 0, "status": info.get("status", "unknown")}
        events: list[dict[str, Any]] = []
        event_index = 0
        try:
            with open(path, encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        parsed = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if "_meta" in parsed:
                        continue
                    if event_index >= after:
                        events.append(parsed)
                    event_index += 1
        except OSError:
            pass
        return {"events": events, "total": event_index, "status": info.get("status", "unknown")}

    return app
