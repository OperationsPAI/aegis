# Remaining non-pair refs

Most app/platform images in this pack are now switched back to
`pair-diag-cn-guangzhou.cr.volces.com/pair/*`, based on refs already present in:

- `AegisLab/manifests/prod/rcabench.yaml`
- `AegisLab/data/initial_data/prod/otel-demo.yaml`
- `AegisLab/data/initial_data/prod/data.yaml`
- `AegisLab/data/initial_data/prod/ts.yaml`

The only refs that still do not use `pair/*` are the ones you confirmed live in `opspai` Docker Hub:

## Dataset images

- `docker.io/opspai/clickhouse_dataset:sum-optional-20260421`
- `docker.io/opspai/clickhouse_dataset:e2e-kind-20260421`

## OCI chart repo refs in seed data

- `oci://registry-1.docker.io/opspai`

If these are later mirrored into `pair-diag-cn-guangzhou.cr.volces.com/pair/*`, I can switch the last remaining refs too.
