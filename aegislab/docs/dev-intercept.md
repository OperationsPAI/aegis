# Developer-laptop intercept via telepresence

This cluster exposes two **dev anchor** Services that act as
intercept targets for [Telepresence v2](https://www.telepresence.io/).
You run `telepresence intercept` against them and traffic that would
otherwise reach those Services is forwarded to your laptop instead.
The browser keeps hitting the production EIP (`https://118.196.98.178:8082`),
so SSO cookies, TLS, and same-origin all keep working — only the dev
prefix (`/dev/*`, `/dev/api/*`) is rerouted.

## Architecture

```
Browser ──► EIP (118.196.98.178:8082) ──► edge-proxy (Caddy)
                                              │
                                              │  @sso       → rcabench-sso
                                              │  @rustfs    → rcabench-rustfs
                                              ├─ @devApi   → rcabench-dev-api ──► (telepresence) ► laptop
                                              ├─ @dev      → rcabench-dev-frontend ──► (telepresence) ► laptop
                                              │  @api       → rcabench-gateway
                                              └  (default)  → rcabench-frontend
```

The dev anchors are tiny caddy pods that return a 503 placeholder when
no intercept is active. As soon as you intercept, your laptop receives
the requests.

## Cluster-side prerequisites

Both are persistent — install once per cluster.

### 1. Traffic-manager (mirrored images)

Telepresence's `tel2` image lives on `ghcr.io` and the `curlimages/curl`
hook image lives on `docker.io`; this cluster cannot reach either. They
are mirrored to `pair-cn-shanghai.cr.volces.com/opspai/{tel2,curl}` (via
volces auto-mirror from `docker.io/opspai/*`). Install with:

```bash
cat > /tmp/telepresence-values.yaml <<'EOF'
image:
  registry: pair-cn-shanghai.cr.volces.com/opspai
  name: tel2
  tag: "2.20.2"
agent:
  image:
    registry: pair-cn-shanghai.cr.volces.com/opspai
    name: tel2
    tag: "2.20.2"
hooks:
  curl:
    registry: pair-cn-shanghai.cr.volces.com/opspai
    image: curl
    tag: "8.1.1"
EOF

# Bump helm timeout, the initial image pull can be slow.
mkdir -p ~/.config/telepresence
cat > ~/.config/telepresence/config.yml <<'EOF'
timeouts:
  helm: 15m
EOF

telepresence helm install -f /tmp/telepresence-values.yaml
```

To upgrade later, `telepresence helm upgrade -f /tmp/telepresence-values.yaml`.

### 2. Dev anchors (this helm chart)

Set `devAnchors.enabled=true` in the cluster's rcabench values
(byte-cluster already has it on — see `manifests/byte-cluster/rcabench.values.yaml`).
A `helm upgrade` rolls out:

- `<release>-dev-anchor-config` ConfigMap (Caddyfile placeholder)
- `<release>-dev-frontend` Deployment + Service (port 80)
- `<release>-dev-api` Deployment + Service (port 80)

The edge-proxy Caddyfile picks up `@dev` and `@devApi` matchers
automatically (gated on the same flag) and re-rolls via its
checksum/config annotation.

## Developer workflow

### One-time laptop setup

```bash
# Linux amd64 — adjust URL for darwin/arm64
curl -sSL -o ~/.local/bin/telepresence \
  https://app.getambassador.io/download/tel2oss/releases/download/v2.20.2/telepresence-linux-amd64
chmod +x ~/.local/bin/telepresence

mkdir -p ~/.config/telepresence
cat > ~/.config/telepresence/config.yml <<'EOF'
timeouts:
  helm: 15m
EOF
```

Your laptop needs kubectl access to the same cluster (the same
kubeconfig that this repo already targets).

### Connect

```bash
sudo -v   # cache sudo creds; the root daemon needs them
telepresence connect -n exp
```

The "root daemon" sets up cluster DNS + a route to the in-cluster pod
network; it has to run as root. `telepresence connect` will prompt for
sudo on first launch — pre-caching with `sudo -v` makes it noiseless.

If you don't have sudo on the laptop, run the daemon in a container
instead:

```bash
telepresence connect --docker -n exp
# then run your dev server INSIDE the same docker network with:
telepresence intercept rcabench-dev-frontend --port 3323:80 \
    --docker-run -- node:20 npm run dev
```

`telepresence status` should show all three sections "Connected".

### Intercept

```bash
# Frontend: vite dev on :3323 → /dev/* reaches it as `http://localhost:3323/dev/...`
telepresence intercept rcabench-dev-frontend --port 3323:80

# Backend: aegis-api dev on :8080 → /dev/api/* reaches it as `http://localhost:8080/dev/api/...`
telepresence intercept rcabench-dev-api --port 8080:80

# Or a one-off python serve:
python3 -m http.server 8000
telepresence intercept rcabench-dev-frontend --port 8000:80
```

`--port LOCAL:80` is *laptop-port:anchor-port*. The anchor always
listens on 80; pick whichever local port your dev server uses.

### Test from the EIP

Open `https://118.196.98.178:8082/dev/` — it hits your laptop.

The path is **not rewritten** — your local server sees the full
`/dev/...` URL. Configure accordingly:

- **Vite**: `defineConfig({ base: '/dev/' })` so asset URLs and the
  router work under the prefix.
- **Static `python -m http.server`**: serves the path as-is, drop your
  `index.html` under `./dev/` locally.
- **Backend**: route `/dev/api/...` in your handler tree (typically a
  thin reverse path-strip).

SSO cookies set on the EIP host work automatically — same origin.

### Leave / clean up

```bash
telepresence leave rcabench-dev-frontend
telepresence leave rcabench-dev-api
telepresence quit       # tear down local daemons
```

While an intercept is live the anchor Deployment carries a
`traffic-agent` sidecar (injected by telepresence). `telepresence leave`
removes it.

## When to use what

- **Frontend only, full-stack still deployed**: intercept `dev-frontend`,
  point its API base URL at `https://118.196.98.178:8082/api/`. You're
  driving production backend with laptop UI.
- **Backend only, frontend still deployed**: intercept `dev-api`, hit
  `https://118.196.98.178:8082/dev/api/...` directly with curl/Postman.
- **Both**: intercept both, you own the full request path.
- **Ad-hoc demo (python serve / static html)**: intercept `dev-frontend`,
  whatever port your throwaway server uses.

## Troubleshooting

**`telepresence intercept` says "service not found"** — check
`kubectl get svc -n exp | grep dev-` and confirm `devAnchors.enabled=true`
in the cluster's values + a `helm upgrade` has been applied.

**Browser gets 503 with "no active intercept"** — that's the placeholder.
You forgot `telepresence intercept`, the daemon disconnected, or
`telepresence leave` already ran.

**Intercept hangs on agent injection** — the `traffic-agent` sidecar
image is being pulled. Check the anchor pod's events; if it's pulling
from `ghcr.io/...` instead of the mirrored registry, the traffic-manager
helm values weren't applied (rerun the `helm upgrade` from step 1).

**SSO cookie missing on `/dev/*`** — should not happen since it's the
same origin. If it does, ensure your dev server is not stripping the
`Cookie` header.
