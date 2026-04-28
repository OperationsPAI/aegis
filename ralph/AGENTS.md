# Ralph Agent Instructions

## Overview

Ralph is an autonomous AI agent loop that runs AI coding tools (Amp, Claude Code, or Codex CLI) repeatedly until all PRD items are complete. Each iteration is a fresh instance with clean context.

## Commands

```bash
# Run the flowchart dev server
cd flowchart && npm run dev

# Build the flowchart
cd flowchart && npm run build

# Run Ralph with Amp (default)
./ralph.sh [max_iterations]

# Run Ralph with Claude Code
./ralph.sh --tool claude [max_iterations]

# Run Ralph with Codex CLI
./ralph.sh --tool codex [max_iterations]

# Run Ralph with a custom prompt file
./ralph.sh --tool codex --prompt-file examples/inject-loop/CODEX.md [max_iterations]

# Run Ralph with isolated per-system state
./ralph.sh --tool codex --state-dir examples/inject-loop/campaigns/trainticket --prompt-file examples/inject-loop/campaigns/trainticket/CODEX.md [max_iterations]
```

## Key Files

- `ralph.sh` - The bash loop that spawns fresh AI instances (supports `--tool amp`, `--tool claude`, or `--tool codex`)
- `prompt.md` - Instructions given to each AMP instance
- `CLAUDE.md` - Instructions given to each Claude Code instance
- `CODEX.md` - Instructions given to each Codex CLI instance
- `prd.json.example` - Example PRD format
- `examples/inject-loop/` - Aegis-oriented prompt and state examples for closed-loop fault injection
- `experiments/lib/live_mix.py` - pulls live supported / injected / trace-state mix for a system before planning the next round
- `flowchart/` - Interactive React Flow diagram explaining how Ralph works

## Flowchart

The `flowchart/` directory contains an interactive visualization built with React Flow. It's designed for presentations - click through to reveal each step with animations.

To run locally:
```bash
cd flowchart
npm install
npm run dev
```

## Patterns

- Each iteration spawns a fresh AI instance (Amp, Claude Code, or Codex CLI) with clean context
- Memory persists via git history, `progress.txt`, and `prd.json`
- Stories should be small enough to complete in one context window
- Always update AGENTS.md with discovered patterns for future iterations
- The outer loop is generic enough to drive non-PRD workflows when paired with a custom prompt file
- Per-system parallel inject loops should use separate `--state-dir` values so `progress.txt` and logs do not collide
- Inject-loop planning should prefer live Aegis inventory (`live_mix.py`) over only local round history when choosing the next fault mix
- On the current byte-cluster style deployment, treat all `HTTP*` chaos types as unsupported even if Aegis enumerates them; campaigns should exclude them and validators should reject them
