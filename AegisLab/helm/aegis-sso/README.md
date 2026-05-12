# aegis-sso

Standalone Helm chart for the Aegis SSO/identity service (`cmd/aegis-sso`).
Sibling to the main `rcabench` backend chart; both reuse the same monorepo
container image — the entrypoint dispatches on `argv[0]=="aegis-sso"`.

## Required: RSA private key

The chart does NOT ship a real RSA key. Generate one per environment:

```bash
openssl genrsa -out sso-private.pem 2048
```

Install with the key injected:

```bash
helm install aegis-sso ./helm/aegis-sso \
  --set-file privateKey.pem=sso-private.pem
```

Or pre-create a Secret and reference it:

```bash
kubectl create secret generic aegis-sso-key \
  --from-file=sso-private.pem=./sso-private.pem
helm install aegis-sso ./helm/aegis-sso \
  --set privateKey.existingSecret=aegis-sso-key
```

## First-boot OIDC client secret

On first boot the SSO service seeds the `aegis-backend` OIDC client and writes
the one-time secret to `[sso] seed_secret_dump_path` (default
`/var/lib/aegis-sso/.first-boot-secret`). This path is backed by a PVC; read
it once and stash in your backend's secret store:

```bash
kubectl exec deploy/aegis-sso -- cat /var/lib/aegis-sso/.first-boot-secret
```

## Backend wiring

The backend chart (`helm/`) exposes an `ssoclient` values block; populate it
to point at this service.
