# Phase 2 Family E — JVM application-level injection

**Owner**: 1 agent (`mfst-E`)
**Worktree**: yes
**Reads first**: `phase2_template.md`, then `SCHEMA.md`

## Fault types in scope (5)

| fault_type_name | seed_tier | target_kind | mechanism |
|---|---|---|---|
| `JVMLatency` | slow | container | Method-level injected delay (specific Java method) |
| `JVMReturn` | erroring | container | Method returns forced value (semantic error) |
| `JVMException` | erroring | container | Method throws thrown exception |
| `JVMMySQLLatency` | slow | container | Latency injected at JDBC call boundary |
| `JVMMySQLException` | erroring | container | JDBC call throws exception |

## Sample injection directories

```bash
ls /home/ddq/AoyangSpace/dataset/rca/ | grep -iE 'jvm|exception|return' | head -20
```

Note: TrainTicket is JVM-only, so all services use these. Cross-reference `injection.json` for `fault_type` ints 23–28.

## Family-specific guidance

### JVM faults are method-scoped, not container-wide

Unlike CPUStress/MemoryStress which affect the whole container, JVM-level injections target a specific method:
- `JVMLatency`: `XService.processOrder()` gets `+5000ms` artificial wait.
- `JVMException`: `Y.foo()` always throws RuntimeException.
- This means **per-method spans** show the effect, not all spans on the service.

Implication for entry signature:
- Use `span.error_rate` or `span.latency_p99_ratio` at the **service level** since manifest doesn't (yet) target individual operations.
- Acknowledge in the evidence MD that this is a coarsening — the ground truth is method-scoped but the manifest matches at service-scope.

### JVMReturn is subtle

Method returns "wrong" but technically valid value (e.g., null instead of object, 0 instead of count). Caller may:
- Treat as success (silent corruption — invisible from outside)
- Crash with NullPointerException downstream (visible as JVMException-like cascade)

Entry signature should be lenient: `error_rate >= 0.1` OR specific null-deref patterns. If the caller is robust, this fault may be Class E (silent absorption). Document this.

### JVMMySQL-prefixed are DB-call-specific

- `JVMMySQLLatency` targets only the JDBC call boundary, not all methods. Spans on the DB-touching methods see latency; other spans don't.
- `JVMMySQLException` similar; affected spans show exception. Caller services that don't call DB don't see anything.

### Derivation layer hints

- Layer 1: caller spans via `calls backward`. Expected: `error_rate` or `latency_p99_ratio`.
- Layer 2: transitive callers. Expected: similar but decayed.
- Generally 1–2 layers; rare to propagate 3+ hops because Java services often catch exceptions at boundaries.

### Hand-off candidates

- JVMException with high frequency (`error_rate >= 0.5`) → HTTPResponseAbort (universal trigger).
- JVMLatency at very high magnitude (`latency_p99_ratio >= 10.0`) → HTTPResponseDelay (downstream sees timeout).
- JVMMySQLException → JVMException (same family, same observable pattern at caller level).

### Augmentation labels

The IR adapters emit specialization labels for JVM faults: `frequent_gc`, `high_heap_pressure`, `oom_killed`. Phase 1's feature vocabulary may not yet have these as first-class features. Use them in `optional_features` if they help discriminate; flag missing ones to orchestrator.

## Acceptance bar (family E)

- 5 manifests, with explicit acknowledgment in evidence MD of the method-vs-service granularity gap.
- `JVMReturn`'s ambiguity (silent vs cascading) explicitly handled.
- DB-specific faults distinguish themselves from generic JVM faults via service-side context (DB-touching vs not).
- All hand-offs justified empirically; no speculative additions.
