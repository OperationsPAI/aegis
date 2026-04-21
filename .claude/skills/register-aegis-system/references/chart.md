# Layer 4 reference: helm values, chart layout, ephemeral caches

Concrete commands and gotchas for the chart/values layer. Methodology
lives in `../SKILL.md` layer 4.

## Happy path: `aegisctl pedestal chart`

Two subcommands cover chart distribution + pre-install:

```bash
# Copy a packaged chart into the producer pod's /tmp cache.
# Auto-resolves the producer pod via kubectl label selector.
aegisctl pedestal chart push --name <code> --tgz ./<chart>.tgz

# Pre-install the chart into the correct namespace (release name = namespace).
# Auto-derives namespace from the system's ns_pattern via /api/v2/systems.
aegisctl pedestal chart install <code> --tgz ./<chart>.tgz --wait
aegisctl pedestal chart install <code> --repo https://charts.example.org \
                                       --chart <name> --version 0.1.0 --wait

# With no --tgz / --repo, aegisctl calls GET /api/v2/systems/by-name/<code>/chart
# and uses whatever source the backend returns (helm_configs.local_path,
# repo_url, or the etcd helm.repo.<name>.url override).
aegisctl pedestal chart install <code> --wait
```

`aegisctl regression run --auto-install` rolls `chart install` into the
regression flow; `--skip-preflight` opts out of the layer-2/3/4 checks.

## Contents

