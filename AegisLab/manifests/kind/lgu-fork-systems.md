# LGU-fork benchmarks on kind: install & regression runbook

Verified on 2026-04-23 against `opspai/rcabench:20260423-bb821fe-applabelunion` (app-label allowlist fix), with backend code at `main@bb821fe` plus `chaos-experiment` patches in `internal/resourcelookup/app_labels.go` + `internal/systemconfig/metadata_store.go`.

Covers the four LGU-forked benchmarks — `hs` (hotel-reservation), `sn` (social-network), `media` (media-microservices), `teastore` — consumed **directly** from the upstream gh-pages charts (no local wrapper). Each has a distinct wiring quirk that silently swallows traces if unaddressed; this runbook bakes each workaround into one `helm install` line so a fresh kind cluster reaches `datapack.no_anomaly` end-to-end.

Not a theory doc — if something here isn't in one of the four `helm install` blocks below, it's not needed.

## 0. Prerequisites

- `docs/deployment/cold-start-kind.md` steps 1–6 completed (kind cluster up, chaos-mesh, cert-manager, NFS, `otel-kube-stack` in `otel` ns with ClickHouse, rcabench backend with `loaded 8 systems (8 enabled)`).
- Helm repos:
  ```
  helm repo add lgu-dsb https://lgu-se-internal.github.io/DeathStarBench
  helm repo add lgu-tea https://lgu-se-internal.github.io/TeaStore
  helm repo update
  ```
- One-time: mirror the 5 infra images the DSB chart pulls under `$infraDockerRegistry/<name>`:
  ```
  for m in \
      alpine/git:latest:opspai/git:latest \
      library/memcached:1.6.7:opspai/memcached:1.6.7 \
      library/mongo:4.4.6:opspai/mongo:4.4.6 \
      library/redis:6.2.4:opspai/redis:6.2.4 \
      library/busybox:1.36:opspai/busybox:1.36 ; do
    src=${m%:*}; dst=${m##*:}
    docker pull --platform linux/amd64 "$src"
    docker tag "$src" "$dst"
    docker push "$dst"
  done
  ```
  DSB charts construct infra image names as `{{ $infraRegistry }}/<name>:<tag>`. Docker Hub has no `library/git`, and the chart's default `defaultImageVersion: 778a8df` never existed under `opspai/`, so keeping `infraDockerRegistry=docker.io/library` also fails for `git`. Mirroring once keeps every subsequent install hermetic.

## 1. Install — DSB stacks (hs / sn / media)

All three charts render `OTEL_EXPORTER_OTLP_ENDPOINT="http://$(NODE_IP):4318"` by default, but the Go services in the LGU fork prepend `http://` again → `http://http:%2F%2F...` → spans silently error out. The hs chart has a values path for the env; `sn` has `global.otel.endpoint` but the template forces `http://` back on; media hardcodes. Workaround: install all three with the known-good image tag + infra registry, then `kubectl set env` a naked `host:port` on every Deployment.

```
# hs ------------------------------------------------------------------
helm upgrade -i hs0 lgu-dsb/hotel-reservation -n hs0 --create-namespace --version 0.1.1 \
  --set global.dockerRegistry=docker.io/opspai \
  --set global.infraDockerRegistry=docker.io/opspai \
  --set global.defaultImageVersion=20260423-61074ea \
  --set global.services.environments.OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector.otel.svc.cluster.local:4318

# sn ------------------------------------------------------------------
helm upgrade -i sn0 lgu-dsb/social-network -n sn0 --create-namespace --version 0.1.1 \
  --set global.dockerRegistry=docker.io/opspai \
  --set global.infraDockerRegistry=docker.io/opspai \
  --set global.defaultImageVersion=20260423-61074ea

# sn: the chart template prepends http:// — post-install override on every Deploy
for d in $(kubectl -n sn0 get deploy -o name); do
  kubectl -n sn0 set env "$d" OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector.otel.svc.cluster.local:4318
done

# media ---------------------------------------------------------------
helm upgrade -i mm0 lgu-dsb/media-microservices -n mm0 --create-namespace --version 0.1.1 \
  --set global.dockerRegistry=docker.io/opspai \
  --set global.infraDockerRegistry=docker.io/opspai \
  --set global.defaultImageVersion=20260423-61074ea

for d in $(kubectl -n mm0 get deploy -o name); do
  kubectl -n mm0 set env "$d" OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector.otel.svc.cluster.local:4318
done
```

Notes:
- Omit `--wait` / `--atomic`. Load-generator's `fetch-datasets` init container can take 2–4 min on first run; `--wait` turns that into a timeout and leaves the release in `failed` state, which then makes aegis's `RestartPedestal` re-install from scratch even with `--skip-restart-pedestal`.
- `defaultImageVersion=20260423-61074ea` overrides the broken `778a8df` pin — present-day `opspai/{social-network-microservices,socialnetwork-loader,hotelreservation-loader,mediamicroservices-loader,media-microservices}:778a8df` doesn't exist; `20260423-61074ea` does.

## 2. Install — TeaStore

