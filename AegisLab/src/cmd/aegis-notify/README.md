# aegis-notify

Standalone notification microservice. Hosts `module/notification`
(per-user inbox, channel registry, publisher orchestrator) plus the
minimum auth surface needed to accept service-token-signed events.

## Run locally

```bash
docker compose up -d redis mysql aegis-sso
cd src
go build -tags duckdb_arrow -o /tmp/aegis-notify ./cmd/aegis-notify
ENV_MODE=dev AEGIS_JWT_SECRET=... /tmp/aegis-notify serve --port 8084 --conf /etc/rcabench
curl http://localhost:8084/healthz
```

Default port `8084`. Endpoints:

- `GET  /healthz`
- `POST /api/v2/events:publish`           — producers (service-token auth)
- `GET  /api/v2/inbox`                    — list a user's inbox
- `GET  /api/v2/inbox/stream`             — per-user SSE
- `GET  /api/v2/inbox/unread-count`
- `POST /api/v2/inbox/:id/read`
- `POST /api/v2/inbox/read-all`
- `POST /api/v2/inbox/:id/archive`
- `GET  /api/v2/inbox/subscriptions`      — preferences
- `PUT  /api/v2/inbox/subscriptions`

Producers in `aegis-backend` import `module/notificationclient` and
flip `[notification] mode = "remote"` + `[notification.remote]
base_url = "http://aegis-notify:8084"` to talk to this binary instead
of the in-process publisher. No producer code changes.
