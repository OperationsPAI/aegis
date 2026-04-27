# Aegis Workspace — Working Guidelines

This is the workspace-level guide. Each subproject (`AegisLab/`, `AegisLab-frontend/`,
`chaos-experiment/`, `rcabench-platform/`, `rcabench-platform-phase2/`) has its own
`CLAUDE.md` with stack-specific rules — read those when working inside a subproject.
This file captures the cross-cutting principles that apply everywhere.

## Core principles

### 1. Quality over quantity — especially for tests

The test suite has grown faster than the value it provides. Adding more tests is not
the goal; **catching real regressions in the parts that actually break** is.

Before writing a new test, ask:
- **Does this test cover behavior that, if broken, would surface as a real user-visible
  bug or pipeline failure?** If no, don't write it.
- **Is this duplicating coverage already provided by an integration / E2E test?**
  Prefer one good integration test over five unit tests that mock the same path.
- **Will this test still be meaningful in 6 months, or am I writing it to pad coverage
  for a PR I'm about to merge?**

When touching existing tests:
- **Prefer deleting or merging weak tests** over adding new ones. A test that only
  asserts "the function returns without panicking" is noise.
- **Mocks lie.** For the inject → collect → detect pipeline, integration tests against
  a real cluster (kind / VKE) catch what unit tests miss. See
  `docs/troubleshooting/benchmark-integration-playbook.md`.
- If a test breaks because production behavior changed and the new behavior is correct,
  **delete or rewrite the test** — don't paper over it with `if newBehavior {}`.

Default stance: **be skeptical of every new `*_test.go` / `test_*.py` file**. The bar
is "this catches a bug that other tests don't" — not "this exercises code path X".

### 2. Don't add features, code, or abstractions beyond what was asked

- A bug fix doesn't need surrounding refactors.
- A one-shot script doesn't need a helper / config / CLI flag.
- Three similar lines beats a premature abstraction.
- Don't design for hypothetical future requirements.
- No backwards-compatibility shims unless explicitly requested — just change the code.

### 3. Comments: default to none

Only write a comment when the **why** is non-obvious — a hidden constraint, a workaround
for a specific bug, behavior that would surprise a reader. Never explain *what* the code
does (well-named identifiers do that), never reference the task / PR / caller
("added for the X flow", "used by Y") — that belongs in the commit message.

### 4. Trust internal code; validate only at boundaries

No defensive nil-checks on values you just constructed. No try/except wrapping
framework guarantees. Validate at user input, external APIs, and shell boundaries —
nowhere else.

### 5. Code is the source of truth

When code, tests, and docs disagree: **code wins**. Update tests and docs to match
real behavior, not the other way around. If a doc claims a feature exists that the
code doesn't have, the doc is wrong — fix or delete it.

## Workspace-specific rules

### aegisctl ownership

If you find yourself reaching for `mysql` / `kubectl` / `redis-cli` to do something
that *should* be an aegisctl command, **flag the gap** rather than silently working
around it. The CLI is the supported surface; raw infrastructure pokes are a smell.

### Stacked PRs

When merging stacked PRs with `--delete-branch`, **retarget child PRs to `main`
BEFORE merging the parent**. Otherwise the child auto-closes and is unrecoverable.
(Lost #177 this way.)

### Skill style (when authoring `aegis/skills/*`)

Skills tell agents **WHAT to do** (symptom → flag / workaround), not **how the code
works**. No function names, no Redis keys, no file paths in skill bodies. No
supplementary design docs. If you want to explain internals, put it in code-topology
or a per-subproject CLAUDE.md, not a skill.

## Where to look

| Need | Path |
|------|------|
| Module dependencies, call paths, dead code | `docs/code-topology/` |
| Recurring E2E pitfalls / fresh-cluster blockers | `docs/troubleshooting/benchmark-integration-playbook.md` |
| Datapack / parquet schema | `docs/troubleshooting/datapack-schema.md` |
| Per-subproject build / test / run | each subproject's `CLAUDE.md` |
| Cross-repo feature linkage | `docs/cross-repo-feature-linkage.md` |
| Recovered requirements index | `project-index.yaml` |

## Doing tasks

- Prefer editing existing files; don't create new ones unless asked.
- Don't create planning / decision / analysis docs unless explicitly requested.
- For exploratory questions, give 2–3 sentences with a recommendation and the main
  tradeoff — don't implement until the user agrees.
- Match scope to what was asked. A user approving one action does not approve the
  same action in a different context.
