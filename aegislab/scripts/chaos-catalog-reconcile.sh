#!/usr/bin/env bash
# chaos-catalog-reconcile.sh — make chaos_points catalog drift loud.
#
# The chaos_points table is fed ONLY by `aegisctl manifest reconcile-dir`
# run by hand. Backend startup and the seed-update cycle never touch it, and
# the manifests under aegislab/manifests/aegis-chaos are NOT shipped in the
# backend image — so a `just manifestgen` regen that nobody reconciled lets
# the live catalog silently fall behind the repo. PR #505 did exactly that:
# the repo manifests would produce 4471 active points but sockshop's DB had
# 525, with dns/network/jvm-runtime-mutator at 0 active across all systems.
#
# This script closes that gap two ways:
#   --check   (default) dry-run reconcile against the live aegis-chaos and
#             FAIL (exit 1) if any system's live active set differs from what
#             the committed manifests would produce. Drift can never again
#             pass unnoticed: wire this into CI-against-a-live-cluster and the
#             seed-update cycle.
#   --apply   run the real reconcile-dir (import + sweep), then re-check.
#
# USAGE
#   AEGIS_CHAOS_SERVER=http://localhost:8082 ./scripts/chaos-catalog-reconcile.sh [--check|--apply] [--system <sys>]
#
# REQUIREMENTS
#   - aegisctl on PATH (or AEGISCTL_BIN) pointed, via AEGIS_CHAOS_SERVER, at a
#     live aegis-chaos with a populated catalog.
#   - jq.
#
# EXIT
#   0  catalog matches the committed manifests (--check) or reconcile applied
#      cleanly and now matches (--apply).
#   1  drift detected (--check) or reconcile failed (--apply).
#   2  usage / environment error.
set -euo pipefail

MODE="check"
SYSTEM=""
AEGISCTL_BIN="${AEGISCTL_BIN:-aegisctl}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST_ROOT="${SCRIPT_DIR}/../manifests/aegis-chaos"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --check) MODE="check"; shift ;;
        --apply) MODE="apply"; shift ;;
        --system) SYSTEM="${2:?--system needs a value}"; shift 2 ;;
        --help|-h) sed -n '2,33p' "$0"; exit 0 ;;
        *) echo "error: unknown arg $1" >&2; exit 2 ;;
    esac
done

for bin in "$AEGISCTL_BIN" jq; do
    command -v "$bin" >/dev/null 2>&1 || { echo "error: $bin not found in PATH" >&2; exit 2; }
done
[[ -d "$MANIFEST_ROOT" ]] || { echo "error: manifest root $MANIFEST_ROOT not found" >&2; exit 2; }
[[ -n "${AEGIS_CHAOS_SERVER:-}" ]] || { echo "error: AEGIS_CHAOS_SERVER not set (e.g. http://localhost:8082)" >&2; exit 2; }

sys_flag=()
[[ -n "$SYSTEM" ]] && sys_flag=(--system "$SYSTEM")

if [[ "$MODE" == "apply" ]]; then
    echo "==> reconcile-dir (apply) ${MANIFEST_ROOT}"
    "$AEGISCTL_BIN" manifest reconcile-dir "$MANIFEST_ROOT" "${sys_flag[@]}"
    echo "==> re-checking for residual drift"
fi

# live active count per system from the catalog.
live_active() {
    "$AEGISCTL_BIN" chaos points list --system "$1" --status active --limit 1 -o json 2>/dev/null \
        | jq -r '.total // 0'
}

dry_json="$("$AEGISCTL_BIN" manifest reconcile-dir "$MANIFEST_ROOT" "${sys_flag[@]}" --dry-run -o json)"

drift=0
printf "%-14s %12s %12s %12s %s\n" SYSTEM MANIFEST_IDS LIVE_ACTIVE DEPRECATED STATUS
while IFS=$'\t' read -r sys manifest_ids deprecated; do
    [[ -z "$sys" ]] && continue
    live="$(live_active "$sys")"
    # A system is in sync iff the manifests produce exactly the live active
    # set: no points to remove (deprecated==0) and the same count.
    if [[ "$deprecated" -eq 0 && "$manifest_ids" -eq "$live" ]]; then
        status="ok"
    else
        status="DRIFT"
        drift=1
    fi
    printf "%-14s %12s %12s %12s %s\n" "$sys" "$manifest_ids" "$live" "$deprecated" "$status"
done < <(echo "$dry_json" | jq -r '.systems[] | [.system, (.active_ids|tostring), (.deprecated|tostring)] | @tsv')

if [[ "$drift" -ne 0 ]]; then
    echo
    echo "DRIFT: the live chaos_points catalog does not match the committed manifests." >&2
    if [[ "$MODE" == "apply" ]]; then
        echo "       reconcile-dir ran but a system still diverges — investigate import failures above." >&2
    else
        echo "       run: $(basename "$0") --apply   (against this same AEGIS_CHAOS_SERVER)" >&2
    fi
    exit 1
fi

echo
echo "OK: chaos_points catalog is in sync with the committed manifests."
