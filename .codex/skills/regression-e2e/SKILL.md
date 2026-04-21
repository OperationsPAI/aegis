---
name: regression-e2e
description: Outline broad troubleshooting directions for end-to-end (E2E) tests, smoke tests, regression runs, and fault-injection validation. Use when Codex needs to explain likely blockers, organize a triage plan, or summarize what to inspect next across stale test contracts, environment readiness, auth/config wiring, image or binary drift, flaky timing, replay or dedupe behavior, missing result data, and observability gaps.
---

# Regression / E2E

Provide a short triage frame for E2E failures. Focus on likely problem classes and next inspection directions rather than detailed runbooks.

## Triage Order

1. Confirm the test contract still matches product behavior.
   - Check for stale expected terminal events, outdated fixtures, renamed statuses, or old snapshots.
2. Confirm the environment is actually ready.
   - Check service health, cluster dependencies, seeded config, storage, queues, and required ports.
3. Confirm version alignment across the stack.
   - Check local CLI or test harness, deployed backend, worker images, schemas, and container tags for drift.
4. Confirm auth and config wiring.
   - Check tokens, permissions, endpoints, namespaces, feature flags, and project or tenant resolution.
5. Confirm rerun semantics.
   - Check dedupe, idempotency, cleanup, and leftover state that can block identical reruns.
6. Confirm the data path completes.
   - Check fault creation, task execution, artifact generation, uploads, persistence, and downstream reads.
7. Confirm observability is sufficient.
   - Follow one trace, job, or execution end-to-end across logs, events, and database state.

## Common Problem Buckets

- Stale contract
  - The product is healthy, but the test still asserts an old event, payload, or sequence.
- Environment drift
  - The docs, fixtures, or assumptions no longer match the real local or CI environment.
- Version drift
  - The wrong binary or image is running, or a remote pull overrides the intended local build.
- Auth or config mismatch
  - The flow starts, but later steps fail because required credentials, flags, or routing are missing.
- Timing or race conditions
  - Async consumers, startup latency, or eventual consistency make the test flaky or misleading.
- Replay or dedupe behavior
  - A new run is suppressed because an older valid run or cached state still exists.
- Missing result data
  - The workflow appears complete, but artifacts, uploads, or DB rows are incomplete or absent.
- Weak observability
  - The user-facing error is generic, so the real blocker is only visible in trace or runtime logs.

## Output Shape

- Name the most likely blocker classes first.
- Separate contract problems from product bugs, environment bugs, and tooling bugs.
- Point to the next inspection surface instead of writing a full playbook.
- Prefer concise hypotheses like "check X because Y" over exhaustive detail.
