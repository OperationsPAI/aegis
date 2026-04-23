---
name: regression-e2e
description: >-
  Triage frame for end-to-end (E2E) test, smoke test, regression, and
  fault-injection validation failures. Use when the user reports an
  E2E/smoke/regression run is broken, a fault-injection flow fails
  mid-pipeline, a previously-green test is now flaky, or asks to "triage",
  "debug the E2E", "why is the regression failing", "help me figure out what
  broke the smoke test". Produces a short hypothesis list and next inspection
  surfaces rather than a full runbook. Trigger words: e2e, end-to-end,
  regression, smoke test, fault injection validation, flaky test, test broke.
---

# Regression / E2E Triage

Provide a short triage frame for E2E failures. Focus on likely problem classes and next inspection directions rather than detailed runbooks.

## Triage Order

1. Confirm the test contract still matches product behavior.
   - Stale expected terminal events, outdated fixtures, renamed statuses, old snapshots.
2. Confirm the environment is actually ready.
   - Service health, cluster dependencies, seeded config, storage, queues, required ports.
3. Confirm version alignment across the stack.
   - Local CLI / test harness, deployed backend, worker images, schemas, container tags — look for drift.
4. Confirm auth and config wiring.
   - Tokens, permissions, endpoints, namespaces, feature flags, project/tenant resolution.
5. Confirm rerun semantics.
   - Dedupe, idempotency, cleanup, leftover state that blocks identical reruns.
6. Confirm the data path completes.
   - Fault creation, task execution, artifact generation, uploads, persistence, downstream reads.
7. Confirm observability is sufficient.
   - Follow one trace / job / execution end-to-end across logs, events, and DB state.

## Common Problem Buckets

- **Stale contract** — product is healthy, but the test asserts an old event, payload, or sequence.
- **Environment drift** — docs, fixtures, or assumptions no longer match the real local or CI environment.
- **Version drift** — wrong binary/image is running, or a remote pull overrides the intended local build.
- **Auth or config mismatch** — the flow starts, but later steps fail because required creds, flags, or routing are missing.
- **Timing / race conditions** — async consumers, startup latency, or eventual consistency make the test flaky.
- **Replay / dedupe behavior** — a new run is suppressed because an older valid run or cached state still exists.
- **Missing result data** — workflow appears complete, but artifacts, uploads, or DB rows are incomplete/absent.
- **Weak observability** — user-facing error is generic; the real blocker is only visible in trace or runtime logs.
- **Selector/label mismatch from operators** — app-label-based selectors return a partial set of services because operator-generated pods (Coherence, Kafka, Postgres controllers) carry different labels than plain Deployments. Pattern to watch: "everything installs healthy, but the selector only sees N of M services."
- **Bootstrap-edge gaps** — seed path populates some stores (DB, configmap, etc.) but not the one the runtime actually reads on first boot. Symptom: the data is "there" somewhere, but the process that needs it gets `not found` the first time and works after a manual reconciliation.
- **Duplicate-submission suppression** — aegisctl regression silently refuses re-submitting the same (app, namespace, chaos_type, duration) within a cooldown; symptom is `duplicate submission suppressed (batches [0])`. Vary `spec[].app` or `duration` to force a fresh hash.
- **App name filtered by serviceendpoints** — backend pre-filters live pod labels against the `serviceendpoints` metadata store before returning them to guided-resolve. A pod WITH the label (e.g. `currency` in otel-demo) still errors `app ... not found; available apps: accounting, ad, cart, ...` if it isn't in the endpoint map. Pick an app that IS in the "available apps" list the error prints.
- **Empty parquet with `.parquet` suffix required** — `RCABENCH_OPTIONAL_EMPTY_PARQUETS` values match on the full filename INCLUDING `.parquet`. Leaving the extension off silently fails to opt in.

## Output Shape

- Name the most likely blocker classes first.
- Separate contract problems from product bugs, environment bugs, and tooling bugs.
- Point to the next inspection surface instead of writing a full playbook.
- Prefer concise hypotheses like "check X because Y" over exhaustive detail.

## Project-specific inspection surfaces (aegis)

When triaging in this repo, common concrete surfaces worth naming early:

- `aegis/docs/troubleshooting/{datapack-schema,app-label-key,benchmark-integration-playbook}.md` — consolidated E2E pitfalls.
- `aegis/docs/deployment/kind/otel-collector-{cfg,rbac,externalname}.yaml` — kind-profile collector manifests (k8sattributes + upsert + 3-signal pipelines).
- `pkg/guidedcli` — guided-first inject pipeline; `/translate` and `GET /metadata` are 410.
- etcd `injection.system.*` — runtime source of truth for injection config (not YAML).
- ClickHouse OTLP traces + Redis task keys — the usual "did the data path complete" inspection.
- Per-stage failure triage: `datapack.build.failed` → inspect the job pod logs (`kubectl logs -n exp <task-uuid>-xxxx`) for UNKNOWN_TABLE (collector missing pipeline), `Parquet file has no data rows: X.parquet` (wrong env-var filename or missing resource enrichment), or `No such file or directory: /data/drain_template/*.bin` (initDrainTemplate disabled but detector algo still wired in).
- `--app-label-key` must match the workload's actual label key (otel-demo uses `app.kubernetes.io/name`, not the default `app`). Mismatch surfaces as "app not found; available apps: <subset>" at backend submit.
