#!/bin/bash
# Idempotency is delegated to `aegisctl manifest import`, which UPSERTs
# by point_id hash on the chaos-service side.
set -e
COUNT_OK=0
COUNT_FAIL=0
for f in $(find aegislab/manifests/aegis-chaos -name '*-A1b.yaml' | sort); do
  if AEGIS_CHAOS_SERVER="${AEGIS_CHAOS_SERVER:?must be set}" aegisctl manifest import "$f"; then
    COUNT_OK=$((COUNT_OK+1))
  else
    COUNT_FAIL=$((COUNT_FAIL+1))
    echo "FAIL: $f" >&2
  fi
done
echo "Imported $COUNT_OK; failed $COUNT_FAIL"
