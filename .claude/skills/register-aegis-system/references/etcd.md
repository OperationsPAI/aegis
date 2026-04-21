# Layer 2 reference: etcd runtime config

Concrete commands and traps for `injection.system.<name>.*` keys. The
methodology for *why* etcd is authoritative lives in `../SKILL.md` layer 2.

## Contents

- [Seven-key layout](#seven-key-layout)
- [dynamic_configs row requirements](#dynamic_configs-row-requirements)
- [value_type enum](#value_type-enum)
- [Etcd key path (scope prefix) trap](#etcd-key-path-scope-prefix-trap)
- [Three-vs-seven-key trap](#three-vs-seven-key-trap)
- [Ordering: DB row before etcd put](#ordering-db-row-before-etcd-put)
- [data.yaml to etcd is one-way and incomplete](#datayaml-to-etcd-is-one-way-and-incomplete)
- [Reconcile commands](#reconcile-commands)

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

If `Status` reads as zero or any of the above keys are absent, `IsEnabled()`
returns false, `InitializeSystems` removes the runtime registration
(`Removed runtime-only system registration: <name>`), and you see
`loaded N systems (0 enabled)` in logs. Submit then fails with
`system "<ns>" does not match any registered namespace pattern`.

## dynamic_configs row requirements

The config listener only loads keys that have a row in the
`dynamic_configs` table — putting a value in etcd alone does nothing.
Each key needs both:

1. A row in `dynamic_configs` with `scope=2` (Global) and a `value_type`
   matching the Go field's kind.
2. The etcd value at the correct scoped path.

`AegisLab/data/initial_data/prod/data.yaml` and `staging/data.yaml` ship
all seven keys. Older seeds (and stale `aegislab-backend-rcabench-config`
ConfigMaps) often ship only three (`count` + `ns_pattern` +
`extract_pattern`). Check both before blaming code.

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

## Etcd key path (scope prefix) trap

The listener reads from `/rcabench/config/<scope>/` where `<scope>` is
one of `global`, `consumer`, `producer`. Writing at the etcd root
produces `key not found: /rcabench/config/global/<key>` and the runtime
registration is removed a moment later. Always prefix:

```bash
etcdctl put /rcabench/config/global/injection.system.<name>.status 1
```

## Three-vs-seven-key trap

Older seeds used a three-key layout (`count` + `ns_pattern` +
`extract_pattern`). The registry now requires seven. If a fresh backend
boot reports `0 enabled` even though etcd has some keys, grep for the
four extra ones (`display_name`, `app_label_key`, `is_builtin`, `status`)
before anything else.

## Ordering: DB row before etcd put

`config_listener` validates every etcd change against `dynamic_configs`.
If the key has no DB row, the put is logged but rejected:

```
failed to retrieve existing config <key> from database: record not found
```

`etcdctl get` will still show the value, but the runtime registry never
picks it up; symptom is `loaded N systems (0 enabled)` persisting.

Correct reconcile order:

1. INSERT into `dynamic_configs` (with scope=2, correct value_type).
2. `etcdctl put /rcabench/config/global/<key> <value>`.
3. Restart backend or re-submit.

Not the reverse.

## data.yaml to etcd is one-way and incomplete

The producer's fresh-seed path `initializeDynamicConfigs` writes the
`dynamic_configs` rows but does **not** publish their `default_value`s
to etcd. Only the legacy `systems`-table migration publishes — and that
path is a no-op for data.yaml-sourced systems.

Symptom: DB and YAML look right, first backend boot still reports
`loaded N systems (0 enabled)` because etcd is empty.

Until this is closed in code, every new data.yaml-seeded system needs a
one-time `etcdctl put` pass after the first boot. Treat a fresh cluster
as "DB-seeded, etcd-empty" and reconcile explicitly.

## Reconcile commands

Write all seven keys for system `<name>` (adjust values per system):

```bash
NS=<name>
etcdctl put /rcabench/config/global/injection.system.$NS.count 1
etcdctl put /rcabench/config/global/injection.system.$NS.ns_pattern "$NS[0-9]+"
etcdctl put /rcabench/config/global/injection.system.$NS.extract_pattern "$NS([0-9]+)"
etcdctl put /rcabench/config/global/injection.system.$NS.display_name "<Display>"
etcdctl put /rcabench/config/global/injection.system.$NS.app_label_key app
etcdctl put /rcabench/config/global/injection.system.$NS.is_builtin true
etcdctl put /rcabench/config/global/injection.system.$NS.status 1
```

Verify:

```bash
etcdctl get --prefix /rcabench/config/global/injection.system.$NS.
```

Then restart the backend and confirm startup log shows the system in
the enabled count.
