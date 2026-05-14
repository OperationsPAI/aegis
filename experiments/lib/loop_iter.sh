#!/bin/bash
# Autonomous loop iteration helper.
# Usage: loop_iter.sh <system> <round>
# Reaps trace terminals, writes per-trace last_event into a TSV.
# Caller (Claude) consumes the TSV to update candidate JSON + plan next round.
set -euo pipefail
SYS=$1
ROUND=$2
DIR="/home/ddq/AoyangSpace/aegis/experiments/${SYS}-loop"
RUNS="${DIR}/runs_round${ROUND}.jsonl"
OUT="${DIR}/terminals_round${ROUND}.tsv"
export PATH=/home/ddq/AoyangSpace/aegis/aegislab/bin:$PATH
: > "$OUT"
# extract unique trace_ids
jq -r '.trace_id // empty' "$RUNS" | sort -u | while read -r tid; do
  [ -z "$tid" ] && continue
  out=$(aegisctl trace get "$tid" 2>/dev/null || echo "")
  state=$(echo "$out" | grep -E "^state:" | awk '{print $2}')
  evt=$(echo "$out" | grep -E "^last_event:" | awk '{print $2}')
  echo -e "${tid}\t${state:-unknown}\t${evt:-unknown}" >> "$OUT"
done
echo "$OUT"
wc -l "$OUT"
