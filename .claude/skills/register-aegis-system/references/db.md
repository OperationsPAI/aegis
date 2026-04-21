# Layer 3 reference: database fixtures

Concrete schemas and seed recipes for `containers`, `container_versions`,
`helm_configs`. Methodology lives in `../SKILL.md` layer 3.

## Happy path (partial): `aegisctl pedestal helm set`

aegisctl covers *only* the `helm_configs` row. The two `containers`
rows + `container_versions` rows are still SQL. The pedestal-name
invariant is also caller responsibility — `aegisctl system register`
does not cross-check it against `containers.name`.

```bash
# after the pedestal containers + container_versions row is seeded:
aegisctl pedestal helm set \
  --container-version-id <id> \
  --chart-name <chart> --version 0.1.0 \
  --repo-name <repo>  --repo-url https://example.org/charts \
  --values-file /var/lib/rcabench/dataset/helm-values/<code>.yaml \
  --local-path /tmp/<chart>.tgz

aegisctl pedestal helm get    --container-version-id <id>
aegisctl pedestal helm verify --container-version-id <id>  # dry-run helm pull
```

For the containers/container_versions rows, see [Seed commands](#seed-commands)
below — SQL is still the only path.

## Contents

- [Happy path (partial): `aegisctl pedestal helm set`](#happy-path-partial-aegisctl-pedestal-helm-set)
- [containers](#containers)
- [container_versions](#container_versions)
- [helm_configs](#helm_configs)
- [Pedestal name = system code constraint](#pedestal-name--system-code-constraint)
- [Benchmark vs pedestal type trap](#benchmark-vs-pedestal-type-trap)
- [Seed commands](#seed-commands)

## containers

Holds two distinct row types distinguished by `type`:

- `type=1` — benchmark (datapack builder image)
- `type=2` — pedestal (the workload itself / its helm chart)

Columns of note:

- `name` — unique per type. For pedestals, **must equal the short system
  code** from layer 1 (see below).
- `active_name` — VIRTUAL column. Never include it in INSERT.

## container_versions

Links a `containers` row to a registry/namespace/repository/tag plus a
runtime `command`. Required fields:

- `container_id`
- `registry`, `namespace`, `repository`, `tag`
- `command` — non-empty. Empty produces
  `runc exec: "": executable file not found`.
- `env_vars` — optional; used for per-benchmark overrides like
  `RCABENCH_OPTIONAL_EMPTY_PARQUETS`.

## helm_configs

Per-pedestal chart info. Required:

- `chart_name`, `version`, `repo_name`
- `value_file` — in-pod path to the values YAML
- `repo_url` — may be empty; if empty, falls back to etcd
  `helm.repo.<repo_name>.url` (see `chart.md`)
- `local_path` — optional pre-staged tgz (e.g. `/tmp/<chart>.tgz`)

`aegisctl pedestal helm set` upserts this row given a
`container-version-id`; prefer it over a direct INSERT.

## Pedestal name = system code constraint

**Still a caller responsibility — aegisctl does not enforce it.**

The submit validator checks `pedestal.name == system_type` where
`system_type` is the short Go constant from
`chaos-experiment/internal/systemconfig/systemconfig.go` (`ts`, `ob`,
`hs`, `sn`, `media`, `tea`, …) — *not* the display-facing name.

If you seed a pedestal row as `hotelreservation` while the registry uses
`hs`, submit returns `mismatched system type hs for pedestal hotelreservation`.

Rules:

- `containers.name` for the pedestal row = short system code.
- data.yaml `name:` = short system code (`aegisctl system register
  --name` uses this exact value).
- `DisplayName` (compiled constant) or the
  `injection.system.<code>.display_name` etcd key = user-facing string.

Check `SystemType` constants in `systemconfig.go` first; never invent a
code.

This is what the hotelreservation integration hit: `RestartPedestal`
queries `containers` by the short code, so a row named `hotelreservation`
is unfindable when the registry says `hs`.

## Benchmark vs pedestal type trap

`CheckContainerExistsWithDifferentType` rejects reusing the same name
across types with the confusing message
`container exists but has type 'pedestal', not 'benchmark'` (or vice
versa). Benchmark and pedestal rows must have **distinct names**, e.g.:

- pedestal: `otel-demo`
- benchmark: `otel-demo-bench`

## Seed commands

Minimal seed (adjust values). Short code `<code>`, upstream chart
`<chart>`, registry image for the datapack `<img>:<tag>`:

```sql
-- pedestal (name MUST == short code)
INSERT INTO containers (name, type) VALUES ('<code>', 2);
SET @pedestal_id = LAST_INSERT_ID();

INSERT INTO container_versions (container_id, registry, namespace,
                                repository, tag, command)
  VALUES (@pedestal_id, 'docker.io', 'opspai',
          '<pedestal-repo>', '<tag>', '<entrypoint>');

-- benchmark (datapack builder)
INSERT INTO containers (name, type) VALUES ('<code>-bench', 1);
INSERT INTO container_versions (container_id, registry, namespace,
                                repository, tag, command)
  VALUES (LAST_INSERT_ID(), 'docker.io', 'opspai',
          'clickhouse_dataset', 'e2e-kind-20260421',
          'python -m rcabench_platform.v3.sdk.datasets.rcabench build');
```

Then attach the helm_configs row via aegisctl:

```bash
aegisctl pedestal helm set --container-version-id <pedestal-cv-id> \
  --chart-name <chart> --version 0.1.0 --repo-name <repo> \
  --repo-url ''  --values-file /var/lib/rcabench/dataset/helm-values/<code>.yaml \
  --local-path /tmp/<chart>.tgz
```

Finally populate etcd/dynamic_configs via `aegisctl system register`
(see `etcd.md`).
