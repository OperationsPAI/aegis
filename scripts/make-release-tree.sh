#!/usr/bin/env bash
# Materialise the public-release view of this repo into a directory.
#
#   scripts/make-release-tree.sh <output-dir> [git-ref]
#
# The output is a plain directory (no .git). Push it to the public repo with
# scripts/push-release.sh, or inspect it first.
set -euo pipefail

OUT=${1:?usage: make-release-tree.sh <output-dir> [git-ref]}
REF=${2:-HEAD}
ROOT=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)

rm -rf "$OUT"
mkdir -p "$OUT"
git -C "$ROOT" archive "$REF" | tar -x -C "$OUT"

cd "$OUT"

# Agent tooling and recovered-requirements index — internal working material.
rm -rf .claude .codex .github/workbuddy aegislab/.github/workbuddy
rm -f project-index.yaml aegislab/project-index.yaml

# Fault-injection campaign artifacts — thousands of run-specific files.
rm -rf experiments

# The live byte-cluster: EIPs, private mirror pins, real operator accounts.
rm -rf aegislab/manifests/byte-cluster aegislab/manifests/cn_mirror

# Env overlays that manifests/README.md itself marks legacy/unverified and
# wires to internal registries (pair-diag-cn-guangzhou, 10.10.10.240).
rm -rf aegislab/manifests/prod aegislab/manifests/test aegislab/manifests/staging
rm -rf aegislab/data/initial_data/staging

# LEGACY.md notes describe which internal environment a file targets.
find . -name LEGACY.md -delete

# Internal mirror -> the docker.io namespace it mirrors. Test fixtures keep
# the original strings; they assert on registry-rewriting behaviour.
grep -rIl 'volces\.com' . --include='*.yaml' --include='*.go' --include='*.md' \
  | grep -v '_test\.go' \
  | xargs -r sed -i \
      -e 's|pair-cn-shanghai\.cr\.volces\.com/opspai|opspai|g' \
      -e 's|pair-diag-cn-guangzhou\.cr\.volces\.com/pair/rcabench|opspai/rcabench|g'

echo "release tree at $OUT ($(find . -type f | wc -l) files)"
