# aegis-blob

Standalone blob storage microservice. Hosts `module/blob` (driver
registry, metadata repository, presign/inline handlers, lifecycle
worker) plus the minimum auth surface needed to accept
service-token-signed requests.

## Run locally

```bash
docker compose up -d mysql sso
cd src
go build -tags duckdb_arrow -o /tmp/aegis-blob ./cmd/aegis-blob
ENV_MODE=dev /tmp/aegis-blob serve --port 8085 --conf /etc/rcabench
curl http://localhost:8085/healthz
```

Default port `8085`. Endpoints (`/api/v2/blob/*`):

- `POST   /buckets/:bucket/presign-put`
- `POST   /buckets/:bucket/presign-get`
- `GET    /buckets/:bucket/objects/:key`        — inline get
- `HEAD   /buckets/:bucket/objects/:key`        — stat
- `DELETE /buckets/:bucket/objects/:key`
- `GET    /buckets/:bucket/objects`             — list by entity
- `GET|PUT /raw/:token`                          — localfs signed-token endpoint

Producers in `aegis-backend` import `module/blobclient` and flip
`[blob.client] mode = "remote"` + `endpoint = "http://aegis-blob:8085"`
to talk to this binary instead of the in-process service. No producer
code changes.

## Buckets

Declared in TOML — see `[blob.buckets.<name>]` in `config.dev.toml`.
v1 supports two drivers:

- `localfs` — bytes on disk; presign mints HMAC-signed token URLs
  served by `/raw/:token` on this binary.
- `s3` — stub returning `ErrDriverNotImplemented`. Real
  implementation lands in Phase B (see RFC).
