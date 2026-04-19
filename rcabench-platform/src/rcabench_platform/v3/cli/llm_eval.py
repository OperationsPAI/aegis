"""CLI subcommand for LLM agent evaluation.

Usage:
    rca llm-eval run <config_path>                        -- preprocess + judge + stat
    rca llm-eval run <config_path> --agent agentm --ak .. -- full pipeline with agent
    rca llm-eval judge <config_path>                      -- re-judge existing rollouts
    rca llm-eval stat <config_path>                       -- show metrics only
    rca llm-eval agents                                   -- list registered agents
"""

from __future__ import annotations

import asyncio
import os
import time
from typing import Annotated, Any, Literal, cast

import typer
from rich.console import Console
from rich.table import Table

app = typer.Typer(
    help="LLM agent evaluation commands", pretty_exceptions_show_locals=False, pretty_exceptions_enable=False
)
console = Console()


def _load_config(config_path: str, exp_id: str | None = None):
    """Load eval config, optionally overriding exp_id."""
    from ..sdk.llm_eval.config import ConfigLoader

    config = ConfigLoader.load_eval_config(config_path)
    if exp_id:
        config.exp_id = exp_id
    return config


def _parse_kwargs(raw: list[str] | None) -> dict[str, str]:
    """Parse ``key=value`` strings into a dict."""
    if not raw:
        return {}
    result: dict[str, str] = {}
    for item in raw:
        key, _, value = item.partition("=")
        if not key:
            continue
        result[key.strip()] = value.strip()
    return result


# ── Shared option types ────────────────────────────────────────────────
ConfigPathArg = Annotated[str, typer.Argument(help="Path to YAML config file")]
ExpIdOpt = Annotated[str | None, typer.Option("--exp-id", help="Override experiment ID")]


# ── run ────────────────────────────────────────────────────────────────
@app.command()
def run(
    config_path: ConfigPathArg,
    exp_id: ExpIdOpt = None,
    # Agent
    agent: Annotated[
        str | None,
        typer.Option("-a", "--agent", help="Agent name from registry (enables rollout)", rich_help_panel="Agent"),
    ] = None,
    agent_kwarg: Annotated[
        list[str] | None,
        typer.Option("--ak", help="Agent kwargs (key=value, repeatable)", rich_help_panel="Agent"),
    ] = None,
    # Overrides
    concurrency: Annotated[
        int | None,
        typer.Option("-n", "--concurrency", help="Rollout concurrency (overrides config)", rich_help_panel="Overrides"),
    ] = None,
    max_samples: Annotated[
        int | None,
        typer.Option(
            "-l", "--max-samples", help="Max samples to rollout (overrides config)", rich_help_panel="Overrides"
        ),
    ] = None,
    timeout: Annotated[
        float | None,
        typer.Option("--timeout", help="Per-sample timeout in seconds", rich_help_panel="Overrides"),
    ] = None,
    max_steps: Annotated[
        int | None,
        typer.Option("--max-steps", help="Per-sample max agent steps", rich_help_panel="Overrides"),
    ] = None,
    source_path: Annotated[
        str | None,
        typer.Option(
            "--source-path", help="Dataset root path (overrides config.source_path)", rich_help_panel="Overrides"
        ),
    ] = None,
    # Dashboard
    dashboard: Annotated[
        bool,
        typer.Option("--dashboard", help="Launch real-time eval dashboard", rich_help_panel="Dashboard"),
    ] = False,
    dashboard_port: Annotated[
        int,
        typer.Option("--dashboard-port", help="Dashboard server port", rich_help_panel="Dashboard"),
    ] = 8765,
    dashboard_host: Annotated[
        str,
        typer.Option("--dashboard-host", help="Dashboard server host", rich_help_panel="Dashboard"),
    ] = "0.0.0.0",
) -> None:
    """Run evaluation pipeline.

    Without --agent: preprocess + judge + stat (no rollout).
    With --agent: preprocess + rollout + judge + stat (full pipeline).
    """
    from pathlib import Path

    from dotenv import load_dotenv

    # find_dotenv() may fail in some environments; explicitly search from cwd upward.
    _env_path = None
    for _d in [Path.cwd(), *Path.cwd().parents]:
        _candidate = _d / ".env"
        if _candidate.is_file():
            _env_path = _candidate
            break

    print(f"[DEBUG] cwd={Path.cwd()}, env_path={_env_path}")
    if _env_path:
        load_dotenv(str(_env_path))
    else:
        load_dotenv()

    config = _load_config(config_path, exp_id)

    if agent is None:
        # Legacy mode: no rollout
        from ..sdk.llm_eval.eval import BaseBenchmark

        benchmark = BaseBenchmark(config)
        asyncio.run(benchmark.main())
        return

    # Full pipeline with agent
    asyncio.run(
        _run_with_agent(
            config=config,
            agent_name=agent,
            agent_kwargs=_parse_kwargs(agent_kwarg),
            concurrency=concurrency,
            max_samples=max_samples,
            timeout=timeout,
            max_steps=max_steps,
            source_path=source_path,
            dashboard=dashboard,
            dashboard_port=dashboard_port,
            dashboard_host=dashboard_host,
        )
    )


