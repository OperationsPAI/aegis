# Terminal Injection state destroys the cluster artifact

When an Injection transitions to `succeeded` / `failed` / `cancelled`,
aegis-chaos calls `Executor.Destroy(handle)` before persisting the
terminal status. The DB row remains as the authoritative inspection
surface; the cluster-side resource (Chaos-Mesh CR, ChaosBlade
experiment, Istio fault patch) is removed. Today's pattern of leaving
Finished CRs in benchmark namespaces accumulates hundreds of stale
objects per long-running cluster, which obscures live chaos when
operators reach for `kubectl get networkchaos -A`. The DB row was
already the canonical inspection surface (`aegisctl inject list` and
the existing UI read from MySQL, not the cluster), so removing the
cluster artifact loses no real surface.

A new `cancelled` terminal status sits alongside `succeeded` / `failed`
so campaign-side policy can distinguish "operator pulled the plug" from
"executor reported failure" — they typically warrant different
downstream behaviour.

Executor.Destroy is required to be idempotent: destroying an already
absent resource MUST succeed (Chaos-Mesh returns 404 → executor treats
as success). Destroy failures do not block the status transition — they
are recorded in `injection.diagnostics` and swept by a periodic
cleanup job.
