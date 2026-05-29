# PointManifest authoring contract

Status: shipped 2026-05-22. Pairs with [ADR-0008](../adr/0008-discovery-is-external-chart-version-binds-point-manifest.md),
[ADR-0009](../adr/0009-point-manifest-delivery-via-helm-post-install-job.md),
[ADR-0010](../adr/0010-point-manifest-validation-offline-plus-dry-run.md).

This is the contract any PointManifest emitter (manual editor, scripted
generator, scaffolded-from-cluster tool) must honor. The contract is the
schema bundled in aegisctl plus the per-capability `target_schema` /
`param_schema` served by the live API. Anything that satisfies both is
guaranteed to import; anything that doesn't will be rejected at one of two
boundaries — `aegisctl manifest validate` (offline envelope) or
`POST /v1beta/systems/{sys}/points/import` (online, full per-capability
JSON Schema enforcement; see #463).

## 1. Format

```yaml
apiVersion: aegis-chaos/v1beta
kind: PointManifest
metadata:
  system: ts                   # required — system code, must match a row registered via system onboard
  service: ts-auth-service     # required — microservice name inside the system
  instance: default            # optional — defaults to "default"; use to disambiguate sharded deployments
  chart_version: "1.4.2"       # required — helm Chart.Version of the benchmark chart, NOT the image SHA
spec:
  replace_scope: service       # service | system | none — see §3
  points:
    - capability: pod_kill
      target:
        namespace: ts
        app: ts-auth-service
      param_overrides:         # optional — see §5
        duration_s: 30
```

The envelope is enforced by [`aegislab/src/cli/cmd/manifest_schema.json`](../../src/cli/cmd/manifest_schema.json).
`additionalProperties: false` is set at every level — unknown keys are rejected
offline before they reach the server. This mirrors the server-side hardening
landed in #463.

## 2. One service per file

**Default rule: one manifest per `(system, service, instance)` triple, in a
file named after the service.** The on-repo seed layout follows this:

```
aegislab/manifests/aegis-chaos/<system>/<service>.yaml
aegislab/manifests/aegis-chaos/<system>/<service>-<variant>.yaml   # variant overlays, e.g. -A1b
```

Rationale:

- **Diff/blame** is per-service. Adding a network fault to `ts-auth-service`
  doesn't churn the `ts-order-service` history.
- **Atomic import** per service. `replace_scope: service` lets a single file
  fully own the catalog for that service; importing it supersedes the
  previous version without touching any sibling service.
- **Generator scaling.** A scaffolder can emit one file per service in
  parallel without contention; reviewers see one PR-changed-file per
  affected service.

A single mega-manifest covering the whole system is technically supported
(`replace_scope: system`) but discouraged. Use it only for first-time
bootstrap of an unfamiliar system; convert to per-service files in the
next iteration.

## 3. `replace_scope` semantics

Imported manifests don't merge with existing points — they *replace* a
window of the catalog. The scope of the window is controlled by
`spec.replace_scope`:

| Scope     | Window                                                   | What gets superseded                                                                 |
|-----------|----------------------------------------------------------|--------------------------------------------------------------------------------------|
| `service` | `(system, service, instance)`                            | Every existing point in that triple whose `id` is not re-emitted in this manifest    |
| `system`  | `(system, *, *)`                                         | Every existing point in the whole system not re-emitted; only safe for full reseed   |
| `none`    | nothing                                                  | Pure append — never supersedes; manifest must avoid id collisions with existing rows |

`service` is the right default for chart-bound delivery — one file per
service, each manifest fully owns its slice.

## 4. `target` field — per-capability shape

The `target` object's allowed keys vary by capability. The authoritative
source is the live capabilities endpoint:

```bash
curl -s $AEGIS_SERVER/v1beta/capabilities | jq '.data[] | {name, target_schema, param_schema}'
curl -s $AEGIS_SERVER/v1beta/capabilities/pod_failure
```

The bundled envelope schema does **not** encode per-capability target shapes.
Two reasons:

1. The capability registry is data, not code — new capabilities land via
   server-side seed migration, not aegisctl release.
2. Server-side validation (#463) enforces these schemas at import; the CLI's
   `manifest validate --fetch-schema` fetches the live JSON Schema and
   reproduces the same decision offline.

Worked examples for the five most common capabilities are kept in-tree at
[`aegislab/manifests/aegis-chaos/`](../../manifests/aegis-chaos/) — see
`teastore-recommender.yaml` for `pod_failure` / `cpu_stress` /
`http_request_abort` / `jvm_method_latency` / `memory_stress` together.

Every point's `target` schema has `additionalProperties: false` injected
server-side regardless of how the seed was authored (see
`schema_validate.go:cloneStrictObjects`) — so the cost of getting
`target.namespace` wrong is a 400, not a silent no-op at chaos-mesh apply.

## 5. `param_overrides` layering

Effective runtime params come from three layers; each layer must satisfy
`capability.param_schema` (per #463):

```
capability defaults  (param_schema defaults / required)
        ↓
manifest param_overrides   (author lockdown — author wins)
        ↓
runtime params from caller (validated as a complete params instance)
```

- At **import**, `param_overrides` is validated against a subset clone of
  `param_schema` (`required` stripped at object positions; unknown keys
  still rejected) — overrides may be partial.
- At **injection submit**, the deep-merge of caller params with point
  `param_overrides` is validated as a **complete** params instance.
  Override wins on key conflicts.

`param_overrides` is how you pin a value the caller must not change
(e.g. `duration_s: 30` to cap blast radius). Omit a key entirely if you
want the caller free to set it.

## 6. `chart_version` semantics

`metadata.chart_version` is the helm `Chart.Version` of the benchmark
chart that ships this manifest, e.g. `"1.4.2"`. It is **not** the image
SHA or the system version.

Bumping `chart_version` is how the catalog rotates — each install of a
new chart version writes a fresh PointManifest row binding to that
version. Historical rows survive for reproducibility (ADR-0008).

In chart-bound delivery (§7), the import overrides whatever `chart_version`
is baked into the manifest file with the consumer chart's actual
`.Chart.Version`: the library Job runs `aegisctl manifest import-dir
--chart-version {{ .Chart.Version }}`. So authors can leave a placeholder
(e.g. `seed-genesis`) in the file — the version that lands in `chaos_points`
is the chart's, which is what the supersede logic keys on. Standalone
`aegisctl manifest import` without `--chart-version` uses the file value
verbatim.

## 7. Chart-bound integration (the canonical delivery path)

Chart authors wire up chart-bound PointManifest delivery by declaring the
`aegis-points` Helm **library chart** as a dependency. The library chart
lives in-tree at [`aegislab/helm/aegis-points/`](../../helm/aegis-points/)
and exposes a single named template, `aegis-points.job`, defined in
[`aegislab/helm/aegis-points/templates/_aegis-points.tpl`](../../helm/aegis-points/templates/_aegis-points.tpl).

When the include is rendered with `.Values.aegis.points.enabled: true`
AND the consumer chart ships at least one `aegis-points/*.yaml`, the
template emits a `post-install,post-upgrade` Job + ConfigMap that POSTs
every manifest to `/v1beta/systems/{sys}/points/import`. Both conditions
together — the value flag and the presence of manifest files — keep the
include opt-in. A chart can declare the dependency and add the
`{{ include }}` line before authoring its first manifest with no
behavioural change.

Hook weight ordering pairs with the system-onboard Job from #458:

| Weight | Hook                                            | Purpose                                       |
|--------|-------------------------------------------------|-----------------------------------------------|
| `-10`  | aegis-onboard-job (#458)                        | Register the system identity + chart binding  |
| `-6`   | aegis-points ConfigMap (this doc, ADR-0009)     | Mount manifests for the import Job to read    |
| `-5`   | aegis-points-import Job (this doc, ADR-0009)    | Fill in chaos_points for the system           |
| `0`    | consumer chart workloads                        | Benchmark services start                      |

By the time workloads come up, the system row exists in etcd, the chart
binding exists in MySQL, and `chaos_points` has every point this chart
ships. No manual `aegisctl manifest import` step.

### Chart author setup (three lines)

1. Declare the library chart dependency in your chart's `Chart.yaml`:

   ```yaml
   dependencies:
     - name: aegis-points
       version: 0.1.0
       repository: file://../../aegislab/helm/aegis-points
   ```

   (Run `helm dependency update` after.)

2. Add a tiny template file in your chart (e.g.
   `templates/aegis-points.yaml`) containing only:

   ```yaml
   {{ include "aegis-points.job" . }}
   ```

3. Put one PointManifest per service under `aegis-points/<service>.yaml`
   inside your chart, and set the required values in `values.yaml`. The
   `metadata.chart_version` in each file is a placeholder — the library Job
   overrides it with `{{ .Chart.Version }}` at import time (§6), so a single
   literal like `seed-genesis` is fine across all files:

   ```yaml
   aegis:
     chaosServer: http://aegis-chaos.aegis.svc:8082
     points:
       enabled: true
       keepGoing: false        # default — fail closed on any per-file error
     aegisctlImage: opspai/aegisctl:v0.5.0
     # Optional:
     serviceAccount: ""        # defaults to "<Release.Name>-aegis"
     tokenSecret: ""           # Secret name with key "token"
     imagePullPolicy: IfNotPresent
   ```

By default the Job uses `backoffLimit: 0` and runs `aegisctl manifest
import-dir` without `--keep-going`, so a malformed manifest fails the
hook loudly instead of leaving `chaos_points` in a mixed state. Set
`keepGoing: true` only when you're knowingly bulk-importing across
known-bad files and intend to clean up afterwards.

## 8. Validation — two surfaces

Per ADR-0010, every manifest gets validated twice:

**Offline (no cluster required)**

```bash
aegisctl manifest validate path/to/<service>.yaml
```

Runs against the bundled envelope schema. Catches: missing required
fields, unknown keys (`additionalProperties: false`), bad enums, bad
`apiVersion` / `kind`. Fast feedback for chart authors editing locally.

Pre-commit hook recipe:

```bash
for f in aegis-points/*.yaml; do
  aegisctl manifest validate "$f" || exit 1
done
```

**Online (server dry-run, full per-capability check)**

```bash
aegisctl manifest import path/to/<service>.yaml --dry-run
```

Hits `/v1beta/systems/{sys}/points/import?dry_run=true`. The server
compiles every referenced capability's `target_schema` and `param_schema`,
validates each point, runs the full DB transaction, then rolls back.
Returns the supersede impact so authors can preview catalog churn before
committing.

This is the only validation surface that catches capability-specific
errors (e.g. `target.namespace` missing on `pod_failure`). The CLI's
`--fetch-schema` flag pulls the same per-capability schemas via
`GET /v1beta/manifest-schema.json` so offline can match online if the
capability set drifts ahead of the bundled aegisctl release.

### L1-vs-L2 coverage gap for `target` / `param_overrides`

The bundled envelope schema does **not** set `additionalProperties:false`
on `target` or `param_overrides`. Those object positions have no static
per-capability `properties` to enumerate at envelope level — applying
`additionalProperties:false` with an empty `properties` set would reject
every real point (target needs `namespace`, `app`, etc. for most
capabilities). The server-side strict mode in
`schema_validate.go:cloneStrictObjects` injects
`additionalProperties:false` after expanding the per-capability schema,
so unknown keys are still rejected at import time — but they ship green
through `aegisctl manifest validate` (L1).

For parity with L2 strict mode, run `aegisctl manifest validate
--fetch-schema` (online, pulls the live per-capability schemas) or
`aegisctl manifest import --dry-run`. A follow-up may wire `--fetch-schema`
into L1 CI so the lint matches server-side rejection exactly.

## 9. CI gates

Two gates protect the catalog (see `.github/workflows/aegislab-manifest-lint.yml`):

### L1 — schema-lint every PointManifest on every PR

```bash
just lint-manifests
# walks aegislab/manifests/aegis-chaos/**/*.yaml and runs
# `aegisctl manifest validate` on each; exits non-zero on the first failure.
```

Same recipe works in any repo shipping PointManifests under
`aegis-points/`: replace the path argument.

### L2 — strict-mode regression on the server

Server-side coverage already lives in
[`aegislab/src/crud/chaos/schema_validate_test.go`](../../src/crud/chaos/schema_validate_test.go):

- `TestImportPoints_TargetSchemaViolation` — point with target missing a
  required field rejected with `ErrSchemaValidation` and leaf path
  `points[0].target.<key>`.
- `TestImportPoints_ParamOverridesSubsetRejectsUnknownKey` — unknown
  `param_overrides` key trips `additionalProperties:false` even when
  `required` is stripped.
- `TestSchemaCompiler_InjectsAdditionalPropertiesFalse` — seed schemas
  without explicit `additionalProperties:false` get it injected by the
  compiler, so loose seeds don't quietly accept unknown keys.

These tests are the hard contract: any change that loosens strict-mode
must update these and is presumed broken until the test author proves
otherwise.

### L2 — strict-mode regression on the CLI

The CLI bundled schema is regression-tested in
[`aegislab/src/cli/cmd/manifest_test.go`](../../src/cli/cmd/manifest_test.go):

- `TestManifestValidate_MissingCapability_ExitsNonZeroAndMentionsField` —
  missing required `capability` is caught offline.
- `TestManifestValidate_UnknownTopLevelKey_ExitsNonZeroAndMentionsField` —
  unknown key under `metadata` is rejected by `additionalProperties:false`.

## 10. What this contract does not cover

- **Selector probes** — whether a target's selector actually matches any
  pods. Server-side runtime concern, tracked in #457 (§3).
- **Generators**. This issue ships the contract; the generator(s) live
  outside aegislab. Any tool that emits a YAML satisfying §1–§5 is a
  conforming generator.
- **Migration of legacy chaos_points rows** lacking schemas. Out of scope
  per #463.
