# initial_data/

Seed data consumed by the `initialization` service (producer + consumer) on first boot. The path is selected via `initialization.data_path` (e.g. `/data/initial_data/prod`).

## Status

| Dir | Status | Notes |
|---|---|---|
| `prod/` | **Canonical / validated 2026-04-22** | Used by the kind cold-start flow. ClickHouse defaults aligned with `docs/deployment/otel-pipeline.yaml`. Covers 8 pedestals (ts, otel-demo, ob, sockshop, hs, sn, media, teastore) with matching `injection.system.<code>.*` keys. |
| `staging/` | **Legacy / unverified** | Historical staging copy. Drifted from `prod/` — do not consume without diffing first. |

If you need a separate environment, branch from `prod/` rather than `staging/`. Deleting `staging/` is fine once no caller references it.
