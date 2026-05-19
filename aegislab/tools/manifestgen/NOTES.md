# manifestgen — notes for operators

Reads `chaos-experiment/internal/<sys>/{serviceendpoints,grpcoperations,databaseoperations,javaclassmethods}`
and writes one Point Manifest YAML per `(system, service)` under
`aegislab/manifests/aegis-chaos/<system>/<service>.yaml`.

Regenerate with `just manifestgen`. Output is deterministic — re-runs
produce byte-identical files (sorted points, sorted target keys,
constant `chart_version=seed-genesis`).

## What's emitted (per service)

| Capability            | Target shape                                      | Source                  | Per-service count |
|-----------------------|---------------------------------------------------|-------------------------|-------------------|
| `pod_kill`            | `{namespace, app}`                                | (every service)         | 1                 |
| `pod_failure`         | `{namespace, app}`                                | (every service)         | 1                 |
| `container_kill`      | `{namespace, app, container=service}` *           | (every service)         | 1                 |
| `cpu_stress`          | `{namespace, app, container=service}` *           | (every service)         | 1                 |
| `memory_stress`       | `{namespace, app, container=service}` *           | (every service)         | 1                 |
| `time_skew`           | `{namespace, app, container=service}` *           | (every service)         | 1                 |
| `http_request_delay`  | `{namespace, app, port, method, path}`            | serviceendpoints + grpc | N per endpoint    |
| `http_request_abort`  | `{namespace, app, port, method, path}`            | serviceendpoints + grpc | N per endpoint    |
| `jvm_method_latency`  | `{namespace, app, class, method}`                 | javaclassmethods        | N per class.method|

\* `container=service` is a deliberate policy default — see `main.go`
`buildPoints` WHY comment. The 8 benchmark charts all follow the k8s
convention `deployment.name == pod.label.app == container[0].name`. If
a benchmark deviates, the rendered chaos-mesh CR will no-op at runtime
against a non-existent container — that's the correct loud failure
mode; we don't silently fudge at manifest time.

gRPC operations are folded into the HTTP family with `method=POST` and
`path=/RPCService/RPCMethod`. HTTP entries lacking any of
`RequestMethod`, `Route`, or a valid integer `ServerPort` are dropped
silently — emitting them would fail the seeded `target_schema`
(`port` is required to be an integer 1..65535, `method` an enum, `path`
non-empty).

## What's intentionally skipped

| Capability             | Reason for skip                                                          |
|------------------------|--------------------------------------------------------------------------|
| `network_delay`        | Seed target_schema requires `{source_app, target_service}`; chaos-experiment data carries no curated peer. Would need to walk `networkdependencies` and pick a destination — that's a separate policy call. |
| `network_loss`         | Same as `network_delay`.                                                 |
| `network_partition`    | Same as `network_delay`.                                                 |
| `network_duplicate`    | Same as `network_delay` (also not in the workload-agnostic baseline).    |
| `network_corrupt`      | Same as `network_delay`.                                                 |
| `network_bandwidth`    | Same as `network_delay`.                                                 |
| `dns_error`            | Seed target_schema requires `domain_patterns` (array, minItems=1); per-service domain data is not in chaos-experiment. |
| `dns_random`           | Same as `dns_error`.                                                     |
| `jvm_mysql_latency`    | Seed target_schema requires `class` + `method` in addition to `db_name/table/sql_type`; `databaseoperations` data has no Java class/method. |
| `jvm_mysql_exception`  | Same as `jvm_mysql_latency`.                                             |
| `jvm_method_exception` | Symmetric with `jvm_method_latency` but adds no new coverage at seed time; can be reintroduced if needed. (Schema would validate — explicit policy choice to keep the seed concise per §11 step 3.) |
| `jvm_method_return`    | Same rationale as `jvm_method_exception`.                                |
| `jvm_gc`, `jvm_cpu_stress`, `jvm_memory_stress` | Same rationale: seed kept concise; cluster-level cpu_stress / memory_stress already cover the JVM container. |
| `http_response_*`, `http_request_replace_*` | Same rationale: seed keeps request_delay + request_abort as the canonical pair; the other 7 HTTPChaos capabilities are reachable via ad-hoc `:import replace_scope=none`. |

If the gap on a skipped capability is real (network, dns, jvm_mysql),
fix it in `chaos-experiment` data first — don't widen manifestgen to
fabricate target fields.

## Test layers

- `TestAllManifestsValidate` — each generated YAML validates against the
  bundled `aegislab/src/cli/cmd/manifest_schema.json` (same schema
  `aegisctl manifest validate` uses).
- `TestTargetSchemasMatch` — each `(capability, target)` pair conforms
  to that capability's `target_schema` from
  `aegislab/tools/capgen/output/capabilities.json`.

Run with `go test ./tools/manifestgen/...`.
