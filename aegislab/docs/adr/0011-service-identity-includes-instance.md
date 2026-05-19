# Service identity includes deployment instance

A **Service** is identified by the 4-tuple
`(system_name, service_name, instance, chart_version)`, not the
3-tuple `(system_name, service_name, version)` that earlier drafts
assumed. `instance` is the helm release name (or a normalised derivative
of it) — for clusters that run `hs0` / `hs1` / `ts0` / `ts1` style
multi-instance benchmarks today, each replica becomes its own Service
row even when the chart version is identical.

Without this dimension, three concrete operational pains all hit the
same place:

- **Multi-instance benchmarks are unrepresentable** (#13). Today's
  aegislab + memory note `Cloud VKE validation 2026-04-24` runs
  `otel-demo0`, `hs0`, etc. The 3-tuple's UNIQUE would force them to
  share a Service row and share a Point catalog they don't share.
- **Concurrent `helm upgrade` corrupts the catalog** (N2 of the
  second-pass review). v3.2.0's `post-delete` and v3.3.0's
  `post-install` race against the same Service row. With `instance`
  in the key, the new chart_version writes a *different* row, so the
  upgrade is atomic per (instance, chart_version) pair.
- **`helm uninstall --no-hooks` orphans the catalog** (N1). With
  `instance` as a first-class key, an external reconciler can list
  helm releases and `retire` any Service row whose
  `(instance, chart_version)` no longer matches a live release —
  catching the no-hooks leak without depending on the hook firing.

## Consequences

- `services` UNIQUE becomes `(system_name, name, instance,
  chart_version)`. Existing `version` column is renamed to
  `chart_version` (semantically the same, but spelled correctly).
- Point hash recipe absorbs `instance`:
  ```
  point_id = SHA256(
      system + "/" + service + "/" + instance + "/" + chart_version
      + "/" + capability + "/" + canonical_json(target)
  )[:16]
  ```
  Cross-service Points still omit service/instance/chart_version.
- Point Manifest envelope adds `metadata.instance` (default
  `default` for single-instance charts).
- A periodic reconciler inside aegis-chaos walks `services` rows in
  `active` state and cross-checks them against a list of live helm
  releases supplied by an `aegisctl orchestrator-side reporter`
  (which has helm read access). Services without a matching release
  for K consecutive checks transition to `retired`. K and the
  reporter shape are deferred until concrete leak incidents.
- `:import` serialises per `(system, service, instance)` via an
  application-level lock (Redis or DB row lock on a dedicated
  `import_locks` table). Concurrent imports against the same instance
  block; concurrent imports across instances proceed in parallel.

## Considered options

- **a. 4-tuple identity** (chosen).
- **b. Keep 3-tuple, model A/B variants as separate Services
  with synthesised names like `frontend-canary`** — rejected;
  service_name should reflect the application's actual identity, not
  encode deployment topology, and would silently fork manifests.
- **c. Keep 3-tuple, accept that multi-instance shares a Point
  catalog** — rejected; A/B variants are deliberately different in
  config and may have different injectable surfaces. Sharing is
  wrong.
