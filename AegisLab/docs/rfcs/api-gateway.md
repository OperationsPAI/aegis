# RFC: API Gateway (`cmd/aegis-gateway`)

- Status: **Draft**
- Owners: aegis-backend
- Stakeholders: every downstream service (sso, notify, blob, monolith
  api), aegis-ui (single base URL)
- Related: `helm/templates/edge-proxy.yaml` (current Caddy proxy that
  this replaces in front of api-gateway), `module/ssoclient`,
  `module/ratelimiter`, `module/configcenter` RFC

---

## Summary

Replace the current Caddy "edge-proxy" + monolith `cmd/api-gateway`
pattern with a proper L7 application gateway: `cmd/aegis-gateway`,
default `:8086`. It owns route → upstream mapping, JWT pre-auth (via
`ssoclient`), global per-route rate limit (via `ratelimiter`), CORS,
request logging, and per-upstream health. Caddy retreats to TLS
termination + static asset serving only.

The current `cmd/api-gateway` is renamed to `cmd/aegis-api` (it was
always the monolith API binary; calling it a gateway misled both
operators and new contributors).

## Motivation

Right now "API gateway" means two unrelated things:

| Layer | What it is | Problem |
| --- | --- | --- |
| `helm/templates/edge-proxy.yaml` (Caddy) | Pure reverse proxy: `:80 → api-gateway-svc:80` and `:9096 → api-gateway-svc:9096` | No auth, no rate limit, no route awareness. Just a port forwarder. |
| `cmd/api-gateway` + `app/gateway/options.go` | "The single API binary: it owns every module" | Misnamed. It's the monolith. As we extract sso / notify / blob into independent binaries, the "gateway" name leaks the wrong mental model. |

Concretely:

- A request hitting `/api/v2/inbox/...` should land on `aegis-notify`,
  not the monolith. Today Caddy doesn't know that.
- A request hitting `/api/v2/blob/...` should land on `aegis-blob`.
- JWT verification is duplicated in every backend service. Verifying
  once at the gateway would let services trust headers
  (`X-Aegis-User-Id`, `X-Aegis-Roles`) instead of re-running the
  ssoclient pipeline.
- Rate limiting is per-process, which means per-pod. A real gateway
  gives us a single chokepoint that's easier to operate.

## Non-goals

- Service mesh (mTLS between services, sidecar injection, observability
  spans across hops). If we go there, it's Istio/Linkerd, not a gateway.
- Layer-4 load balancing. Kubernetes Services do that.
- Caching. CDN handles caching for static assets.
- API composition / GraphQL stitching. Out of scope.
- WAF rules beyond CORS + simple header checks. Buy a real WAF if/when
  needed.

## Proposed design

### Topology

```
                ┌──────────────────────────────────────────────────────┐
                │                  Caddy (TLS termination)             │
                │  - cert mgmt                                         │
                │  - static asset serving (apps/console build)         │
                │  - forwards everything else to aegis-gateway         │
                └────────────────────┬─────────────────────────────────┘
                                     │
                              ┌──────▼───────┐
                              │ aegis-gateway│   (this RFC)
                              │   :8086      │
                              └──────┬───────┘
            ┌───────────┬────────────┼────────────┬─────────────┐
            │           │            │            │             │
       ┌────▼─────┐ ┌───▼────┐ ┌─────▼─────┐ ┌────▼─────┐ ┌─────▼────┐
       │ aegis-sso│ │aegis-  │ │aegis-blob │ │aegis-    │ │ aegis-api│
       │  :8083   │ │notify  │ │  :8085    │ │notify    │ │  (monolith)│
       │          │ │ :8084  │ │           │ │  :8084   │ │   :8080  │
       └──────────┘ └────────┘ └───────────┘ └──────────┘ └──────────┘
```

### Route table

Routes are config-driven (and once `configcenter` lands, hot-
reloadable from etcd). Initial table:

