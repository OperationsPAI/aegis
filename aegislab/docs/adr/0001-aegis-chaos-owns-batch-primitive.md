# aegis-chaos owns the Batch primitive

Today aegis-backend tracks N-of-N completion of hybrid fault-injection
batches in an in-memory `FaultBatchManager`
(`src/core/orchestrator/fault_injection.go:38`), which loses state on
backend restart (issue #305). When the new aegis-chaos HTTP service
takes over chaos lifecycle, the natural place to put batch tracking is
inside aegis-chaos: it already persists per-injection state, so adding a
`POST /v1beta/injection-batches` endpoint and an `injection_batches`
table is cheap, durable across restarts, and reduces webhook traffic
from N callbacks per batch to one. Backend's Campaign layer still owns
"what happens after the batch completes" (BuildDatapack →
RunAlgorithm → CollectResult); the Batch primitive is purely about
injection lifecycle and stays inside the chaos-non-goal boundary.

## Considered options

- **A. aegis-chaos exposes `/injection-batches`** (chosen) — durable
  N-of-N tracking, single webhook per batch.
- **B. Backend reconstructs batches from per-injection webhooks** —
  preserves the "aegis-chaos is purely a primitive" framing, but
  duplicates the existing in-memory bug into a new transport layer and
  forces O(N) webhook traffic per batch.
- **C. Move BuildDatapack triggering into aegis-chaos** — rejected;
  violates the non-goal that campaign orchestration stays in backend.
