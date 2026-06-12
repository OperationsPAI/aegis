#!/usr/bin/env bash
# Merge the trace-mined "observed" catalog with the bytecode-derived
# jvm_method_* / jvm_runtime_mutator points from the current catalog into a
# complete, directly-replaceable catalog.
#
# observed already refreshes everything that is trace-derivable (pod/cpu/mem/
# time, http, grpc, dns, network incl. infra, jvm_mysql). jvm_method_* and
# jvm_runtime_mutator need bytecode analysis and are NOT in traces, so they are
# carried over verbatim — but only for services that observed still renders (a
# service dropped because it sees no normal traffic does not keep stale jvm
# points).
#
# Usage: merge-catalog.sh [observed_dir] [current_dir] [out_dir]
set -euo pipefail
OBS=${1:-aegislab/manifests/aegis-chaos-observed}
CUR=${2:-aegislab/manifests/aegis-chaos}
OUT=${3:-aegislab/manifests/aegis-chaos-merged}

rm -rf "$OUT"
cp -r "$OBS" "$OUT"

carried=0
for sysdir in "$OUT"/*/; do
  sys=$(basename "$sysdir")
  [ -d "$CUR/$sys" ] || continue
  shopt -s nullglob
  for f in "$CUR/$sys"/*-jvm-method-A1b.yaml "$CUR/$sys"/*-jvm-runtime-mutator-A1b.yaml; do
    base=$(basename "$f")
    svc=${base%-jvm-method-A1b.yaml}
    svc=${svc%-jvm-runtime-mutator-A1b.yaml}
    if [ -f "$OUT/$sys/$svc.yaml" ]; then          # observed renders this service
      cp "$f" "$OUT/$sys/"
      carried=$((carried + 1))
    fi
  done
  shopt -u nullglob
done
echo "merged -> $OUT (carried $carried jvm-method/runtime-mutator files)"
