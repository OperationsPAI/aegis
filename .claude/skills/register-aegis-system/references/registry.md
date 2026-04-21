# Layer 1 reference: compiled systemconfig registry

What has to be compiled in vs what can be runtime-only via etcd.
Methodology lives in `../SKILL.md` layer 1.

## What has to be compiled in

`chaos-experiment/internal/systemconfig/builtinSystemRegistrations()`
holds the *shape* of every supported system as Go constants:

- `NsPattern` — regex, e.g. `ts[0-9]+`
- `DisplayName` — user-facing string
- `AppLabelKey` — typically `app`
- `SystemType` constant — the short code (`ts`, `ob`, `hs`, `sn`,
  `media`, `tea`, …). Everything else (DB rows, etcd keys, chart name)
  must reference this exact string.

`pkg/guidedcli` reads this via `GetAllSystemTypes` + `GetRegistration`
to list systems, validate `--system <code>`, and resolve the instance
namespace.

Skip this and the guided flow can't list the system; `--system foo0`
is rejected before any DB or etcd lookup.

## Per-system metadata directory

Each system also needs `internal/<system>/` with service metadata
(endpoints, db ops, jvm methods, …) for the handler layer to do
"intelligent chaos generation". Without it:

- Chaos still injects for **selector + pod-failure** (coarse targeting
  works fine).
- Richer chaos types that need metadata — HTTP latency on a specific
  route, JVM latency on a specific class — have nothing to target.

## What can stay runtime-only via etcd

Everything that `builtinSystemRegistrations()` sets as a constant is
*also* exposed as an `injection.system.<code>.<field>` etcd key and
**etcd wins at runtime**. `InitializeSystems` unregisters each compiled
entry, then re-registers from etcd on boot.

Practical consequence: you can ship a system whose `NsPattern`,
`DisplayName`, or `AppLabelKey` differ from the compiled constant by
overriding the etcd keys. But:

- `SystemType` (the short code) is **not** overridable — it's the
  identity tying DB rows, etcd keys, the chart `chart_name`, and the
  namespace pattern together.
- A system missing from the compiled registry but present in etcd is
  treated as `is_builtin=false` and can still work, but loses the
  per-system metadata directory (see above) since there's no Go package
  to put it in.

## When to add a new entry vs rely on etcd alone

- **Add to Go** when: you need service-metadata-driven chaos (JVM,
  HTTP-route, DB-op targeting), or the system will be a first-class
  supported benchmark.
- **Etcd-only** when: prototyping, one-off onboarding, or the system
  will only use coarse chaos (pod-failure, network selectors).

Either way the seven etcd keys + the three DB fixture rows are
mandatory. Only the compiled registry entry is optional.
