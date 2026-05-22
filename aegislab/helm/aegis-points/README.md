# aegis-points: chart-bound PointManifest import snippet

Reusable Helm template snippet for benchmark charts that want to ship
PointManifests alongside their workloads. Pairs with ADR-0009 and
[point-manifest-spec.md §7](../../docs/aegis-chaos/point-manifest-spec.md#7-chart-bound-integration-the-canonical-delivery-path).

This is **not** a standalone Helm chart — it's a template snippet you copy
into your benchmark chart.

## What you get

One post-install Job + one ConfigMap that:

1. Reads every `aegis-points/*.yaml` file under your chart (each is a
   PointManifest YAML, one service per file per the authoring contract).
2. Mounts them as a ConfigMap.
3. Runs `aegisctl manifest import-dir` against the configured aegis-chaos
   endpoint with `--keep-going`.

Hook weight is **-5**, slotted between the system-onboard Job (`-10`,
ships separately via #458) and your chart's workloads (`0`). So when
your services start, the system identity exists in etcd and chaos_points
is populated.

## Wiring it into your chart

1. Copy `templates/aegis-points-import-job.yaml` into your chart's
   `templates/` directory.
2. Put one manifest per `(system, service, instance)` under
   `<your-chart>/aegis-points/<service>.yaml`. See
   [`aegislab/manifests/aegis-chaos/teastore/`](../../manifests/aegis-chaos/teastore/)
   for working examples.
3. Add the required values to your chart's `values.yaml`:

   ```yaml
   aegis:
     chaosServer: http://aegis-chaos.aegis.svc:8082  # cluster-internal URL
     systemCode: my-system                            # must match metadata.system in every PointManifest
     aegisctlImage: opspai/aegisctl:v0.5.0
     serviceAccount: my-release-aegis                 # often reused from the onboard Job
     tokenSecret: my-aegis-token                      # optional; key "token"
     imagePullPolicy: IfNotPresent
   ```

4. Lint your manifests locally before pushing:

   ```bash
   for f in aegis-points/*.yaml; do
     aegisctl manifest validate "$f" || exit 1
   done
   ```

5. (Optional but recommended) Dry-run against a staging aegis-chaos
   before committing:

   ```bash
   aegisctl manifest import-dir aegis-points/ \
     --server https://staging.aegis.example.com --dry-run
   ```

## What the spec doc covers (read it first)

- The PointManifest envelope and `additionalProperties:false` posture.
- `replace_scope` semantics and the one-service-per-file convention.
- `target` per-capability shape (live capabilities endpoint).
- `param_overrides` layering and author lockdown.
- `chart_version` semantics (helm `Chart.Version`, not image SHA).
- Hook-weight ordering with #458's onboard Job.
- The two CI gates (L1 schema lint, L2 strict-mode regression).
