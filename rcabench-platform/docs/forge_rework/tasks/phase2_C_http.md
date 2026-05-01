# Phase 2 Family C — HTTP-level injection

**Owner**: 1 agent (`mfst-C`)
**Worktree**: yes
**Reads first**: `phase2_template.md`, then `SCHEMA.md`

## Fault types in scope (9)

| fault_type_name | seed_tier | target_kind | mechanism |
|---|---|---|---|
| `HTTPRequestAbort` | erroring | service (or span) | Inbound request aborted before processing |
| `HTTPRequestDelay` | slow | service | Inbound request held before processing |
| `HTTPRequestReplacePath` | erroring | service | Inbound path mangled → 4xx |
| `HTTPRequestReplaceMethod` | erroring | service | Inbound method mangled → 405 |
| `HTTPResponseAbort` | erroring | service | Outbound response aborted |
| `HTTPResponseDelay` | slow | service | Outbound response delayed |
| `HTTPResponseReplaceBody` | erroring | service | Response body mangled |
| `HTTPResponsePatchBody` | erroring | service | Response body partially mangled |
| `HTTPResponseReplaceCode` | erroring | service | Status code forced non-2xx |

## Sample injection directories

```bash
ls /home/ddq/AoyangSpace/dataset/rca/ | grep -iE 'abort|delay|exception|return' | head -30
```

NB: HTTP-prefix is implicit in the dataset; many fault types may surface as `*-abort-*` or `*-delay-*` patterns.

## Family-specific guidance

### HTTP faults manifest at span level FIRST

Unlike resource pressure (which manifests at container/pod level first, then propagates to spans), HTTP faults are **directly observable at spans**:

- Entry signature is a `span` feature: `error_rate`, `latency_p99_ratio`, or specific HTTP status indicators.
- Container/pod-level features are NOT typically affected (the container is healthy; only its responses are mangled by the chaos sidecar).
- This means `target_kind: service` or `span` (decide based on injection point in `injection.json`).

### Abort vs Delay split

- **Abort variants** (Request/Response/ReplaceCode): entry = `span.error_rate >= 0.5` within entry_window_sec. Latency doesn't help; failures are immediate.
- **Delay variants** (RequestDelay, ResponseDelay): entry = `span.latency_p99_ratio >= 5.0`. Error rate stays low if downstream tolerates wait.
- **Replace-Path/Method**: entry = mix of `error_rate >= 0.3` AND specific 4xx pattern. If feature vocabulary lacks 4xx-specific feature, fall back to `error_rate` alone and document the gap.
- **Replace/Patch Body**: entry = caller-side error if caller validates payload; else benign. Document as ambiguous.

### Cascade is short

HTTP faults rarely propagate >2 hops because:
- Callers either tolerate, retry, or fail fast.
- Failures don't compound the same way resource pressure does.

So most HTTP manifests should have **layer 1 only**, possibly layer 2 for upstream callers showing matching error patterns.

### Hand-off candidates

- HTTPResponseDelay → CPUStress-like pattern at downstream callers (their threads block on the slow response). Tempting but **don't add this hand-off without empirical evidence** — it's the wrong direction (CPU stress is upstream of HTTP delay typically). Skip unless data forces it.
- HTTPResponseAbort and HTTPResponseReplaceCode are common universal trigger destinations — make sure their entry_signature accepts `span.error_rate >= 0.2` so cross-family hand-offs land cleanly.

### Derivation layer hints

- Layer 1: spans on the same service (the directly affected ones via `includes forward`). Expected: `error_rate` or `latency_p99_ratio`.
- Layer 2: caller spans via `calls backward`. Expected: matching error/latency, possibly `timeout_rate`.

## Acceptance bar (family C)

- 9 manifests, all using span-level entry signatures.
- Abort/Delay split is reflected in the chosen entry features.
- HTTPResponseAbort and HTTPResponseReplaceCode entry signatures accept `span.error_rate >= 0.2` (so cross-family hand-offs work).
- No speculative cross-family hand-offs; document any case where data is ambiguous.
