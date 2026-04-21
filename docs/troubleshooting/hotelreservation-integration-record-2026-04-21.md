# HotelReservation integration record — 2026-04-21

Integrated DeathStarBench's `hotelReservation` (Go microservices, 9 services +
infra) onto aegis-local as the fourth benchmark system. Goals: re-validate the
register-aegis-system methodology post-sockshop; surface what's different
about a DSB-style Go/Consul/Jaeger stack.

## Pipeline status

End-to-end flow ran through: `RestartPedestal → FaultInjection → BuildDatapack`.
Datapack validation failed on `abnormal_metrics_sum.parquet has no data rows`
— identical class of gap as sockshop (Prometheus/Jaeger stack, no OTel
metrics pipeline). Control-plane layers 1-5 all validated working.

## Artifacts produced

- `benchmark-charts/charts/hotelreservation-aegis/` 0.1.0 — wrapper chart
  embedding DSB's `hotelReservation/helm-chart/hotelreservation` unchanged
  except for two per-service values overrides (see blockers below). Pushed
  to `oci://registry-1.docker.io/opspai/hotelreservation-aegis:0.1.0`.
- No image build needed: `docker.io/deathstarbench/hotel-reservation:latest`
  is a single multi-service image with all Go binaries in `/go/bin` (PATH).
  Infra images pulled on demand (consul 1.13.2, memcached, mongo 5.0, jaeger).
- `AegisLab/data/initial_data/{prod,staging}/data.yaml` — added hotelreservation
  pedestal entry + 7 `injection.system.hs.*` dynamic_configs (prefix `hs` to
  match the compiled systemconfig registry).
- `AegisLab/regression/hotelreservation-guided.yaml` — smoke case targeting
  `app=frontend-hs0-hotelres` in ns `hs0`.

## Blockers hit

### 1. `./frontend` vs PATH — upstream chart assumes WorkingDir=/go/bin

Upstream chart's `command: ./frontend` (and ./geo, ./profile, ...) relies on
WorkingDir being the binary directory. The published image has
`WorkingDir=/workspace` but binaries are in `/go/bin` (on PATH). Result:
`exec: "./frontend": stat ./frontend: no such file or directory`.

Fix: per-service `values.yaml` patch in wrapper chart — drop the `./`
prefix so the container shell resolves via PATH. 8 services patched.

### 2. Upstream mountPath `config.json` resolves to `/config.json`

Kubernetes interprets relative `mountPath: config.json` as `/config.json`,
but the binary reads `./config.json` relative to WorkingDir=/workspace.
Result: service starts, fails to parse config (tries to reach literal
`consul:8500` from hardcoded defaults instead of the Helm-templated FQDN).

Fix: change each subchart's `configMaps[0].mountPath` to
`/workspace/config.json`.

Both fixes together are the "DSB Go-image chart needs WorkingDir
alignment" methodology note — worth adding to the skill.

### 3. data.yaml → dynamic_configs DB rows still a one-way gap (#91.5)

Same as sockshop. Editing `data.yaml` does not populate `dynamic_configs`
on a running cluster; `initializeDynamicConfigs` (consumer.go:118) only
runs during fresh seed. Manual inserts required:

- 7 rows into `dynamic_configs` for `injection.system.hs.*`
- 7 keys under `/rcabench/config/global/injection.system.hs.*` in etcd

Additionally observed: if you `etcdctl put` before the DB rows exist, the
`config_listener` rejects the change (`record not found`) and the runtime
system registry never picks up the system. Correct order: DB rows first,
then etcd put — OR batch both and let the listener eventually converge on
re-put. Add to methodology.

### 4. Pedestal name must match system code, not display name

System registry uses `SystemHotelReservation = "hs"` (short code) as the
NsPattern prefix. The submit validator checks
`container_name == system_type`, so the pedestal container row must be
named `hs`, not `hotelreservation`. The data.yaml and DB insert pattern
from sockshop (where system code and display name were both `sockshop`)
masked this — now explicit: the pedestal container `containers.name`
must equal the compiled `SystemType` value from
`chaos-experiment/internal/systemconfig/systemconfig.go`, not the
display-facing name.

Fix: `UPDATE containers SET name='hs' WHERE name='hotelreservation' AND type=2;`
+ update regression YAML `pedestal.name: hs`.

**Methodology impact:** for any system in the compiled registry, the
data.yaml pedestal entry must use the short system code as its `name`
(the short Go constant — `ts`, `ob`, `hs`, `sn`, `media`), even when
the display name is longer.

### 5. First-submit still needs manual pre-install (#91 — unresolved)

Submit-time guided resolution lists pods before RestartPedestal runs.
Empty namespace → `system "hs" does not match any registered namespace
pattern or system name` (misleading — actual cause is zero-app-set,
masked by the patterns message). Workaround: `helm install hs0 ...`
manually once before the first aegisctl submit.

### 6. `kind load` fails for most images on containerd 2.1 / ctr

`ctr: content digest ...: not found` for mongo/memcached/consul/jaeger.
Workaround: rely on cluster's internet to pull on demand
(`imagePullPolicy: IfNotPresent`). Single-service image
`deathstarbench/hotel-reservation:latest` did load successfully — pattern
isn't universal but affected manifests have multi-arch attestations
that ctr 2.1 can't import.

Not an aegis issue; upstream kind/containerd.

### 7. Release name drift (pre-install vs aegis install)

`helm install hotelreservation ...` vs `helm install hs0 ...` produced
different pod-app-label suffixes. When pre-installing manually (to
work around #5), use release name = the namespace (`hs0`), because
aegis's `installPedestal` uses `releaseName = namespace`
(`src/service/consumer/restart_pedestal.go:178`).

**Methodology:** pre-install release name must match the namespace, or
labels produced by `helm install` differ from what aegis would produce
on its own install.

## What went well

- No image build: single DSB image has all binaries — 30 minutes saved
  vs sockshop's Jib build.
- Upstream chart already sets `app: <svc>-<fullname>` on pods, so no
  label-propagation patching (unlike sockshop's Coherence CR).
- PV/PVC gated behind `mongodb.persistentVolume.enabled=false`, so kind
  works out of the box.
- `app` label value pattern (`<svc>-<release>-<mainChart>`) is
  deterministic once release name is pinned.

## Remaining follow-ups

1. **[new for this session]** Upstream DSB chart has WorkingDir
   misalignment (blockers #1, #2). Should stay in our wrapper as
   per-service value overrides — file a methodology note to skill.
2. **[new for this session]** Pedestal container `name` must equal the
   short system code (blocker #4). Skill note.
3. **[new for this session]** etcd-put-before-DB-insert gets rejected
   by config_listener (blocker #3 variant). Skill note.
4. **[existing]** data.yaml → dynamic_configs auto-publish gap
   (#91.5) — unchanged from sockshop.
5. **[existing]** Backend image rebuild (#17) — blocks remote helm
   install, still using `local_path` fallback.
6. **[existing]** sockshop/hs both need `RCABENCH_OPTIONAL_EMPTY_PARQUETS`
   env override for datapack validation to pass — not a pipeline gap.
