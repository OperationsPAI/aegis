#!/usr/bin/env bash
# Tear down all sockshop deployments + clear runtime locks so the system
# can be re-validated from a clean slate. There's no built-in aegisctl
# subcommand for system-wide teardown today (see issue ?).
#
# Usage: tools/wipe_sockshop.sh [<min-N>] [<max-N>]
#   defaults: 0..9
set -euo pipefail
MIN=${1:-0}; MAX=${2:-9}

echo "=== uninstall helm releases ==="
for n in $(seq "$MIN" "$MAX"); do
  ns="sockshop$n"
  helm -n "$ns" uninstall "$ns" --wait=false 2>&1 | grep -E "uninstalled|Error|already" | head -1 &
done
wait

echo "=== delete namespaces (non-blocking) ==="
for n in $(seq "$MIN" "$MAX"); do
  kubectl delete ns "sockshop$n" --wait=false --ignore-not-found 2>&1 &
done
wait

echo "=== clear redis monitor:ns:sockshop* locks ==="
keys=$(kubectl -n exp exec rcabench-redis-0 -- redis-cli KEYS 'monitor:ns:sockshop*' 2>/dev/null | tr '\r' ' ')
if [[ -n "$keys" ]]; then
  for k in $keys; do
    kubectl -n exp exec rcabench-redis-0 -- redis-cli DEL "$k" >/dev/null
    echo "deleted $k"
  done
fi

echo "=== wait for ns terminating to clear ==="
until [[ -z "$(kubectl get ns 2>&1 | grep -E 'sockshop[0-9]+' | head)" ]]; do
  remaining=$(kubectl get ns 2>&1 | grep -E 'sockshop[0-9]+' | wc -l)
  echo "still terminating: $remaining ns"
  sleep 5
done
echo "=== all sockshop ns gone ==="
