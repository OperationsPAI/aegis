#!/usr/bin/env bash
# relayout.sh — mechanical package-move helper for aegislab repo relayout.
#
# NOTE: GNU sed is assumed (Linux). macOS requires `brew install gnu-sed` and
# PATH adjustment; the -i flag behaves differently on BSD sed.
#
# Usage:
#   tools/relayout.sh move <from-pkg-path> <to-pkg-path>
#   tools/relayout.sh apply <moves.tsv>    # TSV: <from>\t<to>\n per line
#   tools/relayout.sh verify               # run gate sequence only
#
# <from-pkg-path> and <to-pkg-path> are relative to src/ (e.g. module/foo infra/foo).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

log() { echo >&2 "[relayout] $*"; }
die() { echo >&2 "[relayout] ERROR: $*"; exit 1; }

# ---------------------------------------------------------------------------
# move_one <from> <to>
#   Moves src/<from> to src/<to>, rewrites all import paths, runs goimports.
# ---------------------------------------------------------------------------
move_one() {
    local from="$1" to="$2"
    local from_dir="${SRC_DIR}/${from}"
    local to_dir="${SRC_DIR}/${to}"

    [[ -e "${from_dir}" ]] || die "source does not exist: src/${from}"
    [[ -e "${to_dir}" ]]   && die "destination already exists: src/${to}"

    log "move: src/${from} -> src/${to}"

    # 1. git mv (creates parent dirs automatically via git)
    mkdir -p "$(dirname "${to_dir}")"
    git mv "${from_dir}" "${to_dir}"
    log "  git mv done"

    # 2. Rewrite import paths across all .go files.
    #    Two patterns:
    #      "aegis/<from>"   -> "aegis/<to>"      (exact package)
    #      "aegis/<from>/   -> "aegis/<to>/       (sub-package)
    log "  rewriting imports ..."
    find "${SRC_DIR}" -name '*.go' -exec sed -i \
        -e "s|\"aegis/${from}\"|\"aegis/${to}\"|g" \
        -e "s|\"aegis/${from}/|\"aegis/${to}/|g" \
        {} +
    log "  imports rewritten"

    # 3. goimports (best-effort; skip if not on PATH)
    if command -v goimports >/dev/null 2>&1; then
        log "  running goimports ..."
        goimports -w "${SRC_DIR}"
    else
        log "  goimports not found, skipping (run manually: go run golang.org/x/tools/cmd/goimports@latest -w src/)"
    fi
}

# ---------------------------------------------------------------------------
# run_gates
#   Runs the standard gate sequence that every relayout step must pass.
# ---------------------------------------------------------------------------
run_gates() {
    log "=== gates ==="

    cd "${SRC_DIR}"

    log "  go generate ./..."
    go generate ./...

    if command -v swag >/dev/null 2>&1; then
        log "  swag init ..."
        swag init --parseDependency --parseDepth 1 -o ./docs || true
    else
        log "  swag not on PATH, skipping swagger regen"
    fi

    log "  go build -tags duckdb_arrow ./..."
    go build -tags duckdb_arrow ./...

    if command -v golangci-lint >/dev/null 2>&1; then
        log "  golangci-lint run ..."
        golangci-lint run
    else
        log "  golangci-lint not on PATH, skipping"
    fi

    log "=== gates passed ==="
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
cmd="${1:-}"
shift || true

case "${cmd}" in
move)
    [[ $# -eq 2 ]] || die "usage: relayout.sh move <from> <to>"
    move_one "$1" "$2"
    run_gates
    ;;

apply)
    [[ $# -eq 1 ]] || die "usage: relayout.sh apply <moves.tsv>"
    tsv="$1"
    [[ -f "${tsv}" ]] || die "TSV file not found: ${tsv}"
    while IFS=$'\t' read -r from to; do
        # skip blank lines and comment lines
        [[ -z "${from}" || "${from}" == \#* ]] && continue
        move_one "${from}" "${to}"
    done < "${tsv}"
    run_gates
    ;;

verify)
    run_gates
    ;;

*)
    die "unknown command '${cmd}'. Use: move | apply | verify"
    ;;
esac
