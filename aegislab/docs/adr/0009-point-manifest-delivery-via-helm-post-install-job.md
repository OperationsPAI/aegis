# Point manifests are delivered via a helm post-install Job hook

Each microservice helm chart that participates in aegis fault
injection includes a `post-install` / `post-upgrade` Job hook that
POSTs the chart's Point manifest to `:import replace_scope=service`,
and a `post-delete` / `pre-delete` Job hook that POSTs an empty
payload to retire the Service version. Job failure surfaces as
chart install/upgrade failure, so a chart whose manifest is broken
cannot silently ship with a stale catalog.

## Consequences

- Chart authors write the hook once. The hook is templated against
  the chart's `Chart.Version` so the catalog rotation is automatic on
  every release.
- aegis-chaos does not watch any Kubernetes resources in benchmark
  namespaces. Its only cluster-side dependency remains the Chaos-Mesh
  CRDs it manages via the chaos-mesh Executor, in chaos-mesh's own
  namespace.
- The Job needs network reach to aegis-chaos and a service token
  with `write:catalog`. Standard k8s ServiceAccount + Secret pattern;
  no new infrastructure.
- For benchmarks whose chart we don't control (upstream
  social-network, hotel-reservation, …), the manifest is shipped as
  a sidecar chart in the LGU fork. This is consistent with current
  practice of forking these charts for OTel wiring.

## Considered options

- **a. Helm post-install Job hook** (chosen). Failures are loud and
  tied to the deploy.
- **b. ConfigMap with aegis-chaos watch** — elegant, but adds k8s
  read RBAC across all benchmark namespaces, contradicting the
  design goal of minimising aegis-chaos's cluster surface.
- **c. Out-of-band onboarding script** — too loose; drift between
  deployed chart and registered manifest reappears the first time
  someone forgets to run it.
