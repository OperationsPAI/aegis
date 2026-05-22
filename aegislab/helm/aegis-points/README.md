# aegis-points: Helm library chart for chart-bound PointManifest import

`type: library` Helm chart that consumer benchmark charts declare as a
dependency to wire up chart-bound PointManifest delivery to aegis-chaos.
Pairs with ADR-0009 and
[point-manifest-spec.md §7](../../docs/aegis-chaos/point-manifest-spec.md#7-chart-bound-integration-the-canonical-delivery-path).

A library chart emits no resources of its own; it exposes named templates
that consumer charts render via `{{ include }}`.

## What `aegis-points.job` emits

When `.Values.aegis.points.enabled` is `true` AND the consumer chart has
at least one file matching `aegis-points/*.yaml`, the include emits:

1. A `ConfigMap` with every `aegis-points/*.yaml` mounted as a key
   (hook weight `-6`).
2. A `Job` that runs `aegisctl manifest import-dir /etc/aegis-points`
   on `post-install` / `post-upgrade` (hook weight `-5`, `backoffLimit: 0`).

Slots between the system-onboard Job (`-10`, ships separately via #458)
and the consumer chart's workloads (`0`).

If `.Values.aegis.points.enabled` is unset/false **or** there are no
`aegis-points/*.yaml` files, the include emits nothing. Opt-in by
construction — a chart can ship the include before authoring any
manifests.

## Three-line consumer setup

1. Declare the dependency in your chart's `Chart.yaml`:

   ```yaml
   dependencies:
     - name: aegis-points
       version: 0.1.0
       repository: file://../../aegislab/helm/aegis-points
   ```

2. Render the include from a new template file in your chart
   (e.g. `templates/aegis-points.yaml`):

   ```yaml
   {{ include "aegis-points.job" . }}
   ```

3. Put one PointManifest per service under your chart at
   `aegis-points/<service>.yaml` and add the required values:

   ```yaml
   aegis:
     chaosServer: http://aegis-chaos.aegis.svc:8082
     points:
       enabled: true
       keepGoing: false        # default false — fail closed on any per-file error
     aegisctlImage: opspai/aegisctl:v0.5.0
     serviceAccount: ""        # optional; defaults to "<Release.Name>-aegis"
     tokenSecret: ""           # optional; Secret name with key "token"
     imagePullPolicy: IfNotPresent
   ```

See [`aegislab/manifests/aegis-chaos/teastore/`](../../manifests/aegis-chaos/teastore/)
for working PointManifest examples — `teastore-recommender.yaml` exercises
`pod_failure`, `cpu_stress`, `http_request_abort`, `jvm_method_latency`,
and `memory_stress` together.

## Pre-flight checks

Lint manifests locally:

```bash
for f in aegis-points/*.yaml; do
  aegisctl manifest validate "$f" || exit 1
done
```

Dry-run against a staging aegis-chaos before committing:

```bash
aegisctl manifest import-dir aegis-points/ \
  --server https://staging.aegis.example.com --dry-run
```

## Why `keepGoing: false` by default

The onboard Job has already succeeded by the time this Job runs, so the
system identity exists. Failing-closed on a per-file error keeps
`chaos_points` in a coherent state — a half-imported catalog is harder
to debug than a loud install failure. Set `keepGoing: true` only when
you're knowingly bulk-importing across known-bad files and intend to
clean up afterwards.
