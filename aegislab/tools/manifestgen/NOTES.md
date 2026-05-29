# manifestgen — notes for operators

Reads the vendored chaos-experiment data under
`tools/manifestgen/data/<sys>/{serviceendpoints,grpcoperations,databaseoperations,javaclassmethods}`
(parsed as text via go/parser AST — never imported) and writes Point
Manifest YAML under `aegislab/manifests/aegis-chaos/<system>/`.

Two manifest families per `(system, service)`:

- `<service>.yaml` — the workload-agnostic base.
- `<service>-<category>-A1b.yaml` — derived chaos for `http`, `dns`,
  `network`, `jvm-method`, `jvm-mysql`.

The data is vendored (copied) into `data/` so the tool is self-contained;
the external `chaos-experiment` module need not be present. Regenerate with
`just manifestgen` (default `-chaos-root=./data`). Output is deterministic —
re-runs produce byte-identical files (sorted points, sorted target keys,
constant `chart_version=seed-genesis`).

## What's emitted

### Base — `<service>.yaml`

| Capability            | Target shape                                      | Source                  |
|-----------------------|---------------------------------------------------|-------------------------|
| `pod_kill`            | `{namespace, app}`                                | every service           |
| `pod_failure`         | `{namespace, app}`                                | every service           |
| `container_kill`      | `{namespace, app, container}` *                   | every service           |
| `cpu_stress`          | `{namespace, app, container}` *                   | every service           |
| `memory_stress`       | `{namespace, app, container}` *                   | every service           |
| `time_skew`           | `{namespace, app, container}` *                   | every service           |
| `http_request_delay`  | `{namespace, app, port, method, path}`            | serviceendpoints + grpc |
| `http_request_abort`  | `{namespace, app, port, method, path}`            | serviceendpoints + grpc |
| `jvm_method_latency`  | `{namespace, app, class, method}`                 | javaclassmethods        |

\* `container` is the chaos-experiment service-data key (the deployment
name). `app` is the pod app-label. For teastore the trace data keys carry a
`teastore-` deployment prefix the pods don't (pods are labelled `webui`,
`auth`, …), so `app`/`source_app`/`target_service`/`metadata.service` use the
stripped form while `container` and the filename keep the full key. For the
other 7 systems the two coincide.

### A1b categories — `<service>-<category>-A1b.yaml`

| Category     | Capabilities                                                                                          | Target shape                                  | Source                       |
|--------------|-------------------------------------------------------------------------------------------------------|-----------------------------------------------|------------------------------|
| `http`       | `http_response_{abort,delay,replace_code,patch_body,replace_body}`, `http_request_{replace_method,replace_path}` | `{namespace, app, port, method, path}` | serviceendpoints (gRPC routes excluded) |
| `dns`        | `dns_error`, `dns_random`                                                                             | `{namespace, app, domain_patterns:[domain]}`  | serviceendpoints ServerAddress (gRPC-only pairs excluded) |
| `network`    | `network_{delay,loss,duplicate,corrupt,bandwidth,partition}`                                          | `{namespace, source_app, target_service}`     | serviceendpoints + grpc ServerAddress (forward, self-loops dropped) |
| `jvm-method` | `jvm_method_{return,exception}`, `jvm_{cpu_stress,memory_stress}`                                      | `{namespace, app, class, method}`             | javaclassmethods             |
| `jvm-mysql`  | `jvm_mysql_{latency,exception}`                                                                       | `{namespace, app, db_name, table, sql_type}`  | databaseoperations (mysql only) |

## Derivation notes

- **network** pairs are derived forward-only from each service's own
  endpoint `ServerAddress` values (self-loops dropped). Both HTTP
  serviceendpoints and gRPC operations contribute targets.
- **dns** domains are the `ServerAddress` of HTTP (non-gRPC-folded)
  endpoints, minus pairs that communicate only via gRPC (DNS chaos can't
  match those). The domain keeps the full target service name.
- **http A1b** excludes gRPC pseudo-routes (`/pkg.Service/Method`) and
  gRPC-folded operations — chaos-mesh HTTPChaos is HTTP/1.x only. The base
  `http_request_*` pair is still emitted for those so request-level chaos
  stays reachable.
- **jvm-mysql** `sql_type` is the lowercased operation; unrecognized values
  fall back to `all`. The target is keyed on the SQL operation
  (db_name/table/sql_type), not a Java class+method — the Connector/J
  interceptor matches statements. The `jvm_mysql_*` `target_schema` in
  `tools/capgen` was corrected to require `[namespace, app, db_name, table]`.

## HTTP path normalization

Before dedup, high-cardinality path segments are folded to `/*`:

- UUIDs (`/8-4-4-4-12` hex) → `/*`
- bare numeric segments (`/123`) → `/*`

This collapses per-request endpoints (e.g. `ts-user-service`'s ~900 UUID
delete paths) into a single `/api/v1/users/*` point, fixing the chaos-point
explosion (#500). The dedup key is `port|method|path`, so normalized
duplicates merge automatically.

## Test layers

- `TestAllManifestsValidate` — each generated YAML validates against the
  bundled `aegislab/src/cli/cmd/manifest_schema.json`.
- `TestTargetSchemasMatch` — each `(capability, target)` pair conforms to
  that capability's `target_schema` from
  `aegislab/tools/capgen/output/capabilities.json`.

Run with `go test ./...` from this directory (this is a standalone module;
the vendored `data/` packages compile but carry no tests).
