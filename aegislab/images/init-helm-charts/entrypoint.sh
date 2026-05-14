#!/usr/bin/env bash
set -euo pipefail

CONFIG="${HELM_CHARTS_CONFIG:-/app/config.json}"
TARGET="${HELM_CHARTS_TARGET_PATH:-/var/lib/rcabench/dataset/helm-charts}"

mkdir -p "$TARGET"
[ -s "$CONFIG" ] || { echo "no system_helm_configs.json at $CONFIG; nothing to seed"; exit 0; }

registry_for() {
  case "$1" in
    oci://*)
      echo "${1%/}/$2"
      ;;
    *)
      echo ""
      ;;
  esac
}

# DB lookup is OPTIONAL; we skip MySQL if not reachable.
mysql_exec() {
  if [ -z "${MYSQL_HOST:-}" ]; then return 1; fi
  MYSQL_PWD="${MYSQL_PASSWORD:-}" mysql -h "$MYSQL_HOST" -P "${MYSQL_PORT:-3306}" \
    -u "${MYSQL_USER:-root}" "${MYSQL_DATABASE:-rcabench}" -N -B -e "$1" 2>/dev/null
}

jq -c '.[]' "$CONFIG" | while read -r row; do
  chart=$(jq -r '.chart_name' <<<"$row")
  version=$(jq -r '.version' <<<"$row")
  cv_id=$(jq -r '.container_version_id' <<<"$row")
  system=$(jq -r '.system' <<<"$row")

  # Preferred: read repo_url from helm_configs for this container_version_id.
  repo=$(mysql_exec "SELECT repo_url FROM helm_configs WHERE container_version_id=$cv_id LIMIT 1;" || true)
  repo="${repo%$'\n'}"

  if [ -z "$repo" ]; then
    # Fallback: assume OCI Docker Hub under opspai (matches default seed).
    repo="oci://registry-1.docker.io/opspai"
    echo "[$system] no repo_url in DB for cv=$cv_id; fallback $repo"
  fi

  target_tgz="$TARGET/${chart}-${version}.tgz"
  if [ -s "$target_tgz" ]; then
    echo "[$system] $chart@$version already cached at $target_tgz"
    continue
  fi

  case "$repo" in
    oci://*)
      src="${repo%/}/${chart}"
      echo "[$system] helm pull $src --version $version -> $TARGET"
      helm pull "$src" --version "$version" --destination "$TARGET" || {
        echo "[$system] helm pull failed; leaving chart uncached (backend will fall through to remote)"
        continue
      }
      ;;
    *)
      echo "[$system] helm repo add tmp $repo && helm pull tmp/$chart --version $version"
      helm repo add --force-update tmp "$repo" >/dev/null 2>&1 || true
      helm repo update >/dev/null 2>&1 || true
      helm pull "tmp/$chart" --version "$version" --destination "$TARGET" || {
        echo "[$system] helm pull failed; leaving uncached"
        continue
      }
      ;;
  esac

  # Update helm_configs.local_path so backend prefers the cached tgz.
  if [ -n "${MYSQL_HOST:-}" ]; then
    downloaded=$(ls -1t "$TARGET"/${chart}-${version}*.tgz 2>/dev/null | head -1 || true)
    if [ -n "$downloaded" ]; then
      mysql_exec "UPDATE helm_configs SET local_path='$downloaded' WHERE container_version_id=$cv_id;" >/dev/null || true
      echo "[$system] cached at $downloaded; helm_configs.local_path updated"
    fi
  fi
done

echo "init-helm-charts: done"
