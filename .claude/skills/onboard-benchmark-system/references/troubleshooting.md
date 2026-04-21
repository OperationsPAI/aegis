# Troubleshooting — the four patterns that cover ~90% of failures

## 1. Chaos is `Applied` but nothing changes

Check chaos-daemon logs, not the CR describe output. The CR may report
"success" one second and "failed" the next because of reconcile
retries.

```bash
kubectl -n chaos-mesh logs -l app.kubernetes.io/component=chaos-daemon \
  --tail=200 | grep -iE "flush|error|pid" | tail -20
```

- `expected docker:// but got container` → daemon runtime mismatch.
  `helm upgrade chaos-mesh ... --reuse-values --set
  chaosDaemon.runtime=containerd --set
  chaosDaemon.socketPath=/run/containerd/containerd.sock`.
- `unable to flush ip sets ... permission denied` → kernel module
  `ip_set` missing on the host. `sudo modprobe ip_set` (or
  `nsenter -t 1 -m -- modprobe ip_set` inside a privileged container).
- `pod selector matched 0 pods` → your labelSelector is wrong. Confirm
  with `kubectl -n <ns> get pod -l <selector>`.

## 2. Traces aren't reaching ClickHouse

```bash
kubectl -n otel logs deploy/otel-collector --tail=100 | \
  grep -iE "error|accept|reject|retry"
```

- `Authentication failed: password is incorrect` → collector config
  password ≠ ClickHouse env-var password. Align and restart collector.
- No errors, but `SELECT count() FROM otel_traces` is `0` → the
  workload isn't actually emitting. See `instrumentation-patterns.md`.
  A common trap is setting OTEL vars on the wrong container in a
  multi-container pod.
- Schema missing (`table doesn't exist`) → exporter couldn't create
  schema. Check exporter config has `create_schema: true` and the
  connection actually succeeded on collector startup.

## 3. Workload crashes with `env var ... not set`

The demo has a custom instrumentation gate. Read one service's main
file, extract every env var the init path calls `mustMapEnv` /
`os.Getenv(...)` with, and set them all.

Example: Online Boutique v0.10.2 frontend panics with:
```
panic: environment variable "COLLECTOR_SERVICE_ADDR" not set
```
Fix: `kubectl set env deploy/frontend COLLECTOR_SERVICE_ADDR=... ENABLE_TRACING=1`.

## 4. `aegisctl chaos …` returns HTTP 500

```
Warning: batch[0][0]: unknown namespace "demo", using 0
Error: API error 500: An unexpected error occurred
```
The backend's pedestal registry doesn't know your namespace. Either:
(a) register the pedestal through the backend's project API, or
(b) fall back to `chaos-experiment` CLI or raw CRD apply. For a
one-off smoke test, always prefer (b).

## Escalation ladder

1. **Chaos CR status** (fastest signal — gate condition) — 5s
2. **Chaos-daemon log** on the node hosting the target pod — 15s
3. **Collector log** if the issue is about traces, not chaos — 15s
4. **ClickHouse trace count** — whether the pipeline is flowing at all
5. **Backend log** (only for path 1) — rare, usually skip
6. **Ask the user** — only after all of the above, and always batch
   multiple questions into one message.
