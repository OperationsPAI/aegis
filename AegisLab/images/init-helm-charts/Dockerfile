# opspai/init-helm-charts
#
# Helm-chart seed image used by the rcabench Helm release as a post-install Job
# (see AegisLab/helm/templates/init-job.yaml). Reads system_helm_configs.json
# mounted at $HELM_CHARTS_CONFIG, optionally consults MySQL helm_configs for
# repo_url overrides, `helm pull`s each chart to $HELM_CHARTS_TARGET_PATH, and
# (if MySQL is reachable) writes helm_configs.local_path back so the backend
# serves cached tgz instead of re-pulling.
#
# Reconstructed from docker history of opspai/init-helm-charts:cold-start-20260422
# on 2026-04-23 so the build is reproducible from source.
#
# Build:
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     -t opspai/init-helm-charts:<tag> --push .
FROM alpine:3.20.10

RUN apk add --no-cache bash curl jq ca-certificates mysql-client

ARG HELM_VERSION=3.18.4
RUN curl -fsSL https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz \
      | tar -xz -C /tmp \
 && mv /tmp/linux-amd64/helm /usr/local/bin/helm \
 && rm -rf /tmp/linux-amd64 \
 && helm version --short

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENV HELM_CHARTS_CONFIG=/app/config.json \
    HELM_CHARTS_TARGET_PATH=/var/lib/rcabench/dataset/helm-charts

WORKDIR /app
ENTRYPOINT ["/entrypoint.sh"]
