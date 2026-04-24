#!/usr/bin/env bash
# Cut a release for one or more of the aegis monorepo's publishable packages.
#
# Packages:
#   platform          → PyPI  rcabench-platform          tag: release-platform/v<ver>
#   sdk               → PyPI  rcabench                   tag: release-sdk/v<ver>
#   portal            → NPM   @OperationsPAI/portal      tag: release-ts-portal/v<ver>
#   admin             → NPM   @OperationsPAI/admin       tag: release-ts-admin/v<ver>
#   backend-image     → Docker opspai/rcabench           tag: release-backend/v<ver>
#   frontend-image    → Docker opspai/rcabench-frontend  tag: release-frontend/v<ver>
#   backend-chart     → Helm chart rcabench              tag: release-helm-backend/v<ver>
#   frontend-chart    → Helm chart AegisLab-frontend     tag: release-helm-frontend/v<ver>
#
# Usage:
#   scripts/release.sh <pkg>=<version> [<pkg>=<version> ...]
#
# Examples:
#   scripts/release.sh platform=0.5.0
#   scripts/release.sh sdk=1.3.0 portal=1.3.0 admin=1.3.0
#   scripts/release.sh backend-image=1.0.0 backend-chart=1.0.0
#
# What it does:
#   1. Asserts clean working tree on main.
#   2. For `platform`: checks rcabench-platform/pyproject.toml version matches
#      (other packages have version baked in at CI-build time, so no pre-bump).
#   3. Creates all tags locally, then pushes them in a single `git push` so
#      the GitHub workflows fire concurrently when releasing multiple pkgs.
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }

[[ $# -ge 1 ]] || die "usage: $0 <pkg>=<version> [<pkg>=<version> ...]"

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

[[ -z "$(git status --porcelain)" ]] || die "working tree not clean"
branch=$(git rev-parse --abbrev-ref HEAD)
[[ "$branch" == "main" ]] || die "must be on main (currently: $branch)"

tag_prefix_for() {
  case "$1" in
    platform)        echo "release-platform" ;;
    sdk)             echo "release-sdk" ;;
    portal)          echo "release-ts-portal" ;;
    admin)           echo "release-ts-admin" ;;
    backend-image)   echo "release-backend" ;;
    frontend-image)  echo "release-frontend" ;;
    backend-chart)   echo "release-helm-backend" ;;
    frontend-chart)  echo "release-helm-frontend" ;;
    *) die "unknown package: $1" ;;
  esac
}

chart_version_of() {
  # Read `version:` line from Chart.yaml (top-level only).
  sed -n 's/^version:[[:space:]]*\(.*\)$/\1/p' "$1" | head -n1 | tr -d '\r'
}

preflight() {
  local pkg=$1 ver=$2
  case "$pkg" in
    platform)
      local py
      py=$(sed -n 's/^version = "\([^"]*\)"/\1/p' rcabench-platform/pyproject.toml | head -n1)
      [[ "$py" == "$ver" ]] \
        || die "rcabench-platform/pyproject.toml version is '$py', expected '$ver'. Bump it and commit first."
      ;;
    sdk|portal|admin) : ;;  # version injected by CI generator
    backend-image|frontend-image) : ;;  # version comes straight from the tag
    backend-chart)
      local cv
      cv=$(chart_version_of AegisLab/helm/Chart.yaml)
      [[ "$cv" == "$ver" ]] \
        || echo "warn: AegisLab/helm/Chart.yaml version is '$cv', releasing as '$ver' (CI will override --version)." >&2
      ;;
    frontend-chart)
      local cv
      cv=$(chart_version_of AegisLab-frontend/helm/Chart.yaml)
      [[ "$cv" == "$ver" ]] \
        || echo "warn: AegisLab-frontend/helm/Chart.yaml version is '$cv', releasing as '$ver' (CI will override --version)." >&2
      ;;
  esac
}

tags=()
for arg in "$@"; do
  [[ "$arg" == *=* ]] || die "expected <pkg>=<version>, got: $arg"
  pkg="${arg%%=*}"
  ver="${arg#*=}"
  [[ -n "$pkg" && -n "$ver" ]] || die "bad arg: $arg"
  prefix=$(tag_prefix_for "$pkg")
  preflight "$pkg" "$ver"
  tag="$prefix/v$ver"
  if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    die "tag $tag already exists"
  fi
  tags+=("$tag")
done

for t in "${tags[@]}"; do
  git tag -a "$t" -m "$t"
  echo "created tag: $t"
done

git push origin "${tags[@]}"
echo "pushed: ${tags[*]}"