async def _run_with_agent(
    config: Any,
    agent_name: str,
    agent_kwargs: dict[str, str],
    concurrency: int | None,
    max_samples: int | None,
    timeout: float | None,
    max_steps: int | None,
    source_path: str | None,
    dashboard: bool,
    dashboard_port: int,
    dashboard_host: str,
) -> None:
    """Orchestrate the full eval pipeline with a registered agent."""
    from ..sdk.llm_eval.agents import AGENT_REGISTRY
    from ..sdk.llm_eval.eval import BaseBenchmark
    from ..sdk.llm_eval.eval.tracker import EvalTracker
    from ..sdk.utils.notify import Notifier

    notifier = Notifier()
    t_start = time.monotonic()

    # 1. Create agent and auto-fill config metadata
    agent = AGENT_REGISTRY.get(agent_name, exp_id=config.exp_id, **agent_kwargs)
    config.agent_type = agent_name
    if config.model_name is None:
        config.model_name = agent.model_name()

    # 2. Apply CLI overrides
    if concurrency is not None:
        config.concurrency = concurrency
    if max_samples is not None:
        config.max_samples = max_samples
    if source_path is not None:
        config.source_path = source_path

    # 3. Set up DB URL
    if config.db_url:
        os.environ["LLM_EVAL_DB_URL"] = config.db_url

    # 4. Build benchmark (source_path_fn is resolved from config automatically)
    benchmark = BaseBenchmark(config)

    console.print(
        f"[bold]Eval:[/] agent=[cyan]{agent_name}[/]  exp_id=[cyan]{config.exp_id}[/]  "
        f"concurrency=[cyan]{config.concurrency}[/]"
    )

    # 5. Optional dashboard
    tracker: EvalTracker | None = None
    dashboard_server_task = None

    if dashboard:
        tracker = EvalTracker(trajectory_dir="./trajectories")

        import uvicorn

        from ..sdk.llm_eval.eval.dashboard import Broadcaster, create_eval_dashboard

        bc = Broadcaster()
        dash_app = create_eval_dashboard(eval_tracker=tracker, broadcaster=bc)

        _loop = asyncio.get_running_loop()

        def _tracker_to_ws(event: dict) -> None:
            try:
                asyncio.run_coroutine_threadsafe(bc.broadcast(event), _loop)
            except RuntimeError:
                pass

        tracker.add_listener(_tracker_to_ws)

        uvi_config = uvicorn.Config(dash_app, host=dashboard_host, port=dashboard_port, log_level="warning")
        server = uvicorn.Server(uvi_config)
        dashboard_server_task = asyncio.create_task(server.serve())
        await asyncio.sleep(0.3)
        console.print(f"Dashboard: [link=http://localhost:{dashboard_port}]http://localhost:{dashboard_port}[/link]")

    # 6. Preprocess
    console.print("[bold]Phase 1:[/] preprocess")
    benchmark.preprocess()

    # 7. Rollout + Judge (incremental)
    console.print("[bold]Phase 2:[/] rollout + judge (incremental)")
    ok_count, fail_count = 0, 0

    def on_event(sample_id: str, event: dict) -> None:
        evt_type = event.get("type", "")
        sample = event.get("sample")

        if evt_type == "started":
            data_dir = event.get("data_dir", "")
            idx = sample.dataset_index if sample else "?"
            console.print(f"  [blue]START[/] sample id={sample_id} idx={idx} data_dir={data_dir}")
            if tracker and sample:
                tracker.register_sample(sample_id, sample.dataset_index, data_dir)

        elif evt_type == "running":
            run_id = event.get("run_id", "")
            if tracker:
                tracker.mark_running(sample_id, run_id)

        elif evt_type == "trajectory_update":
            traj_path = event.get("path", "")
            if tracker and traj_path:
                tracker.update_trajectory_path(sample_id, traj_path)

        elif evt_type == "completed":
            idx = sample.dataset_index if sample else "?"
            console.print(f"  [green]OK[/] sample id={sample_id} idx={idx}")
            if tracker:
                tracker.mark_completed(sample_id)

        elif evt_type == "failed":
            idx = sample.dataset_index if sample else "?"
            error = event.get("error", "empty response")
            console.print(f"  [red]FAIL[/] sample id={sample_id} idx={idx}: {error}")
            notifier.error(
                f"Sample failed: {sample_id}",
                f"**exp_id:** {config.exp_id}\n**sample:** {sample_id} (idx={idx})\n**error:** {error}",
            )
            if tracker:
                tracker.mark_failed(sample_id, error)

        elif evt_type == "skipped":
            idx = sample.dataset_index if sample else "?"
            console.print(f"  [yellow]SKIP[/] sample id={sample_id} idx={idx}")
            if tracker and sample:
                meta = sample.meta if isinstance(sample.meta, dict) else {}
                tracker.register_sample(sample_id, sample.dataset_index, meta.get("path", ""))
                tracker.mark_skipped(sample_id, "missing incident or data_dir")

    rollout_kwargs: dict[str, Any] = {}
    if timeout is not None:
        rollout_kwargs["timeout"] = timeout
    if max_steps is not None:
        rollout_kwargs["max_steps"] = max_steps

    # Count total samples for incremental stat display
    _init_samples = benchmark.dataset.get_samples(
        stage="init",
        agent_type=benchmark.agent_type,
        model_name=benchmark.model_name,
        tags=benchmark.tags,
        exclude_trajectories=True,
    )
    _total_samples = len(_init_samples[: config.max_samples] if config.max_samples else _init_samples)

    _judge_stats: dict[str, Any] = {"correct": 0, "incorrect": 0, "rc_f1_sum": 0.0, "rc_f1_count": 0}

    def on_judge(sample: Any, all_judged: list[Any]) -> None:
        try:
            if sample.correct is True:
                _judge_stats["correct"] += 1
            elif sample.correct is False:
                _judge_stats["incorrect"] += 1
            if isinstance(sample.meta, dict) and "graph_metrics" in sample.meta:
                primary = sample.meta["graph_metrics"].get("primary", {})
                _judge_stats["rc_f1_sum"] += primary.get("root_cause_f1", 0.0)
                _judge_stats["rc_f1_count"] += 1
        except Exception as exc:
            console.print(f"  [yellow]WARN[/] on_judge stat failed for sample {getattr(sample, 'id', '?')}: {exc}")
        total = len(all_judged)
        correct = _judge_stats["correct"]
        judged = correct + _judge_stats["incorrect"]
        accuracy = (correct / judged * 100) if judged > 0 else 0.0
        parts = [
            f"[cyan]STAT[/] {total}/{_total_samples} judged",
            f"Accuracy: {accuracy:.1f}% ({correct}/{judged})",
        ]
        if _judge_stats["rc_f1_count"] > 0:
            avg_rc_f1 = _judge_stats["rc_f1_sum"] / _judge_stats["rc_f1_count"]
            parts.append(f"RC_F1: {avg_rc_f1:.3f}")
        console.print("  " + " | ".join(parts))

    try:
        ok_count, fail_count, _judged = await benchmark.rollout_and_judge(
            agent,
            max_samples=config.max_samples,
            on_event=on_event,
            on_judge=on_judge,
            **rollout_kwargs,
        )
        console.print(f"  [green]{ok_count} ok[/] / [red]{fail_count} failed[/]")

        # 8. Stat
        console.print("[bold]Phase 3:[/] stat")
        await benchmark.stat()

        # 9. Notify completion
        elapsed = time.monotonic() - t_start
        elapsed_str = f"{elapsed / 60:.1f}min"
        notifier.info(
            f"Eval completed: {config.exp_id}",
            f"**agent:** {agent_name}\n**exp_id:** {config.exp_id}\n"
            f"**results:** {ok_count} ok / {fail_count} failed\n"
            f"**elapsed:** {elapsed_str}",
        )
    except Exception as exc:
        elapsed = time.monotonic() - t_start
        elapsed_str = f"{elapsed / 60:.1f}min"
        notifier.error(
            f"Eval crashed: {config.exp_id}",
            f"**agent:** {agent_name}\n**exp_id:** {config.exp_id}\n"
            f"**results so far:** {ok_count} ok / {fail_count} failed\n"
            f"**elapsed:** {elapsed_str}\n**error:** {exc}",
        )
        raise
    finally:
        notifier.flush()

    # 10. Keep dashboard alive
    if dashboard_server_task is not None:
        console.print(
            f"\nDashboard running at [link=http://localhost:{dashboard_port}]"
            f"http://localhost:{dashboard_port}[/link] -- press Ctrl+C to stop."
        )
        try:
            await dashboard_server_task
        except asyncio.CancelledError:
            pass


