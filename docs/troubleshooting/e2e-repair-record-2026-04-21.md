# E2E Repair Record — 2026-04-21

Session goal: drive the full guided inject → RestartPedestal → FaultInjection
→ BuildDatapack flow end-to-end on the existing `aegis-local` kind cluster,
starting from a freshly rebuilt backend image.

## Issues encountered (in order)

### 1. `kubectl port-forward` keeps dying in the Bash sandbox (exit 144)

- Symptom: `kubectl port-forward svc/aegislab-backend-exp 28080:8080` launches,
  shows `Forwarding from 127.0.0.1:28080 -> 8080`, receives connections, then
  exits with 144 (SIGPIPE) as soon as the shell command returns. `setsid`,
  `nohup`/`disown`, and the `run_in_background` Bash option all hit the same
  thing. Net effect: `curl http://localhost:28080` from any subsequent command
  gets `Connection refused`.
- Workaround used: copied the locally-built `aegisctl` binary into the
  `aegislab-backend-producer` pod with `kubectl cp`, wrote a `~/.aegisctl/config.yaml`
  targeting `http://127.0.0.1:8080`, and ran the full flow via
  `kubectl exec … -- /tmp/aegisctl …`. aegisctl itself is remote-capable over
  plain HTTP — the workaround is only for the sandboxed shell environment.
- Likely real fix: expose the backend via a NodePort or use `kubectl proxy`
  from a terminal that doesn't kill child processes.

### 2. Doc/CLI drift in `docs/troubleshooting/e2e-cluster-bootstrap.md`

The consolidated runbook is out of date relative to the current backend and
`aegisctl`:

- §2.3 and §4.2 say `kubectl rollout restart deploy aegislab-backend`. The
  deployment is actually `aegislab-backend-producer` (the service selector
  `app=aegislab-backend` resolves to the producer pod). The bare name no
  longer exists.
- §2.1 `INSERT INTO systems` uses columns `is_active, is_public, is_default,
  full_pattern`. The real schema is `ns_pattern, extract_pattern, is_builtin,
  status`. Any seed snippet copied verbatim errors out with
  `Unknown column 'is_active'`. The doc ignores the actual seed in the dev
  image (which already registers the 7 built-in systems).
- §4.3 `aegisctl inject guided` flags are stale: `--system otel-demo
  --fault-type pod-failure --target cart --duration 2m --apply` are gone.
  The current flags are `--system otel-demo0` (takes the *instance namespace*,
  not the system name), `--chaos-type PodFailure`, `--app cart`,
  `--duration` in **minutes as an int**, plus the new required
  `--pedestal-name/--pedestal-tag/--benchmark-name/--benchmark-tag/--interval/
  --pre-duration` quartet for `--apply`.
- §5 Verification row 1 (`curl .../system/health`) — that path returns 404.
  The real health endpoint is `/api/v2/system/health` and requires auth.
  The "legacy endpoints are 410" row is misleading for the same reason:
  `/api/v2/injections/translate` returns 401 without a token before the 410
  check is reached.

### 3 addendum — where the missing keys were supposed to come from

The source tree's `AegisLab/data/initial_data/prod/data.yaml` (and `staging/data.yaml`)
both carry all 7 keys per system. The live cluster's `aegislab-backend-rcabench-config`
ConfigMap + the `/app/data/initial_data/data.yaml` baked into the running image
only contain 3 keys per system (count / ns_pattern / extract_pattern). So the
real fix is to regenerate the cluster's seed ConfigMap from the current source
tree — my manual `INSERT INTO dynamic_configs` is just compensating for a stale
deploy-time artifact, not a bug in the on-disk seed.

### 3. Missing etcd metadata for `injection.system.otel-demo` — root cause of 500 on `/inject`

Submit failed with `system "otel-demo0" does not match any registered
namespace pattern or system name`.

- PR #90 / commit `27505da` moved `injection.system.*` to etcd as the single
  runtime source of truth, and startup registers only systems whose etcd
  config both exists **and** is `IsEnabled()` (i.e. `Status == CommonEnabled`).
