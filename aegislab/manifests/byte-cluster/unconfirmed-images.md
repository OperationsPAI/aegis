# Remaining non-pair-host refs

Most app/platform images in this pack are now switched back to
`pair-diag-cn-guangzhou.cr.volces.com/pair/*`, based on refs already present in:

- `aegislab/manifests/prod/rcabench.yaml`
- `aegislab/data/initial_data/prod/otel-demo.yaml`
- `aegislab/data/initial_data/prod/data.yaml`
- `aegislab/data/initial_data/prod/ts.yaml`

The only refs that still do not use `pair/*` are the ones now mirrored under the `opspai` namespace in the Shanghai pair registry:

## Dataset images

- `pair-cn-shanghai.cr.volces.com/opspai/clickhouse_dataset:sum-optional-20260421`
- `pair-cn-shanghai.cr.volces.com/opspai/clickhouse_dataset:e2e-kind-20260421`

## OCI chart repo refs in seed data

- `oci://pair-cn-shanghai.cr.volces.com/opspai`

If these are later mirrored into `pair-diag-cn-guangzhou.cr.volces.com/pair/*`, I can switch the last remaining refs too.
