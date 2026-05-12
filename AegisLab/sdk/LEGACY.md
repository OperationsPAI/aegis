# LEGACY — generated artifacts, not exercised by validation

**Status as of 2026-05-12 (commit `17afc99`):** auto-generated SDKs
(`python/`, `python-gen/`, `typescript/`) produced from
`src/docs/swagger.{json,yaml}`. The validated cold-start flow
([`docs/deployment/cold-start-kind.md`](../../docs/deployment/cold-start-kind.md))
drives the platform entirely through `aegisctl` (Go, in `src/cli`) and
does not import any SDK from this directory.

Consumers (frontend, CLI tooling outside this repo) should regenerate
before use:

```bash
make swag-init && make generate-typescript-sdk
make generate-python-sdk
```

Treat the checked-in copies here as a convenience snapshot, not the
source of truth.
