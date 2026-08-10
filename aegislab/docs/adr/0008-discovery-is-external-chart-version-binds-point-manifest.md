# Discovery is external; helm chart version binds the Point manifest

aegis-chaos owns the Point catalog as data but does not own the
*discovery* of Points. There is no in-process k8s scanner, OpenAPI
parser, ClickHouse trace probe, or JVM agent inside aegis-chaos.
External tools — onboarding scripts, aegisctl helpers, ad-hoc operator
imports — produce the Point list and POST it via the catalog API.

The binding rule that makes this clean: **one helm chart version of a
microservice corresponds to one Point manifest**. Deploying chart
`X@v1.2.0` of service `frontend` brings exactly the Points that were
specified at `v1.2.0`. The Point manifest is shipped with the chart,
versioned with the chart, and applied at install time. Catalog drift
between the deployed application and the injectable surface is
structurally impossible because the application *is* the catalog's
upstream.

## Consequences

- The `POST /v1beta/systems/{sys}:discover` endpoint is removed from
  the API. There is nothing for aegis-chaos to "discover" — Points
  arrive via `:import` only.
- `POST /v1beta/systems/{sys}/points:import` gains a
  `replace_scope: service | system | none` parameter (default `none`).
  `service` is the load-bearing mode: it tells aegis-chaos "this
  payload is the complete catalog for the named Service version;
  Points present in DB but absent from the payload are marked
  `superseded`." This is the atomic catalog rotation primitive that
  matches `helm install`/`helm upgrade` semantics.
- The `superseded` lifecycle described in §4 stops being driven by
  a "K rounds of discovery" heuristic. Instead it is driven by the
  next `:import replace_scope=service` payload, which is the only
  source of truth about "what Points this Service version has now."
- Service-removed cleanup follows naturally: `helm uninstall` triggers
  a post-delete hook that calls `:import replace_scope=service` with
  an empty payload, retiring all Points for that Service version
  atomically.
- aegis-chaos has no ClickHouse dependency, no k8s API watch beyond
  what each Executor needs, and no opinions about how Points are
  derived. This shrinks the service's blast radius and its
  configuration surface considerably.

## Considered options

- **a. External discovery + chart-version as manifest binding**
  (chosen). Catalog is data shipped with the application; aegis-chaos
  just serves it.
- **b. aegis-chaos runs probes itself** — original §6 design.
  Rejected: pulls ClickHouse, OpenAPI parsing, and watermark state
  into a service whose role is catalog + executor routing. Wrong
  shape.
- **c. Hybrid: external scripts plus an internal trace probe** —
  rejected as a confused middle. If discovery is data-in,
  aegis-chaos should not selectively own one probe.
