# aegisctl TLS reference

aegislab production runs behind an edge-proxy Caddy doing `tls internal`,
which mints a self-signed root and issues per-service leaf certs from it.
Engineers have to trust that root before `aegisctl` can talk to the API
without errors. This page documents the full toolkit aegisctl ships for
that workflow.

## Resolution chain

For every HTTPS call, the CLI resolves two knobs:

| Knob | Flag | Env | Context field |
| --- | --- | --- | --- |
| Extra CA(s) | `--ca-cert <path>` | `AEGIS_CA_CERT` | `ca-cert` |
| Skip verify | `--insecure-skip-tls-verify` | `AEGIS_INSECURE_SKIP_VERIFY` | `insecure-skip-tls-verify` |

Priority: **flag > env > active context > auto-discovery**.

Auto-discovery (lowest priority, always merged in) appends every PEM file
in `~/.aegisctl/certs/*.{crt,pem}` plus `~/.aegisctl/ca.crt` to the
system root pool.

If both `--insecure-skip-tls-verify` and a CA cert resolve to non-empty
values, `--insecure-skip-tls-verify` wins (it's strictly more permissive)
and the CLI prints a one-shot stderr warning so the operator notices.
`--quiet` suppresses the warning.

### Precedence example

```sh
# Active context has insecure-skip-tls-verify=true persisted.
# Env says: use this CA. Flag says: use this other CA.
AEGIS_CA_CERT=/etc/env-ca.crt aegisctl --ca-cert /etc/flag-ca.crt project list
#   → flag wins; /etc/flag-ca.crt is used. Insecure NOT set (flag default false).
```

If the same command is run without `--ca-cert`, the env's `/etc/env-ca.crt`
applies. Drop the env too and the context's `insecure-skip-tls-verify=true`
takes over (so verification is disabled).

## When TLS verification fails

The CLI translates `x509:` / `tls:` errors into a multi-line remediation
block on stderr and exits with code **3** (auth failure):

```
Error [tls_verification_failed]: TLS verification failed talking to https://118.196.98.178:8082.
  x509: certificate signed by unknown authority

Fix by one of:
  (a) Auto-trust the server (TOFU):
        aegisctl context trust
  (b) Persist a CA in the current context:
        aegisctl context set --name <ctx> --ca-cert /path/to/ca.crt
  (c) One-shot:
        --ca-cert /path/to/ca.crt          (or AEGIS_CA_CERT=/path)
  (d) Bypass (DEV ONLY):
        --insecure-skip-tls-verify          (or AEGIS_INSECURE_SKIP_VERIFY=1)

Underlying: …
```

Under `--output json` the same payload is emitted on stderr as a single
JSON envelope with `type=tls_verification_failed` and `exit_code=3`.

## `aegisctl context trust` (TOFU)

Opens a TLS handshake to the context's server with
`InsecureSkipVerify=true` so we can read the presented chain regardless
of trust, walks the chain for the most-likely trust anchor, and saves
its PEM to disk:

1. Prefer the LAST cert whose `Subject == Issuer` AND `IsCA` (a
   self-signed root).
2. Fall back to the last `IsCA` cert in the chain.
3. Final fallback: the leaf, with a stderr warning that short rotation
   cycles will keep invalidating trust.

After confirmation (TTY-prompt, or `--yes` / `--force`) it writes
`~/.aegisctl/certs/<host>-<sha8>.crt` (mode 0600, dir 0700) and updates
the context's `ca-cert` field to point at that file. Subsequent calls
will validate the chain normally without ever needing `--insecure`.

### Walkthrough

```sh
$ aegisctl context use byte
$ aegisctl context trust
Server:        https://118.196.98.178:8082
Resolves to:   118.196.98.178:8082
Leaf cert:
  Subject:     CN=byte-cluster.aegis.local
  Issuer:      CN=Caddy Local Authority - 2025 ECC Intermediate
  Valid until: 2026-05-25T11:32:00Z
CA to trust:
  Subject:     CN=Caddy Local Authority - ECC Root
  SHA-256:     8C:34:AB:…
  Valid until: 2035-05-25T11:32:00Z
Save path:     /home/me/.aegisctl/certs/118.196.98.178-deadbeef.crt
Trust this CA and save it to /home/me/.aegisctl/certs/118.196.98.178-deadbeef.crt? [y/N] y
Trusted. Context "byte" now uses --ca-cert /home/me/.aegisctl/certs/118.196.98.178-deadbeef.crt.
```

### Flags

| Flag | Meaning |
| --- | --- |
| `--yes` / `--force` | Skip the confirmation prompt (CI/non-interactive). |
| `--print` | Show the summary and exit; do not write any file or modify the context. |
| `--ca-only` | Save only the issuing CA (default). |
| `--output json` | Emit the summary on stdout (JSON), errors on stderr. |

### Exit codes

| Code | Reason |
| --- | --- |
| 0 | Trusted (or `--print` succeeded). |
| 2 | Usage error (non-HTTPS server, missing context, …). |
| 6 | Non-interactive mode without `--yes`. |
| 7 | Context name not found. |
| 10 | Server returned no certificates. |

## File layout

| Path | Permissions | Purpose |
| --- | --- | --- |
| `~/.aegisctl/config.yaml` | 0600 | All saved contexts including `ca-cert` / `insecure-skip-tls-verify` fields. |
| `~/.aegisctl/ca.crt` | 0600 | Single legacy CA file, still auto-discovered. |
| `~/.aegisctl/certs/*.{crt,pem}` | 0600 (dir 0700) | Auto-discovered CA bundle. `context trust` writes here. |

## For cluster admins

`context trust` is **Trust-On-First-Use**: there is no chain of trust on
the very first connection. A network attacker who intercepts that first
handshake can persist their CA into the engineer's config. The exposure
window is one prompt — subsequent calls validate against the saved CA —
but it is non-zero.

When the operational risk warrants it, distribute the CA out-of-band:

- Publish `caddy_internal_ca.crt` to a private artifact registry; have
  engineers `curl … -o ~/.aegisctl/certs/aegis-internal.crt` from there.
- Bake the CA into the corporate trust store via MDM; auto-discovery
  picks up `~/.aegisctl/ca.crt` automatically.
- Encode the CA fingerprint in onboarding docs so engineers can compare
  the SHA-256 printed by `context trust` before answering `y`.

## End-to-end: byte-cluster

```sh
# 1. Create the context.
aegisctl context set --name byte --server https://118.196.98.178:8082

# 2. Switch to it.
aegisctl context use byte

# 3. Capture the cluster's self-signed CA (TOFU).
aegisctl context trust            # answer y

# 4. Log in via password grant — TLS validates against the saved CA.
aegisctl auth login \
  --username admin \
  --password-stdin   <<< 'TempAdminPass2026!'

# 5. Verify.
aegisctl auth status
aegisctl project list
```

## See also

- `docs/dev-intercept.md` — the Caddy-fronted dev anchors that issue the
  same self-signed roots. If you intercept dev traffic through
  telepresence, you still hit `https://118.196.98.178:8082` and the same
  TOFU workflow above applies.
