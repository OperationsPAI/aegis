# Batch webhook reports three-state aggregate plus full child results

The Batch completion webhook (`POST /hooks/chaos-batch`) carries an
explicit three-state `aggregated_status` of `succeeded | partial |
failed`, plus a `child_results` array enumerating every child
Injection's outcome (status, point_id, ground-truth summary, brief
diagnostics). Backend's Campaign layer is the policy owner for what
"partial" means in any given campaign type — for hybrid evaluation a
partial batch is often still research-useful (see `ts hybrid eval
2026-04-30` memory note: 28% of detector_success cases were usable,
with `gt_no_spans` as the dominant dirty mode), so the Batch primitive
must not collapse partial outcomes into a single success/failure bit.

## Considered options

- **a. Strict aggregate** — succeeded iff all children succeeded, else
  failed. Rejected: would discard partial-success information that is
  research-useful in hybrid eval today.
- **b. Lenient aggregate** — succeeded iff any child succeeded.
  Rejected: hides which children actually landed.
- **c. Three-state + full child results** (chosen) — payload is
  ~2 KB for N≤10 batches; backend gets enough information to apply
  any campaign-specific policy without a second round trip.
