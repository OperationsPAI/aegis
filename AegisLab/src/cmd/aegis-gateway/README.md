# `aegis-gateway`

L7 application gateway. Default port `:8086`.

Owns route → upstream mapping, JWT pre-auth via `clients/sso`,
trusted-header injection (HMAC-signed), global + per-route rate limit,
CORS, and access logging with trace propagation.

This binary has **no database** and **no business logic**. The router
implementation lives at `src/clients/gateway/`; the chart-side route
table lives at `helm/templates/configmap.yaml` (`[[gateway.routes]]`).

## Run

```bash
go run ./cmd/aegis-gateway serve --conf ./config.dev.toml --port 8086
```

Route table is loaded from the `[gateway]` section of the config file;
see `config.dev.toml` for the default microservice topology.
