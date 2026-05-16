# Edge-proxy host aliasing

How to expose the cluster's edge proxy under additional external addresses
(extra EIPs, accelerator endpoints, regional forwarders) without restarting
the proxy pod or breaking the existing EIP.

## Background

`rcabench-edge-proxy` is a Caddy v2 deployment that fronts every public
endpoint: SSO, blob presign passthrough, `aegis-gateway`, `/s/*` share
redirects, the SPA, and the gRPC intake port. The Caddyfile lives in
`helm/templates/edge-proxy-config.yaml` and is mounted into the pod as
`/etc/caddy/Caddyfile`.

The default chart pins the public hostname to a single EIP, e.g.

```caddy
118.196.98.178:8082 {
    tls internal
    ...
}
```

Caddy matches incoming requests by **Host header hostname** against the
site addresses. Anything else falls through to an implicit empty site
and returns `200 content-length: 0` — the classic "site looks
half-broken" symptom.

## When you need this

You added a new L4 forwarder, a CDN, a Volcengine Global Accelerator, or
a secondary EIP that maps to the same pod port (8082). Clients now hit

```
https://<new-host>:<new-port>/  →  <forwarder>  →  pod :8082
```

The TCP packets land on the same Caddy listener (`:8082`), but the
client's `Host` header is `<new-host>:<new-port>` — which the existing
single-host site block does not accept, so Caddy returns the empty 200.

## The fix

Add the new hostname as an alias on the **same site block**, pinned to
the **listener port the pod actually exposes (8082)** — not the
externally-visible port the client typed.

```diff
- 118.196.98.178:8082 {
+ 118.196.98.178:8082, 23.189.248.55:8082 {
      tls internal
      ...
  }
```

Why `:8082` and not the external port (e.g. `:38082`)?

- Caddy's site address `host:port` syntax means "**listen** on `port`,
  match `host`". The port the client typed isn't relevant — the L4
  forwarder rewrites the destination port before Caddy ever sees the
  packet.
- Writing `23.189.248.55:38082` makes Caddy spin up a new listener on
  38082 inside the pod. That port isn't exposed by the Service, so
  traffic still arrives on 8082 and still misses the alias.
- When matching, Caddy strips the port from the `Host` header before
  comparing — so a client sending `Host: 23.189.248.55:38082` matches a
  site declared as `23.189.248.55:8082` (or any port) as long as the
  hostname agrees.

Side effects:

- `tls internal` automatically extends the SAN list to cover the new
  hostname (visible in pod logs: `"obtaining certificate" identifier=
  "23.189.248.55"`).
- All existing handlers (SSO, rustfs, gateway, frontend, dev intercept)
  apply unchanged — the alias shares one site block.

## Applying without restarting the pod

The proxy runs with `admin off` (no admin API), so `caddy reload` over
HTTP is unavailable. Caddy v2 still honours `SIGUSR1` for on-disk reload,
so the rollout is:

```bash
# 1. Patch the ConfigMap (helm upgrade, kubectl apply, or kubectl create
#    --from-file=… | kubectl apply -f -). The chart's source of truth is
#    helm/templates/edge-proxy-config.yaml.
kubectl -n exp get cm rcabench-edge-proxy-config -o jsonpath='{.data.Caddyfile}' > /tmp/Caddyfile
$EDITOR /tmp/Caddyfile
kubectl -n exp create cm rcabench-edge-proxy-config \
    --from-file=Caddyfile=/tmp/Caddyfile --dry-run=client -o yaml \
  | kubectl apply -f -

# 2. Wait for kubelet to project the updated key into the pod
#    (10-60s; the mounted file is a symlinked atomic swap).
POD=$(kubectl -n exp get pod -l app.kubernetes.io/component=edge-proxy -o name | head -1)
until kubectl -n exp exec "$POD" -- grep -q '<new-host>' /etc/caddy/Caddyfile; do
    sleep 2
done

# 3. Validate inside the pod before reloading — a typo here would crash
#    the running Caddy on SIGUSR1.
kubectl -n exp exec "$POD" -- caddy validate \
    --config /etc/caddy/Caddyfile --adapter caddyfile

# 4. Reload. Caddy logs "successfully reloaded config from file" with
#    "signal":"SIGUSR1" on success. On error it keeps the previous
#    config running and logs the failure.
kubectl -n exp exec "$POD" -- kill -USR1 1
kubectl -n exp logs "$POD" --tail=20
```

The reload is graceful: in-flight connections drain on the old listeners,
new connections take the new config, no 5xx burst.

## Verification

Both addresses should produce identical responses for the same path:

```
$ curl -sk -o /dev/null -w 'code=%{http_code} size=%{size_download}\n' \
    https://118.196.98.178:8082/api/v2/health
code=401 size=13

$ curl -sk -o /dev/null -w 'code=%{http_code} size=%{size_download}\n' \
    https://23.189.248.55:38082/api/v2/health
code=401 size=13
```

The pre-fix symptom — `200 content-length: 0` on the new host — is the
single signature to look for if a future alias ever stops working.

## Client side (`aegisctl`)

The added SAN means `aegisctl context trust <new-host>:<port>
--insecure-skip-tls-verify` will TOFU-pin the certificate cleanly. After
that, all `aegisctl` operations work against the alias without further
TLS flags. The trust file lives at `~/.aegisctl/trust/<host>_<port>.pem`.

## When this approach is wrong

- The new endpoint needs a **different TLS certificate** (e.g. a real
  CA-signed cert for a public DNS name). Use a separate site block with
  its own `tls` directive — `tls internal` only signs from Caddy's local
  CA.
- The new endpoint needs to serve a **different set of handlers** (e.g.
  only the share `/s/*` routes, not the SPA). Use a second site block
  with its own `handle` rules. Sharing one block, as above, only fits
  the "same service, more addresses" case.
- You're routing through a **TLS-terminating proxy** that re-encrypts
  upstream. Then host-aliasing isn't the issue — re-write the `Host`
  header at the proxy or terminate TLS at the proxy and proxy to plain
  HTTP. Caddy site config doesn't enter into it.
