# Point Manifest validation: offline schema plus server dry-run

Manifest authors get two validation surfaces and never have to deploy
to find out their manifest is broken.

**Offline** — `aegisctl manifest validate <file>` runs structural and
referential checks against a JSON Schema bundled in aegisctl
(Manifest envelope + every Capability's target/param schemas).
Editors and pre-commit hooks read the same `$schema` directive.
Coverage: required fields, type/enum correctness, capability names,
target conformance to capability target_schema, param_overrides as
legal narrowings of param_schema, intra-manifest consistency.
Coverage gap: cluster-side state (which System is registered, which
Executors are healthy and support which Capability in this cluster).

**Online** — `POST /v1beta/systems/{sys}/points:import?dry_run=true`
runs the full server-side validation (offline checks plus
cluster-state checks) inside a transaction that always rolls back.
Same body and same code path as the real import; the only difference
is the final commit. Output includes the supersede impact: how many
Points would transition to `superseded` if the manifest were
committed.

**Schema discovery** — `GET /v1beta/manifest-schema.json` returns the
authoritative JSON Schema. aegisctl uses its bundled copy by default;
`--fetch-schema` pulls the server's live version to catch
Capabilities added after the CLI was last released.

The Helm post-install hook installed per ADR-0009 calls both: offline
validate first (fails the Job fast), then `--dry-run`, then the real
import. Chart authors can `helm template | aegisctl manifest
validate -` to check a manifest without any cluster involvement.

## Manifest format

kubectl-style envelope — chart authors recognise it instantly:

```yaml
apiVersion: aegis-chaos/v1beta
kind: PointManifest
metadata:
  system: ts
  service: frontend
  version: v3.2.0
spec:
  replace_scope: service        # service | system | none
  points:
    - capability: http_latency
      target: { endpoint: /api/login, method: POST }
      param_overrides:
        delay_ms: { min: 100, max: 2000 }
        duration_s: { max: 60 }
    - capability: pod_kill
      target: {}
      param_overrides: {}
```

## Considered options

- **Two-surface validation** (chosen). Fast feedback offline, full
  guarantee online; one schema, both layers.
- **Server-only validation** — rejected: edit-test loop requires
  hitting aegis-chaos, slow for chart authors iterating locally.
- **CLI-only validation against a static schema** — rejected:
  cannot catch System-not-registered or Capability-not-supported-here.
