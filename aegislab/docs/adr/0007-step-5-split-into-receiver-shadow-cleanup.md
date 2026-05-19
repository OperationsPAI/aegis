# Migration step 5 splits into receiver / per-system shadow / cleanup

Cutting backend off from Chaos-Mesh CRDs is split into three serial
sub-steps so the BuildDatapack trigger chain never loses its fallback
during rollout:

- **5a** — Backend adds `/hooks/chaos-batch` receiver replicating
  `HandleCRDSucceeded` post-process logic (parent-task linkage,
  BuildDatapack submission, OTel context continuation). Code is dead
  until 5b. Lands a `(injection_id, task_type)` uniqueness gate on
  child-task submission first, so dual-firing in 5b cannot duplicate
  downstream work.
- **5b** — Per-system etcd flag
  `injection.system.<sys>.executor_authoritative`. Flipping a system
  to `aegis-chaos` routes its BatchCreate through `POST
  /injection-batches`; the receiver-path triggers BuildDatapack. The
  CRD watcher stays live and also sees the same CR — its
  `HandleCRDSucceeded` call hits the uniqueness gate and no-ops.
  Per-system soak (start with ts, then otel-demo, then the rest)
  ensures stack-specific failure modes never abort the entire
  cutover.
- **5c** — Once every system has soaked, delete the chaos GVRs from
  `platform/k8s/controller.go:109` and remove the chaos branches of
  `HandleCRDSucceeded`. This step is intentionally last because it is
  the only irreversible one.

The watchdog property — every webhook-driven trigger is **shadowed by
a still-live CRD watcher** until 5c — is what makes the migration
safely back-out-able. The cost is a longer 5b window (roughly
2 months at one-system-per-week soak); compressing it forfeits that
safety net.

## Considered options

- **a. 5a/5b/5c with per-system soak** (chosen).
- **b. 5a (receiver in shadow) + atomic 5b** — single global flag flip
  with watcher deletion in the same step; rejected because rollback
  requires redeploying backend.
- **c. Original monolithic step 5** — rejected per review:
  three operations cannot land atomically and the irreversible one
  (watcher delete) blocks rollback the moment the others go in.

## Consequences

- Backend must enforce idempotent submission of BuildDatapack on
  `(injection_id, task_type)` before 5a lands. This is a prerequisite,
  not part of 5a itself.
- During 5b, two paths concurrently observe the same CRD lifecycle;
  every downstream side-effect in the BuildDatapack chain must be
  idempotent at submit boundary, not just at task-runtime boundary.
- Step 6 ("delete chaos-experiment") cannot land before 5c.
