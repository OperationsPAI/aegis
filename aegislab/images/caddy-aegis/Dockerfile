FROM caddy:2-builder AS builder

COPY caddy-aegis-auth/ /src/caddy-aegis-auth/

RUN xcaddy build \
    --with github.com/OperationsPAI/aegis/caddy-aegis-auth=/src/caddy-aegis-auth

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
