# Layer 2 reference: etcd runtime config

Everything needed to get `injection.system.<name>.*` right. Methodology
for *why* etcd is authoritative lives in `../SKILL.md` layer 2.

## Happy path: `aegisctl system register`

One command writes all seven etcd keys + seven `dynamic_configs` rows
atomically (via POST /api/v2/systems), in the correct scope, with
correct value_types:

```bash
# from a full data.yaml file
aegisctl system register --from-seed AegisLab/data/initial_data/prod/data.yaml \
                         --name <code>

# or point at the initial_data root and resolve via --env
aegisctl system register --from-seed AegisLab/data/initial_data \
                         --env prod --name <code>

# replace an existing registration
aegisctl system register --from-seed ... --name <code> --force
```

Inspect / remove:

```bash
aegisctl system list
aegisctl system unregister --name <code>            # prompts; add --yes to skip
```

`list` prints enabled/disabled + `is_builtin` as round-tripped from the
backend (POST /api/v2/systems now preserves `is_builtin`). If a system
is absent from `list` or shows `enabled=false`, that is the reason the
backend logs `loaded N systems (0 enabled)`. Re-run `register`.

## Contents

- [Happy path: `aegisctl system register`](#happy-path-aegisctl-system-register)
- [Seven-key layout](#seven-key-layout)
- [dynamic_configs row requirements](#dynamic_configs-row-requirements)
- [value_type enum](#value_type-enum)
- [What still goes wrong even with aegisctl](#what-still-goes-wrong-even-with-aegisctl)
- [Fallback: raw etcdctl + SQL (aegisctl unavailable)](#fallback-raw-etcdctl--sql-aegisctl-unavailable)

## Seven-key layout

Since PR #90, etcd is the single source of truth for which systems are
enabled at runtime. Every system must ship all seven keys:

```
injection.system.<name>.count
injection.system.<name>.ns_pattern
injection.system.<name>.extract_pattern
injection.system.<name>.display_name
injection.system.<name>.app_label_key
injection.system.<name>.is_builtin
injection.system.<name>.status            # must == 1 (CommonEnabled)
```

On boot, `InitializeSystems` iterates this config, unregisters every
system not in the enabled set, then re-registers each enabled one. The
compiled-in registry (layer 1) is a *template*, overridden by etcd.

If `Status` reads zero or any of the above keys are absent, `IsEnabled()`
returns false, `InitializeSystems` removes the runtime registration
(`Removed runtime-only system registration: <name>`), and the backend
logs `loaded N systems (0 enabled)`. Submit then fails with
`system "<ns>" does not match any registered namespace pattern`.

## dynamic_configs row requirements

The config listener only loads keys that have a row in the
`dynamic_configs` table — putting a value in etcd alone does nothing.
Each key needs both:

1. A row in `dynamic_configs` with `scope=2` (Global) and a `value_type`
   matching the Go field's kind.
2. The etcd value at the correct scoped path.

`aegisctl system register` writes both sides in one transaction.

## value_type enum

| value_type | Go kind |
|------------|---------|
| 0 | string |
| 1 | bool |
| 2 | int |
| 3 | float |

For the seven injection.system.* keys:

- `count` → int (2)
- `ns_pattern`, `extract_pattern`, `display_name`, `app_label_key` → string (0)
- `is_builtin` → bool (1)
- `status` → int (2)

## What still goes wrong even with aegisctl

- **Stale backend:** if a prior boot cached an old view, restart the
  backend after `register`. The listener re-reads on reconnect, but a
  fresh cluster's first "register" happens *after* backend start and
  some handlers lazy-load.
- **Wrong env:** `aegisctl system register --from-seed initial_data/
  --env staging` against a cluster seeded from `prod/` silently
  registers different default values.
- **Not in seed:** if a system isn't in data.yaml at all, `register`
  has nothing to read. Add the entry to `AegisLab/data/initial_data/
  {prod,staging}/data.yaml` first.
- **Layer 3 still required:** enabling via etcd only surfaces the
  system in the guided flow. Without `containers` + `container_versions`
  + `helm_configs` rows (see `db.md`), submit still fails downstream.

## Fallback: raw etcdctl + SQL (aegisctl unavailable)

Use this only when you can't run aegisctl (e.g., backend API is down).
Every trap below is what `aegisctl system register` hides.

### Scope-prefix trap

The listener reads from `/rcabench/config/<scope>/` where `<scope>` is
one of `global`, `consumer`, `producer`. Writing at the etcd root
produces `key not found: /rcabench/config/global/<key>` and the runtime
registration is removed a moment later. Always prefix
`/rcabench/config/global/`.

### Ordering: DB row before etcd put

`config_listener` validates every etcd change against `dynamic_configs`.
If the key has no DB row, the put is logged but rejected:

```
failed to retrieve existing config <key> from database: record not found
```

Correct order: INSERT into `dynamic_configs` (scope=2, correct
value_type) → `etcdctl put` → restart backend or re-submit.

### data.yaml → etcd one-way gap

The producer's fresh-seed path `initializeDynamicConfigs` writes the
`dynamic_configs` rows but does **not** publish their `default_value`s
to etcd. On a fresh cluster, DB and YAML look right but etcd is empty
and first boot reports `0 enabled`. This is the gap `aegisctl system
register` closes.

### Three-vs-seven-key trap

Older seeds shipped only `count` + `ns_pattern` + `extract_pattern`.
The registry now requires all seven. `0 enabled` on a partially-seeded
cluster is almost always the four new keys missing.

### Raw commands

```bash
NS=<name>
# Seed dynamic_configs rows first (SQL), then:
etcdctl put /rcabench/config/global/injection.system.$NS.count 1
etcdctl put /rcabench/config/global/injection.system.$NS.ns_pattern "$NS[0-9]+"
etcdctl put /rcabench/config/global/injection.system.$NS.extract_pattern "$NS([0-9]+)"
etcdctl put /rcabench/config/global/injection.system.$NS.display_name "<Display>"
etcdctl put /rcabench/config/global/injection.system.$NS.app_label_key app
etcdctl put /rcabench/config/global/injection.system.$NS.is_builtin true
etcdctl put /rcabench/config/global/injection.system.$NS.status 1

etcdctl get --prefix /rcabench/config/global/injection.system.$NS.
```

Then restart the backend and confirm startup log shows the system in
the enabled count.