# ── judge ──────────────────────────────────────────────────────────────
@app.command()
def judge(
    config_path: ConfigPathArg,
    exp_id: ExpIdOpt = None,
    stage: Annotated[
        str | None, typer.Option(help="Stage to judge (init/rollout/judged, or none for all)")
    ] = "rollout",
) -> None:
    """Re-judge existing rollout results."""
    config = _load_config(config_path, exp_id)

    from ..sdk.llm_eval.eval import BaseBenchmark

    benchmark = BaseBenchmark(config)
    _stage = None if stage == "none" else stage
    resolved_stage = cast(Literal["init", "rollout", "judged"] | None, _stage)
    asyncio.run(benchmark.judge(stage=resolved_stage))


# ── stat ───────────────────────────────────────────────────────────────
@app.command()
def stat(
    config_path: ConfigPathArg,
    exp_id: ExpIdOpt = None,
) -> None:
    """Show evaluation metrics for judged samples."""
    config = _load_config(config_path, exp_id)

    from ..sdk.llm_eval.eval import BaseBenchmark

    benchmark = BaseBenchmark(config)
    asyncio.run(benchmark.stat())


# ── agents ─────────────────────────────────────────────────────────────
@app.command()
def agents() -> None:
    """List registered agents."""
    from ..sdk.llm_eval.agents import AGENT_REGISTRY

    agent_names = AGENT_REGISTRY.list_agents()
    if not agent_names:
        console.print("[yellow]No agents registered.[/]")
        return

    table = Table(title="Registered Agents")
    table.add_column("Name", style="cyan")
    table.add_column("Module", style="dim")

    for name in agent_names:
        agent_cls = AGENT_REGISTRY.get_class(name)
        module = f"{agent_cls.__module__}.{agent_cls.__name__}"
        table.add_row(name, module)

    console.print(table)
