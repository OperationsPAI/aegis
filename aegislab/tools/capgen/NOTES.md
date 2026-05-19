# capgen — notes for reviewer

## Count
- 32 capabilities emitted (matches all `ChaosTypeMap` entries in
  `chaos-experiment/handler/handler.go`). The design doc cited
  `handler.go:90-134`; current file has the block at lines 101-134, and
  the previously-cited count of "~17/~18" was stale.
- 14 capabilities have a `TODO:` observable contract or probe (see
  `capabilities.md`, the `*(TODO contract)*` markers).

## Why a hand-maintained table, not AST/reflection

The design appendix asks the generator to walk the operator spec structs
to derive target + param fields. I did not do this. Reasons:

1. The shape upstream uses is **not the inter-lingua shape**. Upstream
   spec structs carry `AppIdx`, `NetworkPairIdx`, `EndpointIdx`,
   `MethodIdx`, `ContainerIdx`, `Duration int (minutes)`, etc. — these
   are **indices into per-system resource pools** plus minute-based
   durations. The §3 inter-lingua wants `app`, `namespace`,
   `target_service`, `class`, `method`, etc. and `duration_s`. There is
   no mechanical mapping between them; it requires per-capability
   judgement of "what does this *mean* at the Capability level."
2. Several capabilities (`JVMRuntimeMutator`, `HTTPRequestReplaceMethod`,
   `DNSError`) compute their effective parameters at runtime from
   resource-lookup tables, not from the spec struct alone. AST walking
   would emit incorrect schemas.
3. An invariant test (`main_test.go`) parses `ChaosTypeMap` and fails
   if the upstream enum set drifts from this file, which is the actual
   guardrail the design doc cares about. It catches the failure mode
   ("upstream added a new chaos type, capgen missed it") without
   needing AST machinery.

## Capabilities with intentionally-vague contracts (TODO)

These are flagged honestly rather than faked:

| name | reason |
|------|--------|
| `http_response_replace_body` | parse-error visibility depends on client instrumentation; many clients accept empty bodies silently |
| `http_response_patch_body` | response-body diffs not visible in spans by default; would need response-body capture or downstream error correlation |
| `dns_error` | resolver TTL cache can mask DNS faults for the full TTL window; effect is path-dependent |
| `dns_random` | manifestation depends entirely on caller retry/timeout/TLS config; no universal observable |
| `time_skew` | no universal trace signature; manifestation is app-semantic (JWT expiry, log skew, scheduled jobs) |
| `network_duplicate` | TCP dup-ACK handling absorbs duplicates silently; no robust trace signal |
| `network_corrupt` (note) | TLS vs raw TCP behaviour differs sharply |
| `network_bandwidth` (note) | only payloads > buffer exhibit the cap |
| `jvm_method_latency` | only methods wrapped by a span are observable; many internal helpers won't surface |
| `jvm_method_return` | downstream effect depends on caller validation (NPE / business error / silent corruption) |
| `jvm_gc` | needs JVM metrics exporter (Micrometer/JMX); fall back to span latency spike otherwise |
| `jvm_memory_stress` | stack-mode case surfaces as StackOverflowError, not memory metric |
| `jvm_mysql_latency` | requires Connector/J instrumentation + `db.statement` coverage in traces |
| `jvm_runtime_mutator` | mutation effect is by definition app-semantic; no generic assertion holds |

Step 2 workers should **not promote any of these to `stable`** without
adding a per-stack functional check or a backend-specific probe. Only
`pod_kill` is marked `stable` (matches the step-1 seed already in
`crud/chaos/migrations.go`).

## Naming convention

The design doc text says "kebab-case" in one place but every concrete
example in the design doc (`http_latency`, `pod_kill`) and the
already-landed seed (`pod_kill` in `seed_test.go`) uses snake_case.
This generator follows the code (CLAUDE.md principle 5: code is source
of truth).

## Discrepancy noted but NOT fixed (out of scope for this PR)

- Design doc §14 says "appendix is a placeholder, generator lives at
  `aegis-chaos/cmd/capgen/`". The actual generator I added lives at
  `aegislab/tools/capgen/` because there is no `aegis-chaos/` Go
  package yet (the in-cluster service is a sub-binary of aegislab at
  `aegislab/src/cmd/aegis-chaos/`, sharing the `aegis` Go module).
  Moving it to a future standalone module is a step-5+ concern.
- The design doc cites `handler.go:90-134`. Actual location at HEAD is
  `handler.go:101-134`. Doc could be updated to "the `ChaosTypeMap`
  block, currently around line 100" to avoid future drift; left alone
  per task scope ("DO NOT touch the design doc").

## Step-2 worker handoff

`output/capabilities.json` is the input. Each entry maps 1:1 to a row in
the `chaos_capabilities` table. The per-entry `target_schema` /
`param_schema` / `observable_contract` are intended to drop directly
into `SeedCapabilities` in `crud/chaos/migrations.go`. The matching
chaos-mesh executor in `crud/chaos/executor_chaosmesh.go` needs to
translate the Capability shape into the upstream spec struct shape
(handle the `AppIdx`/`MethodIdx`/etc. indirection by resolving from the
new `chaos_services` / endpoint tables).
