# §11 step 5b — residual debt

Status: chaos-service dispatch path is verified end-to-end on byte-cluster
(FI row 60, state=6, `datapack.no_anomaly` terminal event observed). The
scaffolding items below were *not* cleaned up in the step-5b residual
cleanup pass; each is paired with the trigger that should remove it.

## Not cleaned up

### 1. Chaos service still does not compute groundtruth

`crud/chaos/handler.go` (chaos service handler) stores
`chaos_injections.groundtruth = NULL`. The dispatcher computes a minimal
`{service, container}` server-side in
`core/orchestrator/dispatcher.go::renderGroundtruths` and stuffs it into
`caller_metadata.groundtruths`, which the webhook handler then writes onto
the shadow `fault_injections` row. Without that, the algorithm container's
`prepare_inputs.py` would assert non-empty `ground_truth` and fail.

**Trigger for cleanup**: chaos service grows native groundtruth derivation
(open question on chaos-service repo). When that lands, drop
`renderGroundtruths` from the dispatcher and stop writing
`caller_metadata.groundtruths` — the shadow upsert can then pull from
`chaos_injections.groundtruth` instead.

### 2. CRD watcher coexistence is still required

The in-process CRD informer (`core/orchestrator/k8s_handler.go`) is the
sole writer of `fault.injection.started` for the legacy `chaos-mesh-direct`
path, and is the sole emitter of `fault.injection.completed` for the same
path. The chaos-service path now emits both events itself:

- `fault.injection.started` from `executeFaultInjection` (this pass).
- `fault.injection.completed` from the webhook handler via the gate path
  (already in place).

**Trigger for cleanup**: §11 step 5c, after the chaos-service path soaks
for two release windows without a fall-back. At that point:

- Remove the CRD watcher startup wiring.
- Remove `executorPathChaosMeshDirect` and the per-system flag.
- Remove the `dispatchPath != executorPathChaosService` gate in
  `fault_injection.go` around `CreateInjection`.

### 3. Dual natural keys on `FaultInjection`

`model.FaultInjection.Name` (legacy `{ns}-{service}-{fault}-{hash}`) and
`ChaosInjectionID` (chaos-service ULID) both exist. The `chaos_injection_id`
column already carries an index (`gorm:"size:64;index"` on the model). For
shadow rows the dispatcher fills `Name` with `caller_metadata.datapack.name`
which the dispatcher sets to the task UUID — unhelpful for audit queries
by name pattern.

**Trigger for cleanup**: when the same audit query needs to span legacy and
chaos-service rows (currently they live in disjoint time windows). At that
point change `dispatcher.buildCallerMetadata` so `datapack.name` carries
`{ns}-{service}-{fault}-{ulid-prefix}` and add a migration to backfill
historical rows. Not done in this pass because no caller queries by name
across the two windows yet, and the change is a migration concern.

### 4. `--via-chaos` flag on `regression run` is deprecated but still bound

The flag now errors out instead of routing the submit through chaos
service. Kept bound so users with `AEGIS_INJECT_VIA_CHAOS=1` in their
shell get a clear deprecation message instead of an unknown-flag error.

**Trigger for cleanup**: drop after one release cycle (no callers in CI).
The standard backend submit already exercises the chaos-service path via
the per-system executor flag, so the case is fully covered.

### 5. Shadow FI's defensive FK retry

`crud/hooks/chaos/handler.go::getOrCreateShadowFaultInjection` writes the
shadow row with `TaskID = meta.TaskID` when `meta.HasBackendTask` is true,
then on Create error retries with `TaskID=nil`. The retry is belt-and-braces:
both real backend dispatcher and aegisctl `--via-chaos` flows pass the
correct `HasBackendTask` today. The retry catches the case where the
backend task row was deleted out from under an in-flight webhook (FK
violation race).

**Trigger for cleanup**: drop when there's an integration test asserting
backend-dispatcher tasks are never GC'd while their `fault_injections` row
is missing. Until then, keep the defensive retry.