TeaStore 0.1.1 (merged from [LGU-SE-Internal/TeaStore#7](https://github.com/LGU-SE-Internal/TeaStore/pull/7)) parameterizes the injection annotation from values, so the CR pointer can be expressed in the `helm install` line directly — no post-install patch needed.

```
helm upgrade -i tea0 lgu-tea/teastore -n tea0 --create-namespace --version 0.1.1 \
  --set global.image.registry=docker.io/opspai \
  --set global.image.tag=20260423-2b3fd43 \
  --set opentelemetry.otlpEndpoint=http://otel-collector.otel.svc.cluster.local:4317 \
  --set opentelemetry.monitoringNamespace=otel \
  --set opentelemetry.instrumentationName=otel-kube-stack
```

`monitoringNamespace` + `instrumentationName` must match where the OpenTelemetry Operator's `Instrumentation` CR actually lives. For the aegis `otel-kube-stack` chart the CR is `otel/otel-kube-stack`.

For a cluster already on the 0.1.0 release: `helm upgrade` may fail on the immutable `tea0-teastore-jmeter` Job — `kubectl -n tea0 delete job tea0-teastore-jmeter` and re-run. Fresh installs don't hit this.

## 3. Verify traces are flowing

```
kubectl -n otel exec clickhouse-0 -- clickhouse-client -q "
SELECT ResourceAttributes['k8s.namespace.name'] AS ns, count() AS spans
FROM otel.otel_traces
WHERE Timestamp > now() - INTERVAL 2 MINUTE
GROUP BY ns
ORDER BY spans DESC"
```

Expect non-zero rows for `hs0`, `sn0`, `mm0`, `tea0` within ~60s of pod rollout. If any row is missing:
- `kubectl -n <ns> logs deploy/<svc> 2>&1 | grep "traces export"` — a stray `http:%2F%2F` in the URL is the Go SDK double-prefix bug; re-apply `kubectl set env ... OTEL_EXPORTER_OTLP_ENDPOINT=host:port`.
- For `tea0`: `kubectl -n tea0 get pod tea0-teastore-auth-0 -o jsonpath='{.spec.initContainers[*].name}'` should include `opentelemetry-auto-instrumentation-java`. If absent, the annotation still points at the wrong CR — re-check `monitoringNamespace`/`instrumentationName`.

## 4. Regression

Aegis needs the 0.1.1 pedestal versions registered before the regression yamls will submit. For a fresh seeded cluster the `data.yaml` bump in this PR handles it; for an in-flight cluster (seed already skipped) register once per host:

```
/tmp/aegisctl container register --pedestal --name hs       --registry docker.io --repo opspai --tag 0.1.1 --chart-name hotel-reservation   --chart-version 0.1.1 --repo-name lgu-dsb --repo-url https://lgu-se-internal.github.io/DeathStarBench
/tmp/aegisctl container register --pedestal --name sn       --registry docker.io --repo opspai --tag 0.1.1 --chart-name social-network     --chart-version 0.1.1 --repo-name lgu-dsb --repo-url https://lgu-se-internal.github.io/DeathStarBench
/tmp/aegisctl container register --pedestal --name media    --registry docker.io --repo opspai --tag 0.1.1 --chart-name media-microservices --chart-version 0.1.1 --repo-name lgu-dsb --repo-url https://lgu-se-internal.github.io/DeathStarBench
/tmp/aegisctl container register --pedestal --name teastore --registry docker.io --repo opspai --tag 0.1.1 --chart-name teastore           --chart-version 0.1.1 --repo-name lgu-tea --repo-url https://lgu-se-internal.github.io/TeaStore
```

Run (pass `--skip-restart-pedestal` so the backend doesn't blow away the release and re-install with no values):

```
cd AegisLab
for case in hotelreservation-guided socialnetwork-guided mediamicroservices-guided; do
  /tmp/aegisctl regression run "$case" --auto-install --skip-restart-pedestal --non-interactive --ready-timeout 300
done

# teastore labels its pods with `app=teastore` on every pod (umbrella) and
# per-service identity under `app.kubernetes.io/name`. Backend reads the
# latter (etcd config injection.system.teastore.app_label_key); preflight
# does not, so override the key explicitly.
/tmp/aegisctl regression run teastore-guided \
  --auto-install --skip-restart-pedestal --non-interactive --ready-timeout 300 \
  --app-label-key "app.kubernetes.io/name"
```

Each case exits 0 with `datapack.no_anomaly` on success. A failing `abnormal_traces.parquet: has no data rows` almost always means §3 verification was skipped — the inject succeeded but traces never reached ClickHouse.

## 5. Known quirks not fixed here

- DSB Go SDK still double-prefixes the scheme; every subsequent chart version needs the `kubectl set env host:port` post-step until that is fixed upstream in the LGU fork images. (`opspai/hotelreservation`, `opspai/social-network-microservices`, `opspai/media-microservices`.)
- DSB charts' `global.otel.endpoint` sanitizer prepends `http://` to any non-`http*` value. Even if the fork image stops double-prefixing, the template still needs to allow a naked `host:port` passthrough for aegis to configure cleanly via Helm values alone.
