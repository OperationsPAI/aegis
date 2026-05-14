FROM golang:1.26-bookworm AS builder

ARG GO_BUILD_TAGS

ENV TZ=Asia/Shanghai
WORKDIR /app

RUN apt-get update && apt-get install -y g++ gcc

# chaos-experiment is a sibling repo referenced via `replace ../../chaos-experiment`
# from aegislab/src/go.mod. Build context must be the parent `aegis/` directory
# so both trees are available. Staged at /chaos-experiment to match ../../chaos-experiment
# when go resolves from /app (aegislab/src/).
COPY chaos-experiment /chaos-experiment

COPY aegislab/src/go.mod aegislab/src/go.sum ./
RUN go env -w "GOPROXY=https://goproxy.cn|https://proxy.golang.org|direct"
RUN go mod download

COPY aegislab/src/ .
RUN CGO_ENABLED=1 go build -tags="${GO_BUILD_TAGS}" -ldflags="-s -w" -o /app/exp
# sso is the standalone SSO/identity binary. Reuses the same build tag
# set as /app/exp so the dependency download cache layer above is shared.
RUN CGO_ENABLED=1 go build -tags="${GO_BUILD_TAGS}" -ldflags="-s -w" -o /app/sso ./cmd/aegis-sso

# Additional microservice binaries (notify, blob, gateway, configcenter)
RUN CGO_ENABLED=1 go build -tags="${GO_BUILD_TAGS}" -ldflags="-s -w" -o /app/aegis-notify ./cmd/aegis-notify
RUN CGO_ENABLED=1 go build -tags="${GO_BUILD_TAGS}" -ldflags="-s -w" -o /app/aegis-blob ./cmd/aegis-blob
RUN CGO_ENABLED=1 go build -tags="${GO_BUILD_TAGS}" -ldflags="-s -w" -o /app/aegis-gateway ./cmd/aegis-gateway
RUN CGO_ENABLED=1 go build -tags="${GO_BUILD_TAGS}" -ldflags="-s -w" -o /app/aegis-configcenter ./cmd/aegis-configcenter
FROM debian:bookworm AS runner

ARG INSTALL_CERTS=false
ARG APT_MIRROR=""

ENV TZ=Asia/Shanghai
WORKDIR /app

# ca-certificates is always needed: the backend pulls helm charts from public
# HTTPS repos at runtime and x509: certificate signed by unknown authority
# fails silently (surfaces only as RestartPedestal "no local fallback").
# INSTALL_CERTS=true additionally installs the internal harbor CA.
RUN set -eux; \
    if [ -n "$APT_MIRROR" ]; then \
        sed -i "s|deb.debian.org|${APT_MIRROR}|g" /etc/apt/sources.list.d/debian.sources; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates tzdata git; \
    if [ "$INSTALL_CERTS" = "true" ] && [ -f /usr/local/share/ca-certificates/harbor.lab.pj.crt ]; then \
        chmod 644 /usr/local/share/ca-certificates/harbor.lab.pj.crt; \
        update-ca-certificates; \
    fi; \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/exp /app/exp
COPY --from=builder /app/sso /app/sso
COPY --from=builder /app/aegis-notify /app/aegis-notify
COPY --from=builder /app/aegis-blob /app/aegis-blob
COPY --from=builder /app/aegis-gateway /app/aegis-gateway
COPY --from=builder /app/aegis-configcenter /app/aegis-configcenter
COPY aegislab/src/entrypoint.sh ./entrypoint.sh

RUN chmod +x ./entrypoint.sh
ENTRYPOINT ["./entrypoint.sh"]
