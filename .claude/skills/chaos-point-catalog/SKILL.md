---
name: chaos-point-catalog
description: How to (re)generate the chaos-point catalog from the NORMAL-phase raw traces of collected datapacks, instead of the legacy clickhouseanalyzer (which has no phase filter and keys endpoints by the caller). Covers collect → mine → render → merge-jvm → replace aegis-chaos/ → PR, and the traps (raw-vs-converted blob path, server-vs-caller attribution, infra/mq name resolution, jvm_mysql format, bridgeless tproxy, cold-service drops). Trigger when the user says "重新生成 / 更新 chaos point", "catalog 里好多无效点 / 不存在的点", "从 trace 生成 chaos point", "refresh / regenerate the chaos catalog", "mine chaos points", "observedgen", or asks why a catalog point targets a route a service doesn't host.
---

# chaos-point-catalog — mine chaos points from normal-phase traces

The catalog the chaos system reads lives in `aegislab/manifests/aegis-chaos/<system>/`.
The legacy generator (`clickhouseanalyzer`) queries `otel.otel_traces` with **no
phase filtering** (init + normal + fault folded together) and keys HTTP endpoints
by the **caller**, so it emits points for routes that only fire at startup / under
a fault, attributed to services that don't host them. This pipeline rebuilds the
catalog from the **NORMAL-phase raw traces** of ~100 datapacks/system, so a point
exists only if it was actually exercised in steady-state traffic, attributed to
the **server** that hosts it.

Tools (PR #571): `rcabench-platform/cli/chaos_point_mining/{collect,mine}.py` +
`aegislab/tools/observedgen/{main.go,merge-catalog.sh}`.

## Pipeline

```bash
AEG=~/.local/bin/aegisctl
# 1. collect: enumerate detector_success injections, classify by system from each
#    env.json NAMESPACE, download only the RAW normal_traces.parquet + k8s.json.
python rcabench-platform/cli/chaos_point_mining/collect.py \
  --out /tmp/cpmine --aegisctl "$AEG" --insecure --per-system 100 \
  --projects pair_diagnosis session_aware_rca
# (ts pre-June only: add --ts-before-epoch $(date -u -d 2026-06-01 +%s))

# 2. mine: build per-system observed-surface JSON (writes into the aegis tree)
python rcabench-platform/cli/chaos_point_mining/mine.py \
  --datapack-root /tmp/cpmine/dp \
  --out aegislab/tools/manifestgen/observed --min-count 2

# 3. render: observed JSON -> PointManifests (separate scratch dir, gitignored)
cd aegislab/tools/observedgen
go run . -in ../manifestgen/observed -out ../../manifests/aegis-chaos-observed
cd -

# 4. merge bytecode jvm: carry jvm_method_* / jvm_runtime_mutator from the current
#    catalog for surviving services -> complete catalog
bash aegislab/tools/observedgen/merge-catalog.sh \
  aegislab/manifests/aegis-chaos-observed \
  aegislab/manifests/aegis-chaos \
  aegislab/manifests/aegis-chaos-merged

# 5. replace the live catalog for the covered systems (NOT ob, NOT other dirs)
for s in ts teastore sockshop otel-demo hs media sn; do
  rm -rf aegislab/manifests/aegis-chaos/$s
  cp -r aegislab/manifests/aegis-chaos-merged/$s aegislab/manifests/aegis-chaos/$s
done

# 6. validate every YAML parses, commit ONLY aegislab/manifests/aegis-chaos/**
#    (scratch -observed/-merged are gitignored), open a PR. Don't touch the cluster.
```

After the catalog PR merges, the live byte-cluster needs a reconcile to pick up
the new points — that's a separate step (see `seed-update-cycle` + chaos reconcile).

## Traps

- **Raw, not converted.** `aegisctl blob cp datapack:<name>/` gives the RAW
  `normal_traces.parquet` (ClickHouse schema, `SpanAttributes`/`ResourceAttributes`
  as JSON) — that's what carries route/server/port/rpc/db. The sibling `converted/`
  subdir and any local `~/.aegisctl/...` copies are flattened and have dropped
  those attributes — useless for mining.
- **Server-attributed, no guessing.** Endpoints come from `SpanKind=Server` spans;
  topology edges from `parent_span_id -> parent's ServiceName` (real OTel
  `service.name`, no `clusterAppLabels` fuzzy-resolution). If a point targets a
  route the named service doesn't host, it's the old caller-attribution bug.
- **Infra (mysql/redis/rabbitmq) are leaves with no spans.** db target = the db
  client span's `server.address` (already the real workload name, e.g. `mysql`,
  `valkey-cart`). mq target = the broker **Service** name, resolved from the
  producer span's peer IP (a Service ClusterIP, 192.168.x.x) via the datapack's
  `k8s.json` — it is `rabbitmq`, NOT the non-existent `ts-rabbitmq` the old catalog
  used. Infra gets `network_*` only (no dns/pod).
- **jvm_mysql IS trace-derived** (from `db.name`/`db.sql.table`/`db.operation`).
  The agent wants a **bare table name** and **lowercase** sql_type (`select`), not
  the old `` `ts`.`route_distances` `` + `all`. `jvm_method_*` / `jvm_runtime_mutator`
  are bytecode-only — they are NOT in traces, so they're carried verbatim by
  `merge-catalog.sh` for services that still appear in observed.
- **byte-cluster runs bridgeless tproxy = inbound-only.** A caller-keyed
  `http_request_*` (downstream route on the calling pod) never fires there, so the
  default drops them; pass `--caller-request-points` to observedgen only for
  bridge-mode / non-IPVLAN clusters.
- **DSB/Thrift internals** (media/sn/hs/mm backends) speak `rpc.*`, not http —
  HTTPChaos on them is a silent no-op; their surface is gRPC + pod + network only.
- **Cold services are dropped on purpose.** A service absent from the normal window
  (e.g. media `page-service`, `ts-rebook-service`) gets no points — injecting on a
  service nothing calls produces no anomaly anyway. Re-mine if the loadgen changes
  to exercise it.
- **`min-count` drops the per-request long tail.** Concrete IDs that escape
  normalization survive as count-1 entries; the threshold (default 2) removes them.
  ts collapses ~44k raw http endpoints to ~200 real ones this way.
- **Layout change.** Output is one `<service>.yaml` per service (all families
  inline), replacing the old `*-{dns,http,network,jvm-mysql}-A1b.yaml` fan-out. The
  reconciler groups sibling files per service anyway, so this is equivalent.
- **ts loadgen split.** Post-June ts datapacks are the session-aware campaign;
  `--ts-before-epoch` selects the older traffic profile if you need it.
