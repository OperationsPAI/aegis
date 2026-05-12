# aegis-configcenter

Standalone configuration-center microservice. Hosts `module/configcenter`
— typed admin surface, layered merge (etcd > env > TOML > default),
watch fanout, and audit log. The only binary in v1 that holds etcd
write credentials.

## Run locally

```bash
docker compose up -d mysql etcd sso
cd src
go build -tags duckdb_arrow -o /tmp/aegis-configcenter ./cmd/aegis-configcenter
ENV_MODE=dev AEGIS_JWT_SECRET=... /tmp/aegis-configcenter serve --port 8087 --conf /etc/rcabench
curl http://localhost:8087/healthz
```

Default port `8087`. Endpoints:

- `GET    /healthz`
- `GET    /readyz`
- `GET    /api/v2/config/:namespace`              — list keys (etcd layer)
- `GET    /api/v2/config/:namespace/:key`         — get one
- `PUT    /api/v2/config/:namespace/:key`         — set (admin; writes audit)
- `DELETE /api/v2/config/:namespace/:key`         — delete (admin; writes audit)
- `GET    /api/v2/config/:namespace/:key/history` — recent audit rows
- `GET    /api/v2/config/:namespace/watch`        — SSE change stream

Consumers import `module/configcenterclient` and flip
`[configcenter.client] mode = "remote"` +
`[configcenter.client] endpoint = "http://aegis-configcenter:8087"`
to talk to this binary instead of holding an in-process Center. No
consumer code changes.
