# byte-cluster dev TLS

Locally-generated CA + leaf cert so Caddy on byte-cluster serves a
**trustable** TLS chain — once you install `ca.crt` in macOS Keychain
the "Not Secure" warning goes away and Chrome/Safari start offering to
save passwords again.

Everything that ends in `.crt`, `.key`, `.srl` here is gitignored —
private key material never enters the repo.

## Files

| file               | committed | purpose                                          |
|--------------------|-----------|--------------------------------------------------|
| `openssl-leaf.cnf` | yes       | SAN list for the leaf cert (edit and rerun)      |
| `regen.sh`         | yes       | One-shot CA + leaf generator                     |
| `ca.crt` / `ca.key`     | no   | Root CA — install `ca.crt` on every dev's Mac    |
| `server.crt` / `server.key` | no | Leaf cert Caddy serves                       |

## SANs covered

`openssl-leaf.cnf` includes:

- `118.196.98.178` — primary byte-cluster EIP
- `23.189.248.55` — secondary EIP (CLB port translation 38082→8082)
- `127.0.0.1` + `localhost` — for `kubectl port-forward` testing
- `aegis.local` — optional `/etc/hosts` alias

Add new ones to `openssl-leaf.cnf` then rerun `./regen.sh`.

## First-time setup

```bash
# 1. Generate
./regen.sh

# 2. Push into the cluster (replace <ns> + <release>)
kubectl -n <ns> create secret tls aegis-edge-tls \
  --cert=server.crt --key=server.key \
  --dry-run=client -o yaml | kubectl apply -f -

# 3. Restart edge-proxy so it picks up the new mount
kubectl -n <ns> rollout restart deployment <release>-edge-proxy

# 4. Helm side is already wired: rcabench.values.yaml sets
#    edgeProxy.tls.secretName=aegis-edge-tls. No helm upgrade needed
#    unless you flipped the value off and back on.
```

## Install CA on Mac

```bash
# from this dir, copy ca.crt to your Mac (scp, AirDrop, paste, whatever)
scp ca.crt mac:~/Downloads/aegis-dev-ca.crt
```

On Mac:

1. Double-click `aegis-dev-ca.crt` → Keychain Access opens → add to
   **login** keychain.
2. Find "Aegis Dev Root CA" in the list, **double-click it**.
3. Expand the **Trust** section at the top → "When using this
   certificate" → **Always Trust** → close window → enter password.
4. Fully quit Chrome / Safari (⌘Q) and reopen — they only re-read
   keychain trust at process start.

Verify: `https://118.196.98.178:8082` (or `23.189.248.55:38082`)
should show a green lock. Password manager will now offer to save
credentials.

## Rotation

The leaf is valid for 397 days (Apple's cap on trusted server certs).
When it expires:

```bash
./regen.sh                # reuses the existing CA — no Mac re-trust needed
./regen.sh --rotate-ca    # fresh CA — every dev re-installs ca.crt on Mac
```

After rotating, repeat the `kubectl create secret tls` + `rollout
restart` steps from "First-time setup".

## Disabling

Drop `edgeProxy.tls.secretName` (leave it empty) and Caddy falls back
to `tls internal`. Browser warning returns; password manager goes
silent again.
