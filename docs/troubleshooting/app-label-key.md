# Per-System Pod Selector Label Key (`AppLabelKey`)

## Problem

Chaos Mesh pod selectors match on a label key/value pair. Historically every
`chaos/*_chaos.go` builder hardcoded `"app": <appName>`, which works for
TrainTicket (`app=ts-user-service`) but silently matches zero pods on systems
that use a different convention — notably OpenTelemetry Demo, whose Helm chart
labels pods as `app.kubernetes.io/name=<component>` with no plain `app` label
at all.

Result before this fix: every injection on `otel-demo*` namespaces succeeded
as a Chaos Mesh CRD, but selected zero targets; the pedestal cycle completed
with green status while touching nothing, producing empty parquets downstream.

## Design

A new field on `SystemRegistration`:

```go
type SystemRegistration struct {
    Name        SystemType
    NsPattern   string
    DisplayName string
    AppLabelKey string // "" defaults to "app"
}
```

Helpers in `internal/systemconfig/systemconfig.go`:

- `GetAppLabelKey(system SystemType) string` — lookup by explicit system
- `GetCurrentAppLabelKey() string` — lookup via the thread-local current system

Both default to `"app"` so unregistered or legacy systems behave unchanged.

## Consumer Contract

`handler.BatchCreate` (and any direct caller of `chaos/*` spec builders) MUST
invoke `systemconfig.SetCurrentSystem(...)` before generating specs. The
builder reads the key at build time, not at apply time, so swapping the
current system between builds is safe but swapping between build and apply
would be a race. The guided pipeline (`pkg/guidedcli`) sets the current system
during `BuildInjection` and the backend consumer preserves that ordering.

## Built-in Values

See `internal/systemconfig/systemconfig.go:60-71`:

| System       | `AppLabelKey`                 |
|--------------|-------------------------------|
| `ts`         | `app`                         |
| `otel-demo`  | `app.kubernetes.io/name`      |
| `media`      | `app`                         |
| `hs`         | `app`                         |
| `sn`         | `app`                         |
| `ob`         | `app`                         |
| `sockshop`   | `app`                         |
| `teastore`   | `app`                         |

## Adding a New System With a Custom Key

```go
systemconfig.RegisterSystem(systemconfig.SystemRegistration{
    Name:        "mysystem",
    NsPattern:   `^mysystem\d+$`,
    DisplayName: "MySystem",
    AppLabelKey: "app.kubernetes.io/instance", // whatever your chart emits
})
```

## File Pointers

- `internal/systemconfig/systemconfig.go:40-86` — field + helpers
- `internal/systemconfig/systemconfig.go:60-71` — built-in registrations
- `chaos/pod_chaos.go:54` — representative builder usage
  (same pattern in all 13 files under `chaos/`)
- `pkg/guidedcli/k8s.go:29` — guided-side pod enumeration respects the key
- `pkg/guidedcli/k8s.go:56` — container discovery respects the key

## Verification

On a system whose pods only expose `app.kubernetes.io/name` (no `app` label),
a successful injection will log `all targets injected` in the consumer. If the
key were wrong, the Chaos Mesh CRD would still appear `Ready=True` but with
zero pods matched — check consumer logs, not CRD status. Note that Chaos Mesh
auto-deletes succeeded PodChaos after `duration+grace`, so a stale
`kubectl get podchaos` listing may be empty even for correct runs.
