# Caller-provided idempotency key is the executor's primary key

The `idempotency_key` that callers supply on `POST /injections` is not
just a dedup token at the aegis-chaos boundary — it MUST be usable by
each Executor as the natural primary key of the backend resource it
creates. For Chaos-Mesh this means CR `metadata.name` is derived from
the key (e.g. `aegis-{action}-{sha256(key)[:12]}`); for ChaosBlade-CLI
the executor maintains a small internal `key → blade_uid` table; for
any future executor, the contract is "same key, same resource, always."

Today's Chaos-Mesh wrappers generate `rand.String(6)` name suffixes
(`chaos-experiment/controllers/pod_chaos.go:80`), giving no
crash-recovery guarantees: if aegis-chaos crashes between Apply
succeeding and persisting the returned handle, the in-cluster CR is
orphaned and the next retry creates a duplicate. By moving idempotency
into the resource name itself, the recovery path collapses to a normal
read — restart's reconciler sees the persisted row (whose handle was
written *before* Apply, since the name is deterministic) and resumes
status tracking with no special "applying" intermediate state.

## Considered options

- **a. Caller key is executor primary key** (chosen) — Chaos-Mesh
  treats `AlreadyExists` on POST as success; recovery is a plain Status
  call.
- **b. Two-phase persistence + Executor.FindByKey** — wider interface
  (every executor must implement key→handle lookup), special-case
  recovery path, more code paths to test.
- **c. Transactional outbox** — disproportionate infrastructure for a
  single event type.

## Consequences

- Executor.Apply contract: same key → same resource, idempotent on
  retry, recoverable by handle alone after process restart.
- Cluster operator readability: CR names lose human-meaningful suffix.
  Partially mitigated by keeping the action prefix
  (`aegis-{action}-{12hex}`). Live chaos is still findable via labels
  (system, service, capability) on the resource.
- Cross-executor portability: every new executor MUST design its
  resource naming or its key↔handle table around this contract from
  day one; this is a non-negotiable line in the §8 interface.