```toml
[[gateway.routes]]
prefix     = "/v1/"            # OIDC endpoints
upstream   = "aegis-sso:8083"
auth       = "none"             # SSO itself owns these endpoints

[[gateway.routes]]
prefix     = "/.well-known/"
upstream   = "aegis-sso:8083"
auth       = "none"

[[gateway.routes]]
prefix     = "/api/v2/inbox/"
upstream   = "aegis-notify:8084"
auth       = "jwt"
audiences  = ["portal"]

[[gateway.routes]]
prefix     = "/api/v2/notifications/"
upstream   = "aegis-notify:8084"
auth       = "jwt"

[[gateway.routes]]
prefix     = "/api/v2/blob/"
upstream   = "aegis-blob:8085"
auth       = "jwt"

[[gateway.routes]]
prefix     = "/api/v2/auth/"     # human-user login/register via monolith
upstream   = "aegis-api:8080"
auth       = "none"

[[gateway.routes]]
prefix     = "/"                  # everything else → monolith
upstream   = "aegis-api:8080"
auth       = "jwt"
```

Match order is first-match. Per-route options:

- `auth`: `none` | `jwt` | `service-token` | `jwt-or-service`
- `audiences`: optional, restricts JWT audience
- `rate_limit`: `{rps, burst}` — overrides global default
- `strip_prefix`: bool, default false
- `timeout_seconds`: int, default 30
- `retry`: `{attempts, on_status: [502, 503, 504]}` — default 0

### JWT pre-auth

Verify once at the gateway via `ssoclient` (same library every backend
uses today). On success, **inject headers** the upstream can trust:

```
X-Aegis-User-Id: 42
X-Aegis-User-Email: alice@aegis.example
X-Aegis-Roles: admin,operator
X-Aegis-Token-Aud: portal
X-Aegis-Token-Jti: abc123
```

The upstream's `middleware/auth` is updated to:

1. If `X-Aegis-User-Id` present **and** the request arrived on the
   intra-cluster network (configurable IP whitelist or mTLS), trust
   it directly — skip re-verification.
2. Otherwise, run the existing ssoclient verification path (for
   anyone hitting the upstream directly during development or
   tests).

This is intentionally additive: the upstream still works without
the gateway. The gateway is an optimization + policy point, not a
hard dependency.

### Rate limiting

Global limiter in front of every route, configurable per route. Use
`module/ratelimiter` as the limiter implementation; in v1 it remains
in-process (per gateway pod). When we run multiple gateway replicas:
either accept per-pod limits (typical for L7 gateways) or back the
limiter with Redis (`ratelimiter` already supports a Redis backend —
flip via config).

### CORS

Single CORS policy applied at the gateway:

```toml
[gateway.cors]
allowed_origins  = ["http://localhost:3101", "https://aegis.example"]
allowed_methods  = ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
allowed_headers  = ["Authorization", "Content-Type"]
allow_credentials = true
max_age_seconds  = 86400
```

Upstreams no longer add CORS headers (they were inconsistent — we
remove that code in a follow-up).

### Logging + observability

- One access log line per request: `ts | route | upstream | method |
  path | status | latency_ms | user_id | request_id`
- Trace span per request, propagated via `traceparent` header to the
  upstream (reuse `infra/tracing`).
- Metrics: `gateway_requests_total{route, status}`,
  `gateway_request_duration_seconds{route}`,
  `gateway_upstream_errors_total{route, kind}`,
  `gateway_ratelimit_drops_total{route}`.

### Health

- `GET /healthz` on gateway → 200 if the gateway is alive.
- `GET /readyz` on gateway → 200 if it can reach a configurable
  subset of upstreams (the gateway holds a short-lived TCP probe
  pool per upstream).
- Per-upstream circuit breaker (open after N consecutive 5xx) ships
  in v2; v1 just retries per route config.

### Configuration

Driven by:

