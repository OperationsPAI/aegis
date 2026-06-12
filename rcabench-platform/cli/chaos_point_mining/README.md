# chaos_point_mining

Mine the chaos-point surface that microservices **actually exercise during the
NORMAL phase**, from raw collected datapack traces — replacing the legacy
`clickhouseanalyzer` data source.

## Why

`clickhouseanalyzer` queries `otel.otel_traces` with **no phase filtering**, so
initialization, normal, and fault-injection traffic are folded together, and
endpoints are keyed by the **caller** (client spans). The catalog it feeds ends
up with points for routes that only fire at startup or only appear under a
fault, attributed to services that do not host them (e.g. `front-end` getting a
`/carts/*` point that `carts` actually serves; ts services getting their
downstream's routes).

This tool reads the **raw** `normal_traces.parquet` (the `[NORMAL_START,
NORMAL_END]` window) from many datapacks and emits, per system, only the
surface seen in steady-state normal traffic, attributed to the **server** that
hosts each endpoint, with an observation `count` so per-request long-tail can be
dropped.

## Pipeline

```bash
# 1. collect: enumerate + classify + download raw normal_traces.parquet
python collect.py --out /tmp/cpmine --aegisctl ~/.local/bin/aegisctl --insecure \
    --per-system 100 --projects pair_diagnosis session_aware_rca

# 2. mine: build per-system observed surface JSON
python mine.py --datapack-root /tmp/cpmine/dp \
    --out ../../../aegislab/tools/manifestgen/observed --min-count 2
```

`collect.py` fetches only the top-level `normal_traces.parquet` per datapack
(not the full `-r` datapack). System is derived from the datapack's env.json
`NAMESPACE` with trailing digits stripped (`otel-demo1` → `otel-demo`).

## Output (`aegislab/tools/manifestgen/observed/<system>.json`)

Per system: `services`, `http_endpoints` (service/method/path/port + count +
status + sample span_names), `grpc_operations`, `db_operations`, `edges`
(caller→callee from parent-span linkage + sample span_names). All paths/span
names normalized (UUID / numeric / train-code / id-run / opaque-token → `*`).
An aegis-side renderer turns this into `aegis-chaos/v1beta` PointManifests.

## Committed snapshot provenance

The checked-in `observed/*.json` were mined 2026-06-12 from `detector_success`
datapacks on byte-cluster (projects `pair_diagnosis` + `session_aware_rca`),
≤100 per system, `min_count=2`:
ts/media/teastore/sn/sockshop/otel-demo = 100 each, hs = 11 (full pool).
`ts` used post-June session-aware datapacks (no pre-June datapacks remain on the
cluster). Re-run the pipeline to refresh.

## Known limitations

- `jvm_*` / DB table+sql_type points are **not** trace-derivable (need bytecode
  analysis) — those families stay sourced from the existing manifestgen data.
- Single-digit synthetic ids (`user0`) are not collapsed (the id rule requires a
  2+ digit run); harmless, they survive as low-count distinct paths.
- DSB/Thrift internal services expose little HTTP (only the nginx edge); their
  surface is gRPC/`rpc.*` + topology edges, as expected.
