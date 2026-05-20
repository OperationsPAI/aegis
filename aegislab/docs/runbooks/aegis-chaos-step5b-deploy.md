# aegis-chaos Step 5b — Deploy & Validate Runbook

This runbook captures the **byte-cluster deploy + end-to-end validation** for
the aegis-chaos service-migration §11 step 5b rounds (R1 auth, R2 etcd CLI,
cleanup, R3 ns-clarity, R4a SSE).

Validated on `byte-cluster` (kube context `cd5n1vu2talcp90v8b7r0@…`) on
2026-05-20 with image `docker.io/opspai/rcabench:byte-20260520-step5b-full`
(volces mirror `pair-cn-shanghai.cr.volces.com/opspai/rcabench:byte-20260520-step5b-full`).

## Prerequisites
- monorepo HEAD at or past `914abf53` (R3) + `e7b4d48d` (R4a)
- helm release `rcabench` in ns `exp` already at revision ≥ 90
- chaos subchart enabled in upstream `helm/values.yaml` (`chaos.enabled: true`
  in the byte-cluster overlay)

## Step 0 — Build & push image
```bash
TAG=byte-20260520-step5b-full
docker build -f aegislab/src/Dockerfile -t docker.io/opspai/rcabench:$TAG .
docker push docker.io/opspai/rcabench:$TAG
# volces auto-mirrors docker.io/opspai/* → pair-cn-shanghai.cr.volces.com/opspai/*
# NEVER docker login volces — auto-mirror only, push fails.
```

## Step 1 — Create the shared bearer Secret
The R1 `chaosAuth` block requires an operator-managed Secret. Both directions
(backend→chaos `CHAOS_OUTBOUND_BEARER`, chaos inbound `CHAOS_INBOUND_BEARER`)
share it so rotation is one place.

```bash
TOKEN=$(openssl rand -hex 24)
kubectl -n exp create secret generic rcabench-chaos-auth \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Save `$TOKEN` somewhere recoverable (sealed-secret in prod). To rotate, run
the same command — backend/chaos pods don't auto-reload, so `kubectl rollout
restart deploy/rcabench-aegis-api deploy/rcabench-runtime-worker-service
deploy/rcabench-chaos` after rotation.

## Step 2 — Overlay values
`aegislab/manifests/byte-cluster/rcabench.values.yaml` has the chaos block
appended at the bottom:

```yaml
chaos:
  enabled: true
  auth:
    secretName: rcabench-chaos-auth
    secretKey: token
  chaosAuth:
    secretName: rcabench-chaos-auth
    secretKey: token
```

Image tag in the same overlay (two places: `global.images.rcabench.tag` line
~26 and `sso.image.tag` line ~342) is bumped to the step 5b build.

## Step 3 — Helm upgrade (CAUTION on values inheritance)
**Do NOT use plain `helm upgrade -f overlay.yaml`** — the overlay does not
contain `initialData.admin_user`, and the default `helm upgrade` discards
prior `--set` user-supplied values. The new pod will crash with
`failed to determine producer bootstrap state: initial data admin user
username is empty`.

Two safe patterns:

**Pattern A — full-state upgrade (canonical):**
```bash
helm -n exp get values rcabench --revision <last-good> -o yaml > /tmp/prior.yaml
helm -n exp upgrade rcabench aegislab/helm \
  -f /tmp/prior.yaml \
  -f aegislab/manifests/byte-cluster/rcabench.values.yaml
```
Right-most `-f` wins on merge, so the overlay's chaos block and bumped tags
override the prior snapshot.

**Pattern B — preserve last release's values:**
```bash
helm -n exp upgrade rcabench aegislab/helm \
  -f aegislab/manifests/byte-cluster/rcabench.values.yaml \
  --reset-then-reuse-values
```
This actually has the same admin_user loss in our experience — Pattern A is
the more reliable knob. Document any deviation in the helm release notes.

## Step 4 — Wait for rollout
```bash
kubectl -n exp rollout status deploy/rcabench-aegis-api
kubectl -n exp rollout status deploy/rcabench-runtime-worker-service
kubectl -n exp rollout status deploy/rcabench-chaos
```

## Step 5 — Verify env wiring
```bash
# aegis-api gets CHAOS_OUTBOUND_BEARER
kubectl -n exp exec $(kubectl -n exp get pods -o name | grep aegis-api- | head -1) \
  -c aegis-api -- env | grep -E '^CHAOS_OUTBOUND_BEARER='

# chaos pod gets CHAOS_INBOUND_BEARER
kubectl -n exp exec $(kubectl -n exp get pods -o name | grep rcabench-chaos- | head -1) \
  -- env | grep -E '^CHAOS_INBOUND_BEARER='
```

Confirm ClusterRole has all 7 chaos-mesh CRDs (avoid the R-pre-cleanup
podchaos-only blocker):
```bash
helm template rcabench aegislab/helm \
  -f /tmp/prior.yaml \
  -f aegislab/manifests/byte-cluster/rcabench.values.yaml \
  | awk '/kind: ClusterRole/,/^---/' \
  | grep -A8 'chaos-mesh.org' | head -12
```
Expect: podchaos, networkchaos, stresschaos, timechaos, dnschaos, httpchaos, jvmchaos.

## Step 6 — Flip the catalog flag via aegisctl etcd
```bash
just build-aegisctl  # or: cd aegislab/src && go build -o /tmp/aegisctl ./cli
/tmp/aegisctl etcd get aegis.injection.catalog_source
# expect "in_process"
/tmp/aegisctl etcd put aegis.injection.catalog_source chaos_service \
  --reason "<deploy ticket / change id>"
