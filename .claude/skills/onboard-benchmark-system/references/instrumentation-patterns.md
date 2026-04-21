# Instrumentation patterns — phase 3

OTEL instrumentation has no single convention. Every demo invents its
own gate and its own env-var naming. The fastest way to figure out a
new workload is to **read one service's main.go / app.py, don't guess**.

## Known patterns

### Pattern A: standard OTEL SDK env vars
- Trigger: `OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4317`
- Optional: `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_PROTOCOL`
- Workloads: opentelemetry-demo (astronomy-shop), anything built with
  modern auto-instrumentation SDKs.
- Why it works: the SDK reads these automatically at init time.

### Pattern B: demo-custom gate + custom endpoint var
- Trigger: two vars, both required. Missing either → startup panic or
  silent no-op.
- Example (Online Boutique v0.10.x Go services):
  - `ENABLE_TRACING=1` — turns init on
  - `COLLECTOR_SERVICE_ADDR=otel-collector.otel:4317` — target
  - Panic without `COLLECTOR_SERVICE_ADDR`:
    `panic: environment variable "COLLECTOR_SERVICE_ADDR" not set`
- Node.js services (same demo) emit traces under pattern A alone, so
  mixed-language demos show partial coverage until pattern B is applied.

### Pattern C: SDK autoinstrumentation via operator
- Trigger: opentelemetry-operator installed; pods annotated with
  `instrumentation.opentelemetry.io/inject-<lang>`.
- Workloads: some kube-native demos ship this.
- Heavier to set up; skip unless the existing OTEL collector is already
  wired through the operator.

### Pattern D: bundled collector
- The demo ships its own OTEL collector that you're supposed to point
  at *your* ClickHouse. Edit the demo's collector config.

## Picking the right pattern (decision flow)

1. Does the demo's README mention OTEL? If yes, trust it — follow that.
2. If no, grep one service's source for `otel`, `tracer`, `exporter`.
   - Found `NewOTLPExporterWithEndpoint(os.Getenv("..."))` → pattern A.
   - Found `initTracing()` with `mustMapEnv("SOMETHING_ADDR")` →
     pattern B. Extract the env-var name from the source.
3. If the service imports a specific bundled collector manifest in
   kustomize / helm → pattern D.
4. If no OTEL references at all → the workload is not instrumented.
   Either pick a different demo or add instrumentation. Don't try to
   auto-instrument Go services via the operator — support is immature.

## Confirming instrumentation works

After setting env vars and rolling out, check from the ClickHouse side
rather than the pod side — pod stdout rarely tells you whether spans
reach the collector:

```bash
kubectl -n otel exec clickhouse-0 -- clickhouse-client --password clickhouse \
  -q 'SELECT ServiceName, count() c FROM otel.otel_traces GROUP BY ServiceName ORDER BY c DESC'
```

If only some services appear, the unseen ones are on a different
pattern (mixed-language demos are the common case).

## Traps

- Setting `OTEL_EXPORTER_OTLP_ENDPOINT=http://...` when the demo's code
  expects just `host:port` for gRPC. Some SDKs parse the URL, others
  pass it straight to the grpc dialer which then fails with `invalid
  grpc target`. Try without the scheme first.
- `OTEL_EXPORTER_OTLP_INSECURE=true` is needed when using plain gRPC
  on port 4317 — otherwise the SDK assumes TLS and connection fails
  silently.
- Distroless container images have no shell. `kubectl exec <pod> --
  env` returns empty. Check env via
  `kubectl get pod <pod> -o jsonpath='{.spec.containers[0].env}'` instead.
