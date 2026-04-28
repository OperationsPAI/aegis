---
name: aegisctl
description: How to drive the aegis backend via the aegisctl CLI in an agent-friendly way. Use whenever a task involves listing / counting / filtering injections, traces, tasks, executions, datasets, containers, projects, or pedestals; whenever you'd otherwise reach for mysql / kubectl / redis-cli to inspect aegis state; whenever a recipe might paginate through results; or whenever the user asks for a quick distribution / summary like "how many succeeded vs failed", "看一下注入情况", "注入分布", "成功多少失败多少", "状态分布", "分布", "summary", "breakdown", "success rate", "list X", "show X". Trigger words aegisctl, inject list, trace list, task list, distribution, summary, count, query inject status, jq pipe.
---

# aegisctl — usage philosophy

`aegisctl` is the supported surface for everything in the aegis control plane. The CLI is exhaustively self-documenting; this skill captures only what the help output cannot teach: how to **compose** commands and where the sharp edges are.

## Read help first, always

`aegisctl <noun> [verb] --help` is the source of truth for flags, accepted values, and exit semantics. Don't memorize subcommands from this skill — open `--help` for the resource you're about to touch. Most resources expose `list / get / search / create / delete / files / download` plus resource-specific verbs.

## Compose with pipes; do not page in shell

The CLI is designed for one-line composition with `jq`, `awk`, `sort | uniq -c`. The headline pattern is:

```
aegisctl <noun> list [filters] --all -o ndjson | jq … | sort | uniq -c
```

`--all` is supported on every list command (`inject`, `trace`, `task`, …). When set, it pages internally at the wire's max page size and streams **one record per line** to stdout. **No shell `for page in $(seq …)` loop should ever appear** — if you find yourself writing one, you're using the CLI wrong.

Two consequences worth internalizing:

1. **Format under `--all` must be `ndjson`.** `table` and `json` are refused because they buffer the full set, defeating the purpose. NDJSON keeps memory flat and lets `jq`/`awk` start producing results before the last page lands.
2. **Pagination metadata is suppressed under `--all`.** Stdout is a clean record stream — no envelope, no header, nothing to strip with `tail -n +2`. Pipe straight into `jq`.

For one-shot lookups (single record, top-N preview, "is the cluster healthy") use the default paged form — `--all` is overkill there.

## Use names, not numeric ids

Filter flags accept symbolic values everywhere it's been wired: `--state inject_success`, `--fault-type PodFailure`, `--project pair_diagnosis`. Numeric ids are still accepted for backward compatibility. **Always pass names** in agent-authored commands; they survive enum reordering and read like documentation. If a flag rejects a name, the error message tells you the valid set — read it.

## NDJSON, JSON, table — pick by audience

- `-o table` (default) — humans only. Don't pipe it.
- `-o json` — single-shot inspection of one record's full structure (`get` and `--size 1` style probes).
- `-o ndjson` — every other agent / scripting use. Required under `--all`.

When in doubt, NDJSON. It's the only format whose contract is "one record per line, stable shape" — everything else is for human reading.

## When you reach for mysql / kubectl / redis-cli, stop

If the only way to answer the user's question is to query the database, kubectl-exec into a pod, or scan Redis directly, **that is an aegisctl gap, not a clever shortcut**. Flag it (open an issue, or surface it in your reply), then proceed with the workaround. The CLI is the contract; raw infrastructure pokes are a smell. Same applies to bypassing aegisctl for `helm` operations on pedestals — `aegisctl pedestal …` is the supported path.

## Sharp edges to know about

- **`inject list` returns hybrid batch parents alongside leaf injections.** A `batch-*` row carries its own state and fault_type and *belongs* in distribution counts. Filter them out (`select((.name | startswith("batch-")) | not)`) only when the user explicitly asks for leaf injections.
- **Names of resources are stable across most resource lookups** (`aegisctl container get detector` resolves to the right id). Pass names; don't grep ids.
- **Auth tokens may rotate after backend redeploy.** A 401 in the middle of a session usually means re-run `aegisctl auth login`. Tokens persist to `~/.aegisctl/config.yaml`.
- **`--non-interactive` exists for a reason.** In CI, scripts, or any agent context that should not block on a prompt, set it. CLI fails fast instead of asking.
- **Exit codes are classified.** `aegisctl wait <trace-id>` exits 0=ok / 5=fail / 6=timeout; many commands follow the same pattern. Branch on exit codes in scripts; don't grep stdout.

## Quick cheatsheet (illustrative — not exhaustive)

```
# State distribution of every injection in a project
aegisctl inject list --project pair_diagnosis --all -o ndjson | jq -r .state | sort | uniq -c

# Filter then aggregate
aegisctl inject list --project pair_diagnosis --all --fault-type NetworkLoss -o ndjson \
  | jq -r .state | sort | uniq -c

# Cross-tab in one line
aegisctl task list --all -o ndjson | jq -r '"\(.type)\t\(.state)"' | sort | uniq -c | sort -rn

# Top-N drill-down
aegisctl trace list --project pair_diagnosis --all -o ndjson \
  | jq -r 'select(.state=="Failed") | .id' | head -20
```

These are illustrative seeds — the right pipe for any specific question is built from `--help` + the patterns above, not from copying examples verbatim.
