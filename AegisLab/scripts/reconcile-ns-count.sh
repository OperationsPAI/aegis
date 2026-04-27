#!/usr/bin/env bash
# reconcile-ns-count.sh — operator-triggered hot-fix for issue #227.
#
# The auto-allocator (#166) walks the per-system namespace pool 0..count-1
# and skips slots whose namespace was deleted (probe returns "no workload").
# Until the Pass 1.5 hole-fill fix lands and propagates, every campaign
# round that deletes namespaces leaks one count slot. After 50+ rounds the
# count drifts far above what's actually live and eventually trips the
# dynamic_configs.max_value ceiling.
#
# This script reads live <sys>* namespaces from kubectl, computes the
# minimal count that still covers them (max(idx)+1), and rewrites
# /rcabench/config/global/injection.system.<sys>.count in etcd. Slots are
# NOT actually freed — bootstrap still extends the pool — but the count
# stops climbing past the live high-water mark, which is enough to keep a
# running campaign from hitting max_value.
#
# USAGE
#   ./scripts/reconcile-ns-count.sh [--dry-run] sys1 sys2 ...
#   ./scripts/reconcile-ns-count.sh --dry-run hs sn mm teastore
#
# REQUIREMENTS
#   - kubectl pointed at the target cluster (used to enumerate <sys><N> ns)
#   - etcdctl + ETCDCTL_ENDPOINTS exported pointing at the aegis etcd cluster
#     (aegisctl currently has no `chaos-system update count` subcommand —
#      see memory note feedback_aegisctl_ownership; we go straight to etcd)
#   - jq (for parsing etcd JSON values)
#
# SAFETY
#   - --dry-run prints the target counts and the raw etcd put commands but
#     writes nothing.
#   - Without --dry-run, rewrites only the `count` field of the etcd value
#     at /rcabench/config/global/injection.system.<sys>.count.
#   - Bumps count UP when the live max exceeds it; refuses to silently shrink
#     when the live max is lower (operator must --force-shrink to confirm —
#     the backend's allocator may be holding locks at higher indices that we
#     can't see from kubectl).
#
# REFERENCE: issue #227, memory note project_issue166_plan,
# feedback_aegisctl_ownership.

set -euo pipefail

DRY_RUN=0
FORCE_SHRINK=0
SYSTEMS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=1; shift ;;
        --force-shrink) FORCE_SHRINK=1; shift ;;
        --help|-h)
            sed -n '2,40p' "$0"
            exit 0
            ;;
        *) SYSTEMS+=("$1"); shift ;;
    esac
done

if [[ ${#SYSTEMS[@]} -eq 0 ]]; then
    echo "error: no systems specified" >&2
    echo "usage: $0 [--dry-run] [--force-shrink] sys1 sys2 ..." >&2
    exit 2
fi

for bin in kubectl etcdctl jq; do
    if ! command -v "$bin" >/dev/null 2>&1; then
        echo "error: $bin not found in PATH" >&2
        exit 2
    fi
done

if [[ -z "${ETCDCTL_ENDPOINTS:-}" ]]; then
    echo "error: ETCDCTL_ENDPOINTS not exported (e.g. export ETCDCTL_ENDPOINTS=http://etcd:2379)" >&2
    exit 2
fi

ETCD_KEY_PREFIX="/rcabench/config/global/injection.system."

# extract the highest <sys><N> index in the cluster (-1 if none live).
highest_live_index() {
    local sys="$1"
    kubectl get ns -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' \
        | grep -E "^${sys}[0-9]+$" \
        | sed -E "s/^${sys}([0-9]+)$/\1/" \
        | sort -n \
        | tail -1 \
        || true
}

# read current count value JSON from etcd. The dynamic_config payload is a
# JSON blob with the field `default_value` (string) holding the count.
read_count_value() {
    local sys="$1"
    etcdctl get --print-value-only "${ETCD_KEY_PREFIX}${sys}.count" 2>/dev/null || true
}

# rewrite the JSON blob preserving every field except default_value.
patch_count_value() {
    local raw="$1" newcount="$2"
    echo "$raw" | jq --arg c "$newcount" '.default_value = $c'
}

exit_code=0
for sys in "${SYSTEMS[@]}"; do
    high="$(highest_live_index "$sys" || true)"
    if [[ -z "$high" ]]; then
        target=0
    else
        target=$((high + 1))
    fi

    raw="$(read_count_value "$sys")"
    if [[ -z "$raw" ]]; then
        echo "warn: system ${sys} has no count key in etcd at ${ETCD_KEY_PREFIX}${sys}.count; skipping" >&2
        continue
    fi

    cur="$(echo "$raw" | jq -r '.default_value // empty')"
    if [[ -z "$cur" ]]; then
        echo "warn: system ${sys} count value has no default_value field; skipping (raw=$raw)" >&2
        continue
    fi

    if [[ "$cur" -eq "$target" ]]; then
        printf "%-12s count=%-4s OK (live max=%s)\n" "$sys" "$cur" "${high:-none}"
        continue
    fi

    if [[ "$cur" -lt "$target" ]]; then
        printf "%-12s count=%-4s -> %-4s (live max=%s; BEHIND reality, will bump up)\n" \
            "$sys" "$cur" "$target" "${high:-none}"
    else
        printf "%-12s count=%-4s -> %-4s (live max=%s; AHEAD of reality, would shrink)\n" \
            "$sys" "$cur" "$target" "${high:-none}"
        if [[ "$FORCE_SHRINK" -ne 1 ]]; then
            echo "  refusing to shrink without --force-shrink (backend may hold locks at higher indices)" >&2
            continue
        fi
    fi

    new_raw="$(patch_count_value "$raw" "$target")"
    printf "  etcdctl put %s%s.count <patched-json>\n" "$ETCD_KEY_PREFIX" "$sys"

    if [[ "$DRY_RUN" -eq 1 ]]; then
        continue
    fi

    if ! etcdctl put "${ETCD_KEY_PREFIX}${sys}.count" "$new_raw" >/dev/null; then
        echo "error: etcdctl put failed for ${sys}" >&2
        exit_code=1
    fi
done

if [[ "$DRY_RUN" -eq 1 ]]; then
    echo
    echo "(dry-run: re-run without --dry-run to apply; backend will pick up changes via its etcd watcher)"
fi

exit "$exit_code"
