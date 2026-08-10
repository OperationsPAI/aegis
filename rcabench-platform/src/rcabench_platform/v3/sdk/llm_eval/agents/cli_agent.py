"""Generic CLI agent that executes arbitrary shell commands for RCA evaluation.

Usage::

    agent = CLIAgent(
        command_template='claude -p -- "$(cat {instruction_file})"',
        agent_name="claude-code",
    )
    ok, fail = await benchmark.rollout(agent)
"""

from __future__ import annotations

import asyncio
import shutil
import tempfile
from pathlib import Path
from typing import Any

from .base_agent import AgentResult, BaseAgent


class CLIAgent(BaseAgent):
    """Run an RCA investigation via an external CLI command.

    The agent creates a temporary working directory containing:

    * ``instruction.md`` — incident description produced by the preprocessor.
    * ``data/`` — symlink to the observability data directory.

    The agent is expected to write its output to ``response.json`` in the
    working directory.  The *command_template* string may contain the
    following placeholders (all resolved as absolute paths):

    * ``{instruction_file}`` — path to ``instruction.md``
    * ``{data_dir}`` — path to the ``data/`` symlink
    * ``{output_file}`` — path where the agent should write ``response.json``
    * ``{workdir}`` — root of the temporary working directory

    Args:
        command_template: Shell command template with placeholders.
        agent_name: Name returned by :meth:`name` (default ``"cli"``).
        timeout: Per-sample execution timeout in seconds (``0`` = no limit).
        env: Extra environment variables passed to the subprocess.
    """

    def __init__(
        self,
        command_template: str,
        agent_name: str = "cli",
        timeout: float = 0,
        env: dict[str, str] | None = None,
    ) -> None:
        self._command_template = command_template
        self._agent_name = agent_name
        self._timeout = timeout
        self._env = env

    @staticmethod
    def name() -> str:  # noqa: D102 — docstring in base class
        return "cli"

    async def run(
        self,
        incident: str,
        data_dir: str,
        **kwargs: Any,
    ) -> AgentResult:
        workdir = Path(tempfile.mkdtemp(prefix="llm_eval_cli_"))
        try:
            instruction_file = workdir / "instruction.md"
            output_file = workdir / "response.json"
            data_link = workdir / "data"

            instruction_file.write_text(incident, encoding="utf-8")
            data_link.symlink_to(data_dir)

            cmd = self._command_template.format(
                instruction_file=instruction_file,
                data_dir=data_link,
                output_file=output_file,
                workdir=workdir,
            )

            import os

            env = {**os.environ, **(self._env or {})}

            timeout = kwargs.get("timeout", self._timeout) or None
            proc = await asyncio.create_subprocess_shell(
                cmd,
                cwd=str(workdir),
                env=env,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await asyncio.wait_for(
                proc.communicate(),
                timeout=timeout,
            )

            response = ""
            if output_file.exists():
                response = output_file.read_text(encoding="utf-8")
            elif stdout:
                # Fallback: if no response.json, use stdout as response
                response = stdout.decode("utf-8", errors="replace")

            return AgentResult(
                response=response,
                metadata={
                    "return_code": proc.returncode,
                    "stderr": (stderr or b"").decode("utf-8", errors="replace")[:2000],
                    "agent_name": self._agent_name,
                },
            )
        finally:
            shutil.rmtree(workdir, ignore_errors=True)
