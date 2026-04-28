# Ralph

![Ralph](ralph.webp)

Ralph is an autonomous AI agent loop that runs AI coding tools ([Amp](https://ampcode.com), [Claude Code](https://docs.anthropic.com/en/docs/claude-code), or Codex CLI) repeatedly until all PRD items are complete. Each iteration is a fresh instance with clean context. Memory persists via git history, `progress.txt`, and `prd.json`.

Based on [Geoffrey Huntley's Ralph pattern](https://ghuntley.com/ralph/).

[Read my in-depth article on how I use Ralph](https://x.com/ryancarson/status/2008548371712135632)

## Prerequisites

- One of the following AI coding tools installed and authenticated:
  - [Amp CLI](https://ampcode.com) (default)
  - [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`npm install -g @anthropic-ai/claude-code`)
  - Codex CLI (`codex --version` should work in your shell)
- `jq` installed (`brew install jq` on macOS)
- A git repository for your project

## Setup

### Option 1: Copy to your project

Copy the ralph files into your project:

```bash
# From your project root
mkdir -p scripts/ralph
cp /path/to/ralph/ralph.sh scripts/ralph/

# Copy the prompt template for your AI tool of choice:
cp /path/to/ralph/prompt.md scripts/ralph/prompt.md    # For Amp
# OR
cp /path/to/ralph/CLAUDE.md scripts/ralph/CLAUDE.md    # For Claude Code
# OR
cp /path/to/ralph/CODEX.md scripts/ralph/CODEX.md      # For Codex CLI

chmod +x scripts/ralph/ralph.sh
```

### Option 2: Install skills globally (Amp)

Copy the skills to your Amp or Claude config for use across all projects:

For AMP
```bash
cp -r skills/prd ~/.config/amp/skills/
cp -r skills/ralph ~/.config/amp/skills/
```

For Claude Code (manual)
```bash
cp -r skills/prd ~/.claude/skills/
cp -r skills/ralph ~/.claude/skills/
```

### Option 3: Use as Claude Code Marketplace

Add the Ralph marketplace to Claude Code:

```bash
/plugin marketplace add snarktank/ralph
```

Then install the skills:

```bash
/plugin install ralph-skills@ralph-marketplace
```

Available skills after installation:
- `/prd` - Generate Product Requirements Documents
- `/ralph` - Convert PRDs to prd.json format

Skills are automatically invoked when you ask Claude to:
- "create a prd", "write prd for", "plan this feature"
- "convert this prd", "turn into ralph format", "create prd.json"

### Configure Amp auto-handoff (recommended)

Add to `~/.config/amp/settings.json`:

```json
{
  "amp.experimental.autoHandoff": { "context": 90 }
}
```

This enables automatic handoff when context fills up, allowing Ralph to handle large stories that exceed a single context window.

## Workflow

### 1. Create a PRD

Use the PRD skill to generate a detailed requirements document:

```
Load the prd skill and create a PRD for [your feature description]
```

Answer the clarifying questions. The skill saves output to `tasks/prd-[feature-name].md`.

### 2. Convert PRD to Ralph format

Use the Ralph skill to convert the markdown PRD to JSON:

```
Load the ralph skill and convert tasks/prd-[feature-name].md to prd.json
```

This creates `prd.json` with user stories structured for autonomous execution.

### 3. Run Ralph

```bash
# Using Amp (default)
./scripts/ralph/ralph.sh [max_iterations]

# Using Claude Code
./scripts/ralph/ralph.sh --tool claude [max_iterations]

# Using Codex CLI
./scripts/ralph/ralph.sh --tool codex [max_iterations]
```

Default is 10 iterations. Use `--tool amp`, `--tool claude`, or `--tool codex` to select your AI coding tool.

Ralph will:
1. Create a feature branch (from PRD `branchName`)
2. Pick the highest priority story where `passes: false`
3. Implement that single story
4. Run quality checks (typecheck, tests)
5. Commit if checks pass
6. Update `prd.json` to mark story as `passes: true`
7. Append learnings to `progress.txt`
8. Repeat until all stories pass or max iterations reached

## Key Files

| File | Purpose |
|------|---------|
| `ralph.sh` | The bash loop that spawns fresh AI instances (supports `--tool amp`, `--tool claude`, or `--tool codex`) |
| `prompt.md` | Prompt template for Amp |
| `CLAUDE.md` | Prompt template for Claude Code |
| `CODEX.md` | Prompt template for Codex CLI |
| `prd.json` | User stories with `passes` status (the task list) |
| `prd.json.example` | Example PRD format for reference |
| `progress.txt` | Append-only learnings for future iterations |
| `skills/prd/` | Skill for generating PRDs (works with Amp and Claude Code) |
| `skills/ralph/` | Skill for converting PRDs to JSON (works with Amp and Claude Code) |
| `.claude-plugin/` | Plugin manifest for Claude Code marketplace discovery |
| `flowchart/` | Interactive visualization of how Ralph works |

## Flowchart

[![Ralph Flowchart](ralph-flowchart.png)](https://snarktank.github.io/ralph/)

**[View Interactive Flowchart](https://snarktank.github.io/ralph/)** - Click through to see each step with animations.

The `flowchart/` directory contains the source code. To run locally:

```bash
cd flowchart
npm install
npm run dev
```

## Critical Concepts

### Each Iteration = Fresh Context

Each iteration spawns a **new AI instance** (Amp, Claude Code, or Codex CLI) with clean context. The only memory between iterations is:
- Git history (commits from previous iterations)
- `progress.txt` (learnings and context)
- `prd.json` (which stories are done)

### Small Tasks

Each PRD item should be small enough to complete in one context window. If a task is too big, the LLM runs out of context before finishing and produces poor code.

Right-sized stories:
- Add a database column and migration
- Add a UI component to an existing page
- Update a server action with new logic
- Add a filter dropdown to a list

Too big (split these):
- "Build the entire dashboard"
- "Add authentication"
- "Refactor the API"

### AGENTS.md Updates Are Critical

After each iteration, Ralph updates the relevant `AGENTS.md` files with learnings. This is key because AI coding tools automatically read these files, so future iterations (and future human developers) benefit from discovered patterns, gotchas, and conventions.

Examples of what to add to AGENTS.md:
- Patterns discovered ("this codebase uses X for Y")
- Gotchas ("do not forget to update Z when changing W")
- Useful context ("the settings panel is in component X")

### Feedback Loops

Ralph only works if there are feedback loops:
- Typecheck catches type errors
- Tests verify behavior
- CI must stay green (broken code compounds across iterations)

### Browser Verification for UI Stories

Frontend stories must include "Verify in browser using dev-browser skill" in acceptance criteria. Ralph will use the dev-browser skill to navigate to the page, interact with the UI, and confirm changes work.

### Stop Condition

When all stories have `passes: true`, Ralph outputs `<promise>COMPLETE</promise>` and the loop exits.

## Debugging

Check current state:

```bash
# See which stories are done
cat prd.json | jq '.userStories[] | {id, title, passes}'

# See learnings from previous iterations
cat progress.txt

# Check git history
git log --oneline -10
```

## Customizing the Prompt

After copying `prompt.md` (for Amp), `CLAUDE.md` (for Claude Code), or `CODEX.md` (for Codex CLI) to your project, customize it for your project:
- Add project-specific quality check commands
- Include codebase conventions
- Add common gotchas for your stack

You can also override the prompt file entirely:

```bash
./scripts/ralph/ralph.sh --tool codex --prompt-file path/to/custom-prompt.md 20
```

And you can isolate loop state per run or per system:

```bash
./scripts/ralph/ralph.sh --tool codex --state-dir path/to/state --prompt-file path/to/state/CODEX.md 20
```

That makes Ralph usable for non-PRD loops where the outer "fresh-context each iteration" pattern is still useful.

## Inject Loop Example

This fork includes an Aegis-oriented example under `examples/inject-loop/` for closed-loop fault injection campaigns:

- `examples/inject-loop/CODEX.md` - prompt template for Codex-driven inject-loop rounds
- `examples/inject-loop/campaign.json.example` - example campaign state file
- `examples/inject-loop/prompt.template.md` - renderable prompt template for per-system campaign state
- `examples/inject-loop/init_campaign.py` - initialize one system's campaign state directory
- `examples/inject-loop/launch_parallel.py` - launch/status/stop multiple Codex loops in parallel
- `experiments/lib/live_mix.py` - refresh the allowed supported fault space plus live trace mix before each round

Current cluster note:

- HTTP chaos is hard-disabled for inject loops on the current Aegis cluster. Campaigns default `excluded_chaos_types` to `HTTP*`, `live_mix.py` writes a filtered `_supported_candidates.json`, and `validate_round.py` rejects any round that still contains HTTP chaos.
- `examples/inject-loop/parallel.json.example` - manifest for multi-system parallel runs
- `experiments/lib/live_mix.py` - refresh live per-system injection / trace mix from Aegis before planning a round

The intended usage is to keep Ralph as the **fresh-agent orchestrator**, while the prompt and campaign state redefine what one iteration means:

- reaping and scoring the latest round
- refreshing live supported / injected / running fault mix from Aegis
- enforcing `chaos_type` diversity before candidate selection
- writing the next `candidates_round<N>.json`
- submitting through `experiments/lib/submit_dual.py`

Example:

```bash
cp ralph/examples/inject-loop/campaign.json.example ralph/examples/inject-loop/campaign.json
./ralph/ralph.sh --tool codex --prompt-file ralph/examples/inject-loop/CODEX.md 12
```

For per-system parallel loops, initialize one state directory per system and then launch them together:

```bash
python3 ralph/examples/inject-loop/init_campaign.py \
  --state-dir ralph/examples/inject-loop/campaigns/trainticket \
  --loop-dir experiments/trainticket-loop \
  --duration-minutes 10 \
  --migrate-rounds

python3 ralph/examples/inject-loop/init_campaign.py \
  --state-dir ralph/examples/inject-loop/campaigns/teastore \
  --loop-dir experiments/teastore-loop \
  --duration-minutes 15

python3 experiments/lib/live_mix.py \
  --campaign ralph/examples/inject-loop/campaigns/trainticket/campaign.json \
  --refresh-supported

python3 ralph/examples/inject-loop/launch_parallel.py \
  launch \
  --dry-run \
  --manifest ralph/examples/inject-loop/parallel.json
```

Each system gets its own:

- `campaign.json`
- `CODEX.md`
- `progress.txt`
- `codex.loop.log`
- `codex.pid`

That separation avoids progress/log collisions when several Codex loops run at once.

By default `launch_parallel.py` skips campaigns whose `campaign.json` status is not `running`. Use `--include-paused` only when you intentionally want to start paused systems.

## Archiving

Ralph automatically archives previous runs when you start a new feature (different `branchName`). Archives are saved to `archive/YYYY-MM-DD-feature-name/`.

## References

- [Geoffrey Huntley's Ralph article](https://ghuntley.com/ralph/)
- [Amp documentation](https://ampcode.com/manual)
- [Claude Code documentation](https://docs.anthropic.com/en/docs/claude-code)