1. `[gateway.routes]` table in `config.<env>.toml` (static).
2. Once `configcenter` lands: `/aegis/<env>/gateway/routes` in etcd
   overrides + adds. Hot reload, no restart.

### Failure modes

- Upstream unreachable → 502 with request_id; metric bumped; retry
  per route policy.
- JWT verification fails → 401; never reaches upstream.
- Rate limit exceeded → 429 with `Retry-After`.
- Gateway pod crash → kube replaces it; route table is config so
  no state to recover.

### What about gRPC?

The current edge-proxy also forwards `:9096` for the runtime-intake
gRPC. v1 of `aegis-gateway` does **not** terminate gRPC — we keep
Caddy or a dedicated gRPC proxy for that one path until we have a
second gRPC service worth gatewaying. Premature otherwise.

## Migration plan

1. **Phase A (this PR)**: rename `cmd/api-gateway` → `cmd/aegis-api`,
   `app/gateway` → `app/monolith` (the existing options file).
   Land `cmd/aegis-gateway` + `app/gateway` (the new gateway). Wire
   route table in TOML. No production traffic yet.
2. **Phase B**: Helm chart adds the gateway deployment + service. The
   existing `edge-proxy` Caddy starts pointing at `aegis-gateway`
   instead of `aegis-api`.
3. **Phase C**: trusted-header upstream short-circuit lands in
   each backend (`module/auth`). Re-test that direct upstream access
   still works (for dev / `pnpm dev` proxy fallback).
4. **Phase D**: once `configcenter` is live, move the route table to
   etcd for hot reload.

## Alternatives considered

- **Use Caddy as the gateway (Caddyfile routes + plugins).** Possible,
  but JWT verification needs a Caddy plugin written in Go anyway, and
  we lose the ability to reuse `ssoclient` and `ratelimiter`
  directly. Easier to own the gateway as a Go binary.
- **Use Envoy / Kong / Traefik.** All capable; all add an
  operational component the team doesn't run. JWT plugins exist
  (Envoy JWT filter) but mapping our specific audience/role
  semantics is non-trivial. Defer until we outgrow a Go gateway.
- **Stay on Caddy reverse proxy + per-service auth.** Cheapest, but
  every new service re-runs the ssoclient pipeline, CORS gets
  duplicated, rate-limit policy fragments. The whole point of
  pulling sso/notify/blob into independent services is to make this
  the natural extraction point.
- **Build into the monolith ("monolith dispatches to RPCs").** That's
  what we have today, modulo the missing RPCs. The monolith binary
  shouldn't grow more responsibilities; it should shed them.

## Open questions

1. **Service-token issuance for upstreams.** When the gateway calls
   an upstream with trusted-header injection, does the upstream
   require a service token in addition? Proposal: no for v1 — IP
   allowlist + signed header (HMAC over user_id+ts+jti) is enough.
   mTLS as a v2 hardening step.
2. **mTLS between gateway and upstreams.** Skip in v1 (rely on
   network policies). Add in v2 if compliance demands it.
3. **Where do we run a /admin route?** Proposal: route `prefix =
   "/api/v2/admin/"` → monolith, `auth = "jwt"`, `roles = ["admin"]`
   (new field), and the monolith trusts the role header.

## Acceptance criteria

- `aegis-gateway serve --port 8086` boots, loads route table from
  TOML, passes `/healthz`.
- Hitting `http://gateway/api/v2/inbox` from a logged-in browser
  reaches `aegis-notify` with `X-Aegis-User-Id` injected, and the
  inbox returns the user's items.
- Hitting `http://gateway/api/v2/inbox` without a JWT returns 401
  from the gateway (never reaches notify).
- Rate-limit headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`)
  present on every response.
- `pnpm dev` proxy in `apps/console/vite.config.ts` points at
  `aegis-gateway` instead of the SSO target directly, and both the
  /authorize flow AND the inbox flow work end-to-end.
- The monolith's existing tests still pass when called directly
  (not via the gateway).
