# Datapack Output Schema

The datapack is the unit of data handed off to downstream RCA pipelines. It is
produced by a Kubernetes Job running the `clickhouse_dataset:latest` image
(driver: `/app/cli/prepare_inputs.py`, main entry around `prepare_inputs.py:366`).
One datapack corresponds to one fault injection run.

## Files

### Parquet (12 total)

Two windows × six signals:

| Window     | Signals                                                                                                  |
|------------|----------------------------------------------------------------------------------------------------------|
| `normal_`  | `logs`, `metrics`, `metrics_histogram`, `metrics_sum`, `trace_id_ts`, `traces`                           |
| `abnormal_`| same six                                                                                                  |

Filenames: `{normal,abnormal}_{logs,metrics,metrics_histogram,metrics_sum,trace_id_ts,traces}.parquet`.

### JSON (3 total)

- `env.json` — run metadata: injection id, system, ns slot, windows, versions.
- `k8s.json` — cluster snapshot at collection time: pods, services, deployments,
  endpoints in the target namespace. Requires `rcabench-sa` read RBAC on those
  resources.
- `injection.json` — fault definition (GuidedConfig, target pods, chaos CRD
  reference). Written during the inject cycle by the backend; **not** refreshed
  on datapack retry, so retries reuse the original.

### Integrity

- `sha256sum.txt` — manifest over all 15 files above.

## Time Windows

Four timestamps, all **epoch seconds, UTC**:

```
NORMAL_START   NORMAL_END   (= ABNORMAL_START - pre_duration_seconds)
ABNORMAL_START ABNORMAL_END (injection start → injection start + duration)
```

The datapack SELECTs from ClickHouse with `WHERE Timestamp BETWEEN ... AND ...`
(traces use `Timestamp`, metrics use `TimeUnix` — see
`aegislab_e2e_pitfalls.md` memory on the column split).

### Timezone pitfall

ClickHouse `otel_traces.Timestamp` is stored in UTC. The datapack pod's
`DB_TIMEZONE` env drives how the pipeline formats the window boundaries before
sending them to ClickHouse. If `DB_TIMEZONE=Asia/Shanghai` (the helm values
default) the window is shifted +8h and the query returns zero rows — which
then fails validation on "Parquet file has no data rows". **Set
`DB_TIMEZONE=UTC` on the datapack pod.** Fix tracked as a values-file change.

## Source Schema

- ClickHouse database: `otel` (**not** `default` — the collector is configured
  to write the `otel` database; datapack must match).
- Tables: `otel_traces`, `otel_metrics_sum`, `otel_metrics_histogram`,
  `otel_metrics_gauge` (queried as `otel_metrics`), `otel_logs`,
  `otel_traces_trace_id_ts`.

Required resource attributes on every signal: `k8s.namespace.name`,
`k8s.pod.name`, `service.name`. These depend on the collector's
`k8sattributes` processor having pod + namespace RBAC — missing pod RBAC
means `k8s.namespace.name` is absent and every datapack query filters to
zero rows.

## Validation

All-or-nothing. The pipeline rejects the datapack if:
- any of the 15 files is missing, or
- any parquet has zero data rows, or
- the sha256 manifest mismatches.

No `--tolerate-empty` / `.partial` mode exists today. Tracked as code gap —
the first 1-minute window after a fresh helm reinstall often has zero logs,
which kills the whole datapack.

## File Pointers

- Pipeline entry: `/app/cli/prepare_inputs.py:366` (inside
  `clickhouse_dataset:latest`)
- Collection configuration (window, namespace slot): `env.json`
- Validation result is POSTed back to the backend at
  `http://<k8s.service.internal_url>/api/v1/datapack/callback`.
