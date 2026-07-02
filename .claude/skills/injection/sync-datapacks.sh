#!/usr/bin/env bash
# sync-datapacks.sh — pull all pair_diagnosis datapacks to a local directory.
#
# First run: downloads everything (5k+ datapacks, takes a while).
# Subsequent runs (cron): incremental — only downloads datapacks whose
# directory doesn't already exist locally. Idempotent and safe to Ctrl-C
# (partially downloaded datapacks are detected and re-downloaded next run).
#
# Usage:
#   sync-datapacks.sh [--dest /path/to/dir] [--project pair_diagnosis] [--parallel N]
#
# Cron example (every 30min):
#   */30 * * * * /home/ddq/aegis-campaign/.claude/skills/injection/sync-datapacks.sh >> /tmp/sync-datapacks.log 2>&1
#
# Disable: touch /tmp/sync-datapacks.disabled
set -euo pipefail

DEST="/mnt/jfs/aegis_datasets"
PROJECT="pair_diagnosis"
PARALLEL=4
AEGISCTL="/home/ddq/.local/bin/aegisctl"
export AEGIS_INSECURE_SKIP_VERIFY=1
export PATH="/home/ddq/.local/bin:/usr/local/bin:/usr/bin:/bin:${PATH:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dest)     DEST="$2"; shift 2 ;;
    --project)  PROJECT="$2"; shift 2 ;;
    --parallel) PARALLEL="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

[ -f /tmp/sync-datapacks.disabled ] && exit 0

mkdir -p "$DEST"

ts() { date '+%F %T'; }

echo "[$(ts)] sync-datapacks: project=$PROJECT dest=$DEST"

# Fetch all detector_success inject names (the ones with usable datapacks).
NAMES=$("$AEGISCTL" inject list --project "$PROJECT" --state detector_success --all -o ndjson 2>/dev/null \
  | python3 -c "import json,sys; [print(json.loads(l)['name']) for l in sys.stdin if json.loads(l).get('name')]" 2>/dev/null)

TOTAL=$(echo "$NAMES" | grep -c . || true)
echo "[$(ts)] found $TOTAL datapacks in project $PROJECT"

# Filter to only those not yet downloaded (incremental).
# A datapack is "done" if its local dir exists AND contains a .valid marker
# (the build writes .valid as the last file; if missing the download was partial).
NEW=0
DOWNLOAD_LIST=$(mktemp)
while IFS= read -r name; do
  [ -z "$name" ] && continue
  if [ -d "$DEST/$name" ] && [ -f "$DEST/$name/.valid" ]; then
    continue
  fi
  echo "$name" >> "$DOWNLOAD_LIST"
  NEW=$((NEW + 1))
done <<< "$NAMES"

if [ "$NEW" -eq 0 ]; then
  echo "[$(ts)] all $TOTAL datapacks already synced, nothing to do"
  rm -f "$DOWNLOAD_LIST"
  exit 0
fi

echo "[$(ts)] downloading $NEW new datapacks ($PARALLEL parallel)..."

download_one() {
  local name="$1"
  local dest_dir="${SYNC_DEST}/${name}"
  mkdir -p "$dest_dir"
  if "${SYNC_AEGISCTL}" blob cp -r "datapack:${name}/" "$dest_dir/" >/dev/null 2>&1; then
    echo "[$(date '+%F %T')] OK $name ($(find "$dest_dir" -type f | wc -l) files)"
  else
    echo "[$(date '+%F %T')] FAIL $name" >&2
    rm -rf "$dest_dir"
  fi
}
export -f download_one
export SYNC_DEST="$DEST" SYNC_AEGISCTL="$AEGISCTL" AEGIS_INSECURE_SKIP_VERIFY PATH

xargs -P "$PARALLEL" -I{} bash -c 'download_one "$@"' _ {} < "$DOWNLOAD_LIST"

rm -f "$DOWNLOAD_LIST"

DONE=$(find "$DEST" -maxdepth 2 -name ".valid" | wc -l)
echo "[$(ts)] sync complete: $DONE/$TOTAL datapacks on disk"
