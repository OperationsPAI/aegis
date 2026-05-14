# aegislab Helm chart

Standard chart commands (`helm install`, `helm upgrade`, `helm template`)
apply. This README documents only opt-in features that aren't obvious from
`values.yaml`.

## Multi-environment manifest (`/.well-known/aegis-environments.json`)

The aegis-api server can publish a list of sibling deployments (prod / stage
/ dev / …) so a single UI build can switch between them. The manifest is
served from `GET /.well-known/aegis-environments.json` and is consumed by
the `useEnvironmentManifest()` hook in the UI.

To enable it:

1. Set `aegisEnvironments.enabled: true`.
2. Populate `aegisEnvironments.manifest` with a `default` id and one entry
   per environment (see `values-multi-env.example.yaml`).
3. Apply the **same** values file to every release (prod, stage, dev) so
   they all advertise the identical environment list.

```bash
helm install aegis ./helm -f ./helm/values-multi-env.example.yaml
```

The chart renders a ConfigMap (`<release>-aegis-environments`) carrying
`manifest.json` and injects it into the aegis-api container as the
`AEGIS_ENVIRONMENTS_JSON` env var. The handler validates the payload at
startup. `badge` accepts `default | info | warning | danger`; `default` must
match one of the listed `id`s.

When `aegisEnvironments.enabled` is `false` (the default) no ConfigMap is
rendered, no env var is injected, and the handler returns `404` so single-
env installs behave exactly as before. Prefer this Helm flag over editing
`config.prod.toml` so multi-env state stays declarative.
