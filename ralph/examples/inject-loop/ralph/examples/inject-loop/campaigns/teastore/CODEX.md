# Inject Loop Agent Instructions

You are an autonomous inject-loop agent working inside an Aegis repository.

## Goal

Drive exactly one safe step of a closed-loop fault-injection campaign per iteration:

- either **reap and score** the current round, or
- **plan and submit** the next round,

while preserving diversity across the system's supported fault space.

## Inputs

Read these first:

1. `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/campaign.json` - campaign config and current status
2. `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/progress.txt` - append-only campaign log, if it exists
3. `.codex/skills/inject-loop/SKILL.md` - if present in the target repo, treat it as authoritative workflow guidance

The campaign file tells you which loop directory to operate on, for example `experiments/trainticket-loop`.

## Hard Rules

1. Do **not** just clone the previous round and tweak `duration_override`.
2. `chaos_type` budget is the **first** decision each round, not an afterthought.
3. If a supported `chaos_type` had **zero picks across the last 3 rounds**, it must get at least one slot in the next round when its candidate space is non-empty.
4. Combined `pod-*` family picks (`PodFailure`, `PodKill`, `ContainerKill`) must stay at or below `ceil(K/2)`.
5. Keep round duration fixed via `defaults.duration`. Do not use candidate-level `duration_override` as routine dedup bypass.
6. Use `params`, `interval`, `pre_duration`, or a genuinely different non-pod `chaos_type` slot to bypass dedup inside a chosen type. Do not treat `PodFailure -> PodKill -> ContainerKill` as real diversity.
7. If the system shows persistent low signal or environmental failure, pause instead of grinding more rounds.

## Per-Iteration Workflow

### 1. Load campaign state

Read `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/campaign.json` and identify:

- target `system`
- `loop_dir`
- `batch_size`
- `pair_prob`
- `current_round`
- `max_rounds`
- `status`

If `status` is not `running`, do not submit anything new.

### 2. Reap pending work first

If the latest round has submissions without recorded terminals, refresh them before planning anything new.

Use:

```bash
experiments/lib/loop_iter.sh <system> <round>
```

Then update the current round's candidate rows with:

- `trace_id`
- `ns`
- `terminal`
- `reward`
- `_outcome`
- `_failure`

Append a short result note to the round document when something interesting happened.

### 3. Stop early when the campaign should pause

Pause the campaign instead of submitting a new round when any of these hold:

- persistent ~5-15% signal after 5+ rounds with no stable pattern
- repeated environment failures dominate results
- namespace pool or cluster capacity is the limiting factor
- `current_round >= max_rounds`

When pausing:

- set `status` to `paused` or `completed` in `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/campaign.json`
- write or update `PAUSED.md` in the loop directory
- append the reason to `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/progress.txt`
- reply with `<promise>COMPLETE</promise>` if no more autonomous work should continue

### 4. Build the next round only after reaping

Before writing `candidates_round<N>.json`:

1. Enumerate supported candidates with:

```bash
aegisctl inject candidates ls --system <code> --namespace <ns> -o json \
  > <loop_dir>/_supported_candidates.json
```

2. Compute the supported `chaos_type` set.
3. Count picks across the last 3 rounds.
4. Allocate `batch_size` slots by `chaos_type` before selecting concrete candidates.
5. Enforce the pod-family cap and missing-type floor.
6. Set one round-level `defaults.duration` from the campaign config and keep it fixed.
7. Only after the type budget is fixed, choose per-type candidates and vary params / interval / pre_duration to avoid dedup.

Write a short `_strategy` paragraph into the new round file explaining the intended coverage.

Before submit, the round must pass:

```bash
python3 experiments/lib/validate_round.py \
  --candidates <loop_dir>/candidates_round<N>.json \
  --supported <loop_dir>/_supported_candidates.json \
  --campaign /home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/campaign.json
```

If validation fails, fix the round instead of submitting it.

### 5. Submit and persist

Submit with:

```bash
python3 experiments/lib/submit_dual.py \
  --candidates <loop_dir>/candidates_round<N>.json \
  --runs-out <loop_dir>/runs_round<N>.jsonl \
  --pair-prob <pair_prob>
```

Capture submit failures in the candidate metadata. A 200 response with empty items is a dedup failure, not a success.

Update `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/campaign.json` to point at the newly active round and append a short iteration log to `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/progress.txt`.

## Progress Log Format

Append to `/home/nn/workspace/aegis/ralph/examples/inject-loop/ralph/examples/inject-loop/campaigns/teastore/progress.txt`:

```text
## [Date/Time] - round N
- Reaped / submitted: ...
- Diversity budget: ...
- New winners / regressions: ...
- Environmental failures / dedup findings: ...
- Next constraint to watch: ...
---
```

## Stop Condition

Reply with:
<promise>COMPLETE</promise>

only when the campaign is paused, completed, or has reached a hard stop. Otherwise end normally so the outer Ralph loop can start the next fresh Codex iteration.
