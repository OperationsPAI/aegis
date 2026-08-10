# Authentication — first-deploy credentials & login

aegislab uses **SSO (OIDC)** as its only authentication path. There is no
hard-coded default username/password. This document describes where the
first-boot admin credential comes from and how a human (or `aegisctl`)
logs in against any cluster.

## How credentials get created on first deploy

When the `rcabench-sso` pod boots against a fresh database, it runs the
seeding routine in `src/boot/sso/initialization.go`:

| What | Where it lands |
|---|---|
| Admin user (`username=admin`, `email=admin@aegis.local`) | rcabench MySQL `users` table |
| Admin **plaintext password** | A. Logged once at WARN level by the SSO pod<br>B. Written to `/var/lib/sso/.first-boot-secret.admin` on the SSO PVC (`0o600`) |
| OIDC backend client (`client_id=aegis-backend`) | rcabench MySQL `oidc_clients` table |
| OIDC backend client_secret | A. Logged once at WARN<br>B. Written to `/var/lib/sso/.first-boot-secret` on the SSO PVC<br>C. Mirrored to a K8s Secret named **`sso-bootstrap`**, key `aegis-backend-secret` (for downstream services that can't mount the SSO PVC) |
| OIDC console client (for the web UI) | rcabench MySQL `oidc_clients` table — public client, PKCE-protected, no secret |

The admin password defaults to a **random 32-hex-char string** unless the
operator pre-supplies one via the `AEGIS_SSO_BOOTSTRAP_PASSWORD` env var
on the SSO Deployment. Subsequent boots are no-ops: existing rows are
left untouched, and no dump file is rewritten.

### Pre-supplying a known password (optional)

Add to your overlay's `manifests/<env>/rcabench.values.yaml` under the
SSO subchart values:

```yaml
sso:
  env:
    AEGIS_SSO_BOOTSTRAP_PASSWORD: "your-secret-here"
```

Use any secret-injection mechanism your team prefers; don't commit
plaintext.

## Retrieving the seeded password

```bash
# 1) Find the SSO pod
SSO_POD=$(kubectl -n exp get pod -l app=rcabench-sso \
  --no-headers -o custom-columns=NAME:.metadata.name | head -1)

# 2) Read the dump file (one-time persisted on the SSO PVC)
kubectl -n exp exec "$SSO_POD" -- cat /var/lib/sso/.first-boot-secret.admin
# → e.g. a1e3c6964cbba29040070e6ffebd9aa2

# 3) (Separately) the OIDC backend client_secret, if a service needs it
kubectl -n exp get secret sso-bootstrap \
  -o jsonpath='{.data.aegis-backend-secret}' | base64 -d
```

If `/var/lib/sso/.first-boot-secret.admin` is missing, the seeder
already ran (DB has the user) but the dump file got purged or the PVC
was reset. Rotate via the admin API or wipe the SSO DB row and let the
seeder re-run.

## How a human logs in

### Option A — `aegisctl` (recommended for CLI work)

```bash
# Forward the edge-proxy locally
kubectl -n exp port-forward svc/rcabench-edge-proxy 8082:8082 &

# Exchange password for a JWT (saved into ~/.aegisctl/config.yaml)
ADMIN_PW=$(kubectl -n exp exec "$SSO_POD" -- cat /var/lib/sso/.first-boot-secret.admin)
echo "$ADMIN_PW" | aegisctl auth login \
  --server http://127.0.0.1:8082 \
  --username admin --password-stdin

# Verify
aegisctl --server http://127.0.0.1:8082 system list
```

`aegisctl auth login --username --password-stdin` hits
`POST /api/v2/auth/login`, which the backend forwards to SSO using the
OAuth2 password-grant flow. No browser involved. Stored token expires
after 24h; rerun `auth login` to refresh.

### Option B — API key (for service accounts / CI)

```bash
# After human login, mint a key
aegisctl auth api-key create --name ci-bot --expires 90d

# Use it
aegisctl auth login \
  --server http://127.0.0.1:8082 \
  --key-id <pk_xxx> --key-secret <ks_xxx>
```

API key login hits `POST /api/v2/auth/api-key/token` (signature-based
exchange — the secret never crosses the wire after creation).

### Option C — Web console (browser)

Visit `https://<console-domain>/`. The UI redirects through the SSO
authorize endpoint (`/oauth2/authorize?client_id=aegis-console&...`) and
completes the PKCE flow. Username/password screen is presented by SSO
itself, not by the rcabench frontend.

## Why `data.yaml admin_user` no longer has a password

Earlier versions shipped `admin_user.password: admin123` in every
`data.yaml`. That field is unused — the rcabench-side seeder creates an
ownership row in `users` but does not authenticate against it; SSO has
its own admin user with its own password. The misleading default was
removed so newcomers don't waste time trying to log in with `admin123`.

## Related code

- `src/boot/sso/initialization.go` — `seedDefaultAdminUser`,
  `seedDefaultOIDCClient`, `upsertBootstrapK8sSecret`
- `src/boot/seed/producer.go` — `initializeAdminUser` (rcabench-side
  ownership row, no auth)
- `src/cli/cmd/auth.go` — `aegisctl auth login` implementation
- `src/cli/client/auth.go` — password / API-key login HTTP wires
