# aegis-sso

Standalone identity service for the Aegis platform. Hosts `module/user`,
`module/auth`, and `module/rbac` only — no chaos/k8s/business modules.

## Run locally

```bash
docker compose up -d redis mysql
cd src
go build -tags duckdb_arrow -o /tmp/aegis-sso ./cmd/aegis-sso
ENV_MODE=dev /tmp/aegis-sso sso --port 8083 --conf config.dev.toml
curl http://localhost:8083/healthz
```

Default port is `8083`. OIDC + `/v1/*` admin endpoints land in PR-1b.