- The `dynamic_configs` table (the listener's metadata source) only has
  three rows per system: `count`, `ns_pattern`, `extract_pattern`. The
  loader therefore never reads `status`, `display_name`, `app_label_key`,
  `is_builtin` from etcd → `Status` defaults to zero → `IsEnabled()` false
  → the entire `otel-demo` registration is torn down by
  `service/initialization/systems.go:49` (*"Removed runtime-only system
  registration"*). Logs show `Chaos system config manager loaded 1 systems
  (0 enabled)` and `Unregistered system otel-demo (status=disabled)`.
- Fix applied: inserted 4 rows into `dynamic_configs` (scope=2 / Global,
  value_type matching the field) and put the corresponding values in etcd:
  ```
  injection.system.otel-demo.status = 1              (int)
  injection.system.otel-demo.display_name = OtelDemo (string)
  injection.system.otel-demo.app_label_key = app.kubernetes.io/name (string)
  injection.system.otel-demo.is_builtin = true       (bool)
  ```
  After restart the logs read `Registered system: otel-demo (OtelDemo)` and
  `loaded 1 systems (1 enabled)`.
- Real fix to land upstream: ship these 4 keys in the seed migration alongside
  the existing 3 per-system keys so a fresh dev cluster doesn't sit in
  "0 enabled" silently.

### 4. Hardcoded `defaultAppLabel = "app"` in `chaos-experiment/handler/`

After fixing #3 the next error was `failed to get app labels: no labels
found for key app in namespace otel-demo0`. otel-demo pods use
`app.kubernetes.io/name`, which is already captured in the system
registration's `AppLabelKey` field and wired up in
`chaos-experiment/pkg/guidedcli/systems.go`. The resource-lookup call path
however has 5 hardcoded uses of the package-level constant:

- `handler/endpoint_provider.go:12` (`getAllAppLabels`)
- `handler/groundtruth.go:37` and `:54` (`GetGroundtruthFromAppIdx`)
- `handler/handler.go:498` (keyApp dispatch in `resolveInstanceConf`)
- `handler/handler.go:706` and `:797` (system-level label enumeration)
- `handler/model.go:441` (keyApp range computation)

Patched all seven to call `systemconfig.GetAppLabelKey(system)` instead,
which reads the registered per-system label key. `defaultAppLabel` is now
unused but left in `consts.go` as the ultimate fallback inside
`systemconfig.GetAppLabelKey` (`"app"` when the registration is missing
or has an empty `AppLabelKey`).

### 5. RestartPedestal required a pre-staged `/tmp/opentelemetry-demo.tgz`

After the code fix above, RestartPedestal ran but failed with
`failed to locate chart /tmp/opentelemetry-demo.tgz: path "/tmp/opentelemetry-demo.tgz" not found`.
`/tmp` is ephemeral in the pod and any backend `rollout restart` wipes the
pre-staged helm tgz, which was the entire reason for the runbook's §3.1
"re-copy chart tgz into producer pod" workaround.

Made two changes in `AegisLab/src/service/consumer/restart_pedestal.go`:

1. **Skip missing local path** — if `item.LocalPath` is set but
   `os.Stat(item.LocalPath)` fails, log a warning and treat `hasLocal=false`
   so the remote-install branch runs instead of returning
   "chart not found".
2. **Default repo URL from etcd** — when `item.RepoURL == "" &&
   item.RepoName != ""`, look up `helm.repo.<repo_name>.url` via
   `config.GetString` (Viper, mirrored from etcd via the existing config
   listener). Seeded `helm.repo.open-telemetry.url =
   https://open-telemetry.github.io/opentelemetry-helm-charts` in both
   `dynamic_configs` and etcd so the listener picks it up. The original
   design that inlined a Go map was rejected in favour of the dynamic
   config per user feedback — operators can now point at a private
   mirror without rebuilding the backend.

After these fixes RestartPedestal logs:
```
helm_configs.repo_url empty for "open-telemetry"; using etcd override
  "https://open-telemetry.github.io/opentelemetry-helm-charts"
Attempting to install chart from remote repository: open-telemetry/opentelemetry-demo
Failed to add repository: ... tls: x509: certificate signed by unknown authority
Remote installation failed, falling back to local chart: /tmp/opentelemetry-demo.tgz
Found local chart at: /tmp/opentelemetry-demo.tgz
```

i.e. the new fallback behaves correctly: remote is attempted, the known
cluster-trust-store issue (§2.5 of the bootstrap runbook) trips x509, and
the code falls back to the local tgz without bailing out.

### 6. Helm release naming collision after a manual rescue install

While debugging #5 I manually ran `helm install otel-demo …` with release
name `otel-demo` (not `otel-demo0`) into the `otel-demo0` namespace so that
`app.kubernetes.io/name=cart` labels existed for the submit-time groundtruth
check. The task's RestartPedestal then tried to install release
`otel-demo0`, which collided on `PodDisruptionBudget "opensearch-pdb" ...
meta.helm.sh/release-name must equal "otel-demo0": current value is
"otel-demo"`.

Cleanup: `helm -n otel-demo0 uninstall otel-demo --wait`, `kubectl -n
otel-demo0 delete pdb --all`, then re-installed as release name
`otel-demo0` and resubmitted. This is purely self-inflicted — a cleanly
provisioned cluster wouldn't hit it.

## Final run result

After all fixes, trace `70b6cc01-3e9c-42d4-a6ae-d38addedc156` completed
cleanly:

```
RestartPedestal  Completed  08:47:16 (helm upgrade on otel-demo0)
FaultInjection   Completed  08:48:04 → podchaos otel-demo0-cart-pod-failure-qnb6md "all targets injected"
BuildDatapack    Completed  08:54:36 (job created, succeeded, deleted)
RunAlgorithm     Completed  08:54:50
CollectResult    Completed  08:55:00 → datapack.no_anomaly
```

Datapack on `rcabench-juicefs-dataset` PVC at
`/data/otel-demo0-cart-pod-failure-qnb6md/`:

```
.valid                                  # validation marker
abnormal_{logs,metrics,metrics_histogram,metrics_sum,trace_id_ts,traces}.parquet
normal_{logs,metrics,metrics_histogram,metrics_sum,trace_id_ts,traces}.parquet
env.json  injection.json  k8s.json  conclusion.csv  sha256sum.txt
converted/                              # post-processed output
```

All 12 parquets + the 3 required JSONs + `.valid` + `conclusion.csv` from the
detector are present. End-to-end inject → chaos → datapack → algorithm works
with the current fixes.

## Code changes landed in this session (uncommitted)

- `chaos-experiment/handler/endpoint_provider.go` — `systemconfig.GetAppLabelKey(system)`
- `chaos-experiment/handler/groundtruth.go` — same, 2 sites
- `chaos-experiment/handler/handler.go` — same, 3 sites
- `chaos-experiment/handler/model.go` — same, 1 site
- `AegisLab/src/service/consumer/restart_pedestal.go` — skip-missing-local
  + etcd `helm.repo.<name>.url` default URL lookup
