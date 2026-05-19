# Batch.aggregated_status is persisted, guarded by a batch row lock

`injection_batches.aggregated_status` is a persisted column, not
derived at query time. Every transition of a child Injection's
terminal status acquires `SELECT ... FOR UPDATE` on the parent batch
row, updates the child, recomputes aggregated_status from the
post-update child set, writes it back, and on terminal transition
publishes the webhook — all in one transaction. The lock is what
prevents duplicate webhook firing and what enforces the single-source
invariant ("aggregated_status equals the aggregation of children").

The persisted-but-locked form was chosen over (b) pure derivation and
(c) persisted + drift-reconciler because: (i) `GET
/injection-batches/{id}` is a hot path for campaign polling and a
single-row SELECT beats a join, (ii) the row-lock at terminal
transition naturally serialises "who got to fire the webhook" without
an application-level dedup, (iii) batch sizes are small (N≤10 in
practice) so the lock window is microseconds, and (iv) the drift
reconciler in (c) is engineering for a failure mode that has no
evidence of occurring yet.

## Consequences

- Every code path that mutates `injections.status` MUST go through the
  batch-row-lock entry point. Non-batched (singleton) injections skip
  the lock entirely; they are guarded only by row-level idempotency on
  `(injection_id, new_status)`.
- Multi-batch updates lock in `batch_id` ascending order to avoid
  cycles. In practice only `DELETE /injection-batches/{id}` touches
  more than one row at a time, and it touches exactly one batch.
- A cancellation that hits a batch with previously-succeeded children
  still resolves to `aggregated_status = cancelled`, not `partial`.
  `partial` is reserved for batches that ran to natural completion
  with mixed outcomes; `cancelled` expresses operator intent and is
  the load-bearing signal for campaign-side policy. Per-child outcomes
  remain in `child_results`, so callers that want to mine partial data
  do so at the child level.
- **Terminal aggregated_status is sticky.** Once
  `aggregated_status` is written to any of `succeeded | partial |
  failed | cancelled`, subsequent child status transitions update
  only the child row; they do NOT recompute aggregated_status and
  they do NOT fire a second batch webhook. This closes the race
  where a child mid-Apply at cancel time completes successfully
  *after* the batch was marked cancelled — without stickiness,
  `cancelled` could flip back to `partial` and a contradictory
  second webhook would fire.
