# Phase-windowed measurement — phases 5 and 6

The only honest way to measure chaos effect in a stream of continuous
traces is to partition spans by wall-clock window: `baseline`,
`inject`, `recovery`. Three rules:

1. Record timestamps in the same clock (UTC) you query against.
2. Leave a small gap between phases (~5s) — chaos apply and recover
   aren't instantaneous.
3. Query **caller-side** spans, not target-side, for network delays
   with `direction: to`. The target's own server spans barely move.

## Canonical shell skeleton

```bash
T_BASE_START=$(date -u +%Y-%m-%dT%H:%M:%S); sleep 45
T_BASE_END=$(date -u +%Y-%m-%dT%H:%M:%S)

kubectl apply -f networkchaos.yaml
sleep 6   # let chaos reconcile
T_INJ_START=$(date -u +%Y-%m-%dT%H:%M:%S); sleep 60
T_INJ_END=$(date -u +%Y-%m-%dT%H:%M:%S)

sleep 60  # wait out remaining chaos duration if any
kubectl delete networkchaos -n <ns> <name> --wait=false
sleep 15  # chaos recovery ≈ 5–10s; add a buffer
T_REC_START=$(date -u +%Y-%m-%dT%H:%M:%S); sleep 30
T_REC_END=$(date -u +%Y-%m-%dT%H:%M:%S)

echo "T_BASE_START=$T_BASE_START T_BASE_END=$T_BASE_END ..." > /tmp/windows.txt
```

## Canonical ClickHouse query

Fill in the target ServiceName pattern. The query partitions with
`multiIf`, and excludes the between-phase gaps with the `0-other`
sentinel:

```sql
SELECT phase,
       count()                               AS spans,
       round(avg(Duration/1e6),3)            AS avg_ms,
       round(quantile(0.5)(Duration/1e6),3)  AS p50_ms,
       round(quantile(0.95)(Duration/1e6),3) AS p95_ms,
       round(max(Duration/1e6),3)            AS max_ms
FROM (
  SELECT Duration,
    multiIf(
      Timestamp BETWEEN toDateTime64('${T_BASE_START}', 3)
                    AND toDateTime64('${T_BASE_END}',   3), '1-baseline',
      Timestamp BETWEEN toDateTime64('${T_INJ_START}',  3)
                    AND toDateTime64('${T_INJ_END}',    3), '2-inject',
      Timestamp BETWEEN toDateTime64('${T_REC_START}',  3)
                    AND toDateTime64('${T_REC_END}',    3), '3-recovery',
      '0-other'
    ) AS phase
  FROM otel.otel_traces
  WHERE SpanKind='Client'                           -- caller-side
    AND SpanName LIKE '%<TargetService>%'           -- e.g. CurrencyService
)
WHERE phase != '0-other'
GROUP BY phase
ORDER BY phase
FORMAT PrettyCompact;
```

Write it to a file and pass via `--queries-file`; do not try to inline
a multi-line SQL with timestamps through shell quoting. Every attempt
to do so has failed:

```bash
cat > /tmp/q.sql <<EOF
<SQL with ${VAR} interpolation>
EOF
kubectl -n otel cp /tmp/q.sql clickhouse-0:/tmp/q.sql
kubectl -n otel exec clickhouse-0 -- \
  clickhouse-client --password clickhouse --queries-file /tmp/q.sql
```

## Reading the result

A clean, well-defined injection looks like this:

```
┌─phase──────┬─spans─┬──avg_ms─┬──p50_ms─┬──p95_ms─┐
│ 1-baseline │   ~N  │    tiny │    tiny │  small  │
│ 2-inject   │   ~N  │ ~delay  │ ~delay  │ ~delay  │
│ 3-recovery │   ~N  │    tiny │    tiny │  small  │
└────────────┴───────┴─────────┴─────────┴─────────┘
```

The `2-inject` row should show the latency parameter you configured
almost exactly on p50. If p50 is way bigger than the delay, you're
looking at fan-out amplification (aggregate trace covering many
children). If p50 is barely moved, you're probably looking at
target-side server spans — re-query with `SpanKind='Client'` and a
name filter on the target.

## Failure modes

| Symptom | Likely cause |
|---|---|
| `0-other` has most of the spans | Windows overlap the boundaries; widen baseline/recovery sleeps. |
| `2-inject` count is low | Load generator not running, or chaos tore down too many calls (try a smaller latency). |
| p50 barely changed | You queried server spans, not client spans. |
| Wildly different means with stable p50 | A few outlier connection resets; report p50 as the signal. |
