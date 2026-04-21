# Worked example — onboarding Online Boutique end-to-end

This is the full transcript from the session that produced
`docs/deployment/09-smoke-run.md`. It's condensed but every step is a
real command that ran. Use it as a template, not a to-do list — your
system will differ.

## Preflight (phase 1)

```bash
kubectl --context kind-aegis-local get nodes       # 3 Ready
kubectl -n chaos-mesh get pods                     # 7 Running
kubectl -n otel get pods                           # (not installed yet — flagged for phase 3)
```

Chaos-mesh was already up from issue #7, but with
`chaosDaemon.runtime=docker`. Not discovered until phase 6. Lesson:
don't trust a "healthy" chaos-mesh until you've smoke-tested a
NetworkChaos against any pod.

## Deploy (phase 2)

```bash
kubectl create namespace demo
kubectl -n demo apply -f \
  https://raw.githubusercontent.com/GoogleCloudPlatform/microservices-demo/v0.10.2/release/kubernetes-manifests.yaml
kubectl -n demo wait --for=condition=available --timeout=300s deploy --all
```

Validated with port-forward + curl. Hit the host port collision with
flutter — had to use `28080` instead of `18080`.

## Instrument (phase 3)

First attempt — standard OTEL env vars. **Did not work** — Online
Boutique's Go services don't read them, only Node.js services did. Only
currencyservice and paymentservice (both Node.js) showed up.

Second attempt — added `ENABLE_TRACING=1`. Go services panicked on
startup because `COLLECTOR_SERVICE_ADDR` wasn't set.

Third attempt — final recipe:
```bash
for d in $(kubectl -n demo get deploy -o name); do
  kubectl -n demo set env $d \
    ENABLE_TRACING=1 \
    COLLECTOR_SERVICE_ADDR=otel-collector.otel:4317
done
```
Then 6 services showed spans in ClickHouse.

Before this worked, the observability pipeline itself had to come up —
see `references/prerequisites.md` and
`docs/deployment/otel-pipeline.yaml`. The collector first crashed on
ClickHouse auth because `CLICKHOUSE_PASSWORD=""` doesn't create a
usable default user in CH 24.x.

## Pick injection path (phase 4)

Historical note: the original session fell back to raw NetworkChaos
because registering a pedestal used to be a multi-hour etcd+SQL+
`kubectl cp` ritual. That's no longer the case. With current aegisctl:

```bash
# register demo as a system (writes etcd keys + DB rows atomically)
aegisctl system register --from-seed configs/systems/onlineboutique.yaml

# stage the pedestal chart into the producer pod and install it
aegisctl pedestal chart push   --chart dist/onlineboutique-<ver>.tgz
aegisctl pedestal chart install ob --namespace demo

# now the guided flow accepts the namespace
aegisctl inject guided   # or: aegisctl regression run <case> --auto-install
```

See the sibling skill `register-aegis-system` for the seed-yaml schema
and per-layer troubleshooting (etcd keys, DB fixture constraints, chart
repo URL overrides). Don't re-derive those here.

If you only want a one-shot smoke test and have no interest in the
datapack, path 3 (raw NetworkChaos CRD, ~20 lines of YAML) is still
perfectly fine — that's what the original session did:

```yaml
# networkchaos-currency.yaml — minimal path-3 escape hatch
apiVersion: chaos-mesh.org/v1alpha1
kind: NetworkChaos
# ... delay 200ms to=currencyservice, duration 120s
```

## Run + measure (phases 5–6)

First injection: `app=frontend`. Chaos stuck in `Failed to apply:
unable to flush ip sets ... expected docker:// but got container`. This
was the chaos-daemon runtime bug. Fixed via helm upgrade, re-applied,
chaos reached `AllInjected`.

Baseline/inject/recovery host-side curl:
- baseline p50 13.5 ms
- inject p50 **2.7 s** (90× amplification from 200 ms delay due to
  synchronous fan-out in the frontend request path)
- recovery p50 10.3 ms

Second injection: `app=currencyservice`, 120 s duration, same 200 ms
delay. Ran three timestamp windows, queried ClickHouse caller-side
client spans filtered on `SpanName LIKE '%CurrencyService%'`:
- baseline p50 1.8 ms
- inject p50 **202.0 ms** (200 ms delay lands exactly on the median)
- recovery p50 1.3 ms

Currencyservice's own server spans barely moved (0.159 → 0.206 ms) —
confirming the delay is on the network path, not inside the service.
This is always the difference between a meaningless measurement and a
clean one.

## Document (phase 7)

`docs/deployment/09-smoke-run.md` + `docs/deployment/otel-pipeline.yaml`
+ two new `known-gaps.md` entries:
- Chaos Mesh default `runtime=docker` is wrong for kind v1.34.
- aegisctl backend rejects arbitrary namespaces (needs a pedestal
  registered).

## What took time, in rank order

1. chaos-daemon runtime mismatch (~20 min): hidden by a
   confusingly-generic error; only diagnosed after reading the full
   daemon log stack trace.
2. Instrumentation patterns (~25 min): standard OTEL env vars, then
   gate discovery, then the Go panic after `ENABLE_TRACING=1` alone,
   then the third-try correct combination.
3. ClickHouse password mismatch (~10 min): trivial once spotted, but
   the collector error was buried in a long stack.
4. Host port collision with Flutter (~5 min): silent — curl hit the
   wrong process and returned plausible HTML.
5. Shell quoting of SQL timestamps (~5 min): had to pivot to
   `--queries-file`.

Time on the actual happy path (once all four of the above were solved):
~8 minutes including three 60-second wait windows.

## Reusable artifacts from this session

- `/home/ddq/AoyangSpace/aegis/docs/deployment/otel-pipeline.yaml`
- `/home/ddq/AoyangSpace/aegis/docs/deployment/09-smoke-run.md` (the
  phase-7 writeup, with full query and raw numbers)