/tmp/aegisctl etcd get aegis.injection.catalog_source
# expect "chaos_service"
```
This goes through aegis-configcenter's `PUT /api/v2/config/aegis/injection.catalog_source`,
not raw etcd. Audit logs apply.

To roll back: `/tmp/aegisctl etcd put aegis.injection.catalog_source in_process`.

## Step 7 — End-to-end smoke
The chaos service is ClusterIP-only on `:8086`. From a laptop, port-forward:
```bash
kubectl -n exp port-forward svc/rcabench-chaos 18086:8086 &
```

### 7.1 Auth smoke
```bash
TOKEN=$(kubectl -n exp get secret rcabench-chaos-auth -o jsonpath='{.data.token}' | base64 -d)
# Public endpoint (no auth):
curl -s http://localhost:18086/v1beta/manifest-schema.json | head -c 80
# Authed endpoint:
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:18086/v1beta/systems/otel-demo/points | head -c 200
# Expect: 200 with point list.
# WITHOUT token → falls through to TrustedHeaderAuth → 401.
```
Note: do not use `curl -sI` (HEAD) — gin returns 404 because none of the v1beta
routes register HEAD handlers. Use plain `curl -s` or `-X GET`.

### 7.2 Inject smoke (manual)
```bash
TOKEN=$(kubectl -n exp get secret rcabench-chaos-auth -o jsonpath='{.data.token}' | base64 -d)
# 1) Pick a point that matches a real cluster ns (see step 5b R4b gap below).
POINT_ID=$(curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:18086/v1beta/systems/otel-demo/points?service=frontend&capability=pod_failure" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["data"]["points"][0]["id"])')

# 2) Submit injection.
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"idempotency_key\":\"smoke-$(date +%s)\",\"point_id\":\"$POINT_ID\",\"namespace\":\"otel-demo0\",\"duration\":30}" \
  http://localhost:18086/v1beta/injections | python3 -m json.tool

# 3) Watch via SSE.
INJ_ID=<id from step 2>
curl -sN -H "Authorization: Bearer $TOKEN" \
  "http://localhost:18086/v1beta/injections/$INJ_ID/events"

# 4) Destroy when done.
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" \
  "http://localhost:18086/v1beta/injections/$INJ_ID" | python3 -m json.tool
```

### 7.3 SSE end-to-end (aegisctl)
```bash
/tmp/aegisctl chaos inject watch <injection-id> --timeout 5m
# Pretty-prints each transition; exits 0 on succeeded/destroyed, 1 otherwise.
```

## Known gaps (do NOT promote chaos_service as default until these are fixed)

These surfaced during 2026-05-20 validation. R3 (catalog preflight ns-clarity)
documented the logical/concrete-ns distinction in comments and renames but did
not fully wire the concrete ns through the runtime path:

1. **`aegisctl regression run --via-chaos` PodFailure target uses concrete ns.**
   `inject_guided_via_chaos.go` builds `target: {namespace: cfg.Namespace, app: cfg.App}`
   where `cfg.Namespace` is the pool-allocated ns (e.g. `otel-demo0`). The
   chaos catalog is seeded with `target: {namespace: <logical>, ...}` (e.g.
   `otel-demo`), so the `point_id` derived locally never matches the catalog's
   row → 404 `chaos: point not found`.
   **Fix needed**: aegisctl must use the *logical* system name in the target
   for catalog lookup; the concrete ns is carried separately in the request
   `namespace` field for CR apply.

2. **Executor uses point.target.namespace for CR create, not request namespace.**
   Even when (1) is bypassed and the request `namespace=otel-demo0` is set
   explicitly, the executor applies the chaos-mesh CR in the catalog's
   `target.namespace` (`otel-demo`) → CR creation fails with
   `namespaces "otel-demo" not found`.
   **Fix needed**: renderer/executor must honor the request's `namespace`
   field for CR apply (and selector `Namespaces:` list), with target metadata
   only contributing to selector labels.

3. **Workaround used during R4b validation**: import a manifest with the
   concrete ns into the catalog (`POST /v1beta/systems/otel-demo/points/import`
   with `target: {namespace: otel-demo0, app: frontend}`), then POST inject
   with that derived point_id. Full pipeline proven this way.

**Bottom line**: at the time of writing, `aegis.injection.catalog_source =
chaos_service` is safe to flip ON (the backend pre-flight observes it and
gracefully falls back), but NEW injection-write paths (`aegisctl --via-chaos`)
won't round-trip until gaps 1+2 are fixed. Keep the flag on `in_process` as
the default until the followup round.

## Followups (not part of 5b R4b)

| ID | Gap | Round target |
|----|-----|--------------|
| **5b-R5** | aegisctl `--via-chaos` logical-ns target + executor concrete-ns apply | next |
| **MAJOR M1** | `executor_chaosmesh.go` `cr_absent ⇒ ExecStateSucceeded` state laundering | 5b-R5 or 5c-prep |
| **MAJOR M5** | manifestgen `errors.Is` sentinel vs string-match | trivial; rolled into any manifestgen touch |
| **MAJOR M6** | reconciler HA — no `replicas:1` guard in helm | document as known constraint, add chart guard |
| **5c** | Tear down backend CRD watcher (irreversible) | post-soak |

## Image tag history (byte-cluster, 2026-05)
| Tag | Brief |
|-----|-------|
| `byte-20260518-orch-trace-coverage-r1` | pre-step-5b baseline |
| `byte-20260519-step4r4` | step 4 R4 (catalog preflight observable cutover) |
| `byte-20260520-step5b-full` | step 5b R1+R2+cleanup+R3+R4a — this runbook |