- [Happy path: `aegisctl pedestal chart`](#happy-path-aegisctl-pedestal-chart)
- [Wrapper chart layout](#wrapper-chart-layout)
- [values.yaml conventions](#valuesyaml-conventions)
- [Restart wipes /tmp gotcha](#restart-wipes-tmp-gotcha)
- [helm.repo.<name>.url etcd override](#helmreponameurl-etcd-override)
- [Hardcoded env in upstream values](#hardcoded-env-in-upstream-values)
- [WorkingDir / PATH alignment (single-image Go stacks)](#workingdir--path-alignment-single-image-go-stacks)
- [Release name = namespace invariant](#release-name--namespace-invariant)
- [No helm hooks for workaround jobs](#no-helm-hooks-for-workaround-jobs)
- [Cluster-level operator prerequisites](#cluster-level-operator-prerequisites)
- [Fallback: raw kubectl cp + helm install](#fallback-raw-kubectl-cp--helm-install)

## Wrapper chart layout

Four of the six current benchmarks (onlineboutique, DSB
hotelreservation / mediamicroservices / socialnetwork, TeaStore) publish
only a git tree. Pattern (umbrella: `OperationsPAI/benchmark-charts`,
issue #92):

```
charts/<system>/
  Chart.yaml           # dependencies: include a `repository: ""` entry
                       # even for embedded subcharts or helm lint fails
  values.yaml          # opinionated defaults — see below
  charts/<upstream>/   # copy, not `helm dep` pull, so templates are
                       # editable in-tree
  templates/           # added resources (ExternalName shims, etc.)
```

Bump the wrapper chart version on every change — aegis's helm gateway
caches by name+version, so a reused version won't re-pull the tgz.

## values.yaml conventions

Opinionated defaults the wrapper should set:

- Image registry pointing at our mirror.
- OTel endpoint pointing at the cluster collector.
- `LoadBalancer` Service type disabled (kind has no LB provider;
  `Wait=true` hangs forever otherwise).
- Disable subcharts that need external credentials (GCP metadata,
  managed DB, etc.).
- Absolute `mountPath` for config files (see Go-stack trap below).

## Restart wipes /tmp gotcha

Every backend `rollout restart` wipes `/tmp`. A pipeline assuming the
local tgz exists fails with `failed to locate chart /tmp/<chart>.tgz:
path ... not found` on the next inject. Fix: re-run `aegisctl pedestal
chart push --name <code> --tgz ...` after every rollout, or rely on one
of the two mitigations:

- `restart_pedestal.go` does `os.Stat(LocalPath)` and falls through to
  remote install when absent (local cache is optional).
- The etcd override `helm.repo.<repo_name>.url` supplies a URL when
  `helm_configs.repo_url` is empty.

On networked clusters the etcd override is enough. On air-gapped
clusters a pre-staged tgz is still required.

## helm.repo.<name>.url etcd override

Seed a `dynamic_configs` row (Global scope, string value_type) and an
etcd value. The preferred path is via aegisctl config wiring, but this
single key isn't covered by `aegisctl system register`; raw etcdctl:

```bash
etcdctl put /rcabench/config/global/helm.repo.<repo_name>.url \
  https://example.org/charts
```

`helm_configs.repo_url` is then the override-of-override; leave it
empty to pick up the etcd value.

## Hardcoded env in upstream values

Charts frequently bake in dev-invalid collector DNS like
`opentelemetry-kube-stack-deployment-collector.monitoring`. `kubectl set
env` on the deploy is **not durable** — `restart_pedestal` re-runs `helm
upgrade` from the file each cycle, overwriting live edits. Fix the
values file, not live deploys.

## WorkingDir / PATH alignment (single-image Go stacks)

DSB-family benchmarks ship one image containing all service binaries.
Upstream charts encode the entry as `command: ./<svc>`, which only works
if `WorkingDir` is the binary directory. Many images set
`WorkingDir=/workspace` (source tree) while binaries live in `/go/bin`
(on PATH).

Symptom: every pod `RunContainerError`:
`exec: "./frontend": no such file or directory`.

Also: relative `mountPath: config.json` resolves to `/config.json`, but
the binary reads `./config.json` from `WorkingDir` → configs silently
never apply.

Two-line fix pattern:

- Drop the `./` prefix (`command: ["frontend"]`) so the shell
  PATH-resolves.
- Change `mountPath` to an absolute path under `WorkingDir`
  (e.g. `/workspace/config.json`).

Don't try to override `WorkingDir` via values — most charts don't
expose it.

## Release name = namespace invariant

Aegis's `installPedestal` in `src/service/consumer/restart_pedestal.go:178`
derives release name from namespace. Charts often bake the release name
into pod labels (`app: <svc>-<release>-<mainChart>`). Mismatched
pre-install release → labels that differ from what aegis produces →
guided resolution picks up a set of apps that disappears on the first
aegis-driven upgrade.

`aegisctl pedestal chart install <code>` enforces this automatically:
it uses the derived namespace as the release name. Only matters if you
fall back to raw `helm install`.

## No helm hooks for workaround jobs

`installAction.Wait=true` + any crashlooping resource = deadlock where
post-install hooks never fire. Do not use a helm hook Job to patch
around chart limitations — patch templates directly.

## Cluster-level operator prerequisites

Charts for operator-backed workloads (Oracle Coherence, Strimzi,
pg-operator, keycloak-operator, elastic-operator, …) assume the
operator's CRDs and controller pods are *already present*. Aegis does
not manage these today.

First-onboarding checklist:

1. Operator present? `kubectl get crd | grep <operator-domain>` and
   `kubectl get deploy -n <operator-ns>`.
2. Admission webhook reachable from aegis namespaces?
3. CR fields match the installed operator version (pin in notes).

Until aegis grows a `prerequisites` declaration driving an idempotent
`helm upgrade --install`, record per-system operator dependencies
out-of-band.

## Fallback: raw kubectl cp + helm install

Only use when aegisctl is unavailable.

```bash
# tgz into producer pod
kubectl -n aegis cp /path/to/<chart>.tgz \
  aegislab-producer-0:/tmp/<chart>.tgz

# values file
kubectl -n aegis cp values.yaml \
  aegislab-producer-0:/var/lib/rcabench/dataset/helm-values/<code>.yaml

# pre-install — release name MUST equal namespace
helm install <ns> ./charts/<system> -n <ns> --create-namespace
```
