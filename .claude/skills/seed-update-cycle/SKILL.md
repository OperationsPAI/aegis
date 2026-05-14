---
name: seed-update-cycle
description: How to push a seed / helm-value / chart change from a merged PR into the live byte-cluster so backend RP picks it up. Covers CM apply, worker restart, reseed, helm release teardown, kubectl set image (instead of helm upgrade for backend), drift recovery via direct SQL, and the foot-guns that broke us today (helm --set [0] array replace, YAML float coercion, multi-arch image push, LB EIP churn). Trigger when user merges a `chore(byte-cluster/seed)` / `fix(byte-cluster/seed)` / `chore(byte-cluster): bump rcabench image` PR and asks "apply", "更新", "reseed", "推到集群", "更新流程", "怎么生效", or wants to know why a merged seed change isn't visible in fresh ts/hs/otel-demo namespaces.
---

# byte-cluster seed / helm-value update cycle

A merged PR on aegis main does NOT automatically reach the live cluster. The byte-cluster has 5 layers of state and you need to touch the right ones. Picking the wrong path (helm upgrade vs kubectl set image, reseed vs SQL) corrupts state or churns the LB EIP.

## Layers and what propagates them

| Change | Layer | Push command |
|---|---|---|
| Backend Go code | `pair-cn-shanghai.cr.volces.com/opspai/rcabench:<tag>` Deployment image | docker build + push + `kubectl set image deploy/rcabench-{api-gateway,runtime-worker-service}` |
| Detector / clickhouse_dataset image | DB `container_versions.image_ref` | seed bump + reseed (no Deployment touch — algo/BD jobs use it on next dispatch) |
| `data.yaml` seed (helm_config.values) | DB `parameter_configs` + `helm_config_values` rows | apply CM + restart worker + `aegisctl system reseed --apply` |
| `otel-demo.yaml` / `ts.yaml` overlay | CM `rcabench-initial-data` data key | apply CM + restart worker (no reseed; backend reads file at install time) |
| Chart change (LGU DSB / train-ticket / otel-demo-aegis fork) | helm repo gh-pages publish + seed bump pedestal_version | upstream PR merge → wait for chart publish → seed bump pedestal_version + helm_config.version |
| ts/hs/otel-demo helm release in namespace | per-ns helm release | `helm uninstall + delete ns` (loop's next round triggers fresh RP install) |

## Standard apply flow

1. **Pull main**: `git checkout main && git pull --ff-only`. Verify the diff landed.
2. **Apply CM** (whenever `data.yaml` / `otel-demo.yaml` / `ts.yaml` changed):
   ```bash
   cd aegislab && kubectl -n exp create cm rcabench-initial-data \
     --from-file=data.yaml=manifests/byte-cluster/initial-data/data.yaml \
     --from-file=otel-demo.yaml=manifests/byte-cluster/initial-data/otel-demo.yaml \
     --from-file=ts.yaml=manifests/byte-cluster/initial-data/ts.yaml \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
3. **Restart api-gateway + worker** (so they re-mount the CM):
   ```bash
   kubectl -n exp rollout restart deploy/rcabench-api-gateway deploy/rcabench-runtime-worker-service
   kubectl -n exp rollout status ...   # wait for both
   ```
4. **Reseed** (only when DB seed `helm_config.values` changed):
   ```bash
   /tmp/aegisctl --server http://118.196.98.67:8082 system reseed --apply
   ```
   Look for `applied`/`backfilled`/`new helm value`. If reseed shows nothing for an entry you expect, see "drift recovery" below.
5. **Nuke affected helm releases** (only when helm template/chart logic changed and existing releases have the old shape baked in):
   ```bash
   helm ls -A --short | grep -E "^(ts|hs|otel-demo)[0-9]+$" | xargs -I {} -P 12 sh -c 'helm uninstall {} -n {} 2>&1 | tail -1'
   kubectl get ns | awk '$1 ~ /^(ts|hs|otel-demo)[0-9]+$/ {print $1}' | xargs -I {} -P 12 kubectl delete ns {} --wait=false
   sleep 12
   # finalizer strip for stuck Terminating
   kubectl get ns | awk '$1 ~ /<sys>[0-9]+$/ && $2=="Terminating" {print $1}' | \
     xargs -I {} sh -c 'kubectl get ns {} -o json | jq ".spec.finalizers=[]" | \
       kubectl replace --raw /api/v1/namespaces/{}/finalize -f -'
   ```
6. **Loop picks up next round** (15-20 min sleep cadence). New RP install uses the new chart / values / image.

## NEVER do `helm upgrade rcabench` on byte-cluster

The Volcengine LB EIP is bound to `rcabench-edge-proxy` Service via manual annotation (see #335 / #336). `helm upgrade rcabench` re-renders that Service and risks the controller dropping/re-creating the CLB → EIP changes → external clients break. Always use **`kubectl set image`** for backend Deployment updates:

```bash
kubectl -n exp set image deploy/rcabench-api-gateway api-gateway=pair-cn-shanghai.cr.volces.com/opspai/rcabench:<tag>
kubectl -n exp set image deploy/rcabench-runtime-worker-service runtime-worker-service=pair-cn-shanghai.cr.volces.com/opspai/rcabench:<tag>
```

Long-term fix: split edge-proxy into its own helm release (issue #342).

## Drift recovery via direct SQL

`aegisctl system reseed --apply` only writes:
- New `container_versions` (immutability contract: bump the version name in seed)
- New `helm_config_values` rows for new versions
- Backfilled rows for existing versions when key didn't exist
- DRIFT: changed `default_value` on existing keys is sometimes NOT picked up

When reseed log shows nothing for a key you just changed:
```bash
kubectl -n exp exec rcabench-mysql-0 -- mysql -uroot -pyourpassword rcabench -e \
  "UPDATE parameter_configs SET default_value='<new>' WHERE config_key='<key>';"
```

When you removed a seed entry and need to drop the existing row:
```bash
kubectl -n exp exec rcabench-mysql-0 -- mysql -uroot -pyourpassword rcabench -e "
DELETE hcv FROM helm_config_values hcv
  JOIN parameter_configs pc ON pc.id=hcv.parameter_config_id
  WHERE pc.config_key='<key>';
DELETE FROM parameter_configs WHERE config_key='<key>';"
```

## Foot-guns that broke us today

1. **helm `--set foo[0].image=X` REPLACES the array, doesn't merge fields**. Setting only `[0].image` clears `[0].name` + `[0].command` from the chart default. k8s rejects with `spec.template.spec.initContainers[0].name: Required value`. Either set the FULL object, or use the sidecar overlay file (helm `-f` does deep-merge).

2. **YAML float coercion of versioned tags**. Seed `default_value: "17.6"` round-trips as float `17.6` through Go yaml, then helm chart schema rejects (wants string). Use a non-numeric tag suffix like `17.6-bookworm`. Other safe tags: `2.2.0` (two dots = string), `v0.12.9` (v prefix = string), `9.0.1-alpine3.23` (non-digit = string).

3. **byte-cluster nodes can NOT egress docker.io directly**. Always use `pair-cn-shanghai.cr.volces.com/opspai/<image>:<tag>`. Volces auto-mirrors `docker.io/opspai/*` to that path — push to docker.io then references resolve.

4. **Multi-arch image push fails with `docker push`**. `docker pull` only fetches one platform; push of the resulting manifest list fails on missing layers. Use:
   ```bash
   docker buildx imagetools create --tag docker.io/opspai/<image>:<tag> <source-image>
   ```
   Direct registry-to-registry copy preserves all platforms.

5. **runtime-worker pod caches CM values at startup**. After CM apply, you MUST `kubectl rollout restart deploy/rcabench-runtime-worker-service`. K8s CM-mount auto-sync (~60s) updates the file on disk, but the worker process re-reads only on startup.

6. **`status=1` zombie container_versions break the pedestal selector**. The selector picks among `status=1` rows. Old rows like `hs@0.1.1` (chart never published) hang around with `status=1` and the `(name_major, name_minor, name_patch)` ordering ties at `(0,0,0)` because legacy rows have unpopulated semver fields. PR #330 added `id DESC` tie-breaker. If you see `helm pull` 404s for a chart version that doesn't exist, check `SELECT * FROM container_versions WHERE container_id=<X>` and SQL set old rows `status=-1`.

7. **`helm_configs.value_file` is a FROZEN snapshot, not a CM mount** (#360). When you bump `pedestal_version` in `data.yaml`, the `value_file` column points to a path like `/var/lib/rcabench/dataset/helm-values/<system>_values_<timestamp>.yaml` inside the api-gateway PVC. The file is captured at first-register / new-version INSERT (producer.go:275, reseed.go:227); existing-version reseed is drift-detect-only and explicitly preserves the path (`upsertHelmConfigForReseed` in reseed.go:897). It DOES NOT update when you `kubectl create cm` the overlay. Sink (`dto/container.go::GetValuesMap:59`) reads ValueFile from disk at install time and silently treats missing/0-byte as "no overlay". Symptom: helm install doesn't include overlay content (e.g. otel-demo init containers, llm disable). Verify per-version:
   ```bash
   kubectl -n exp exec rcabench-mysql-0 -- mysql -uroot -pyourpassword rcabench -e \
     "SELECT cv.name, hc.value_file FROM container_versions cv
        JOIN helm_configs hc ON hc.container_version_id=cv.id
        WHERE cv.container_id IN (SELECT id FROM containers WHERE name='<sys>')
          AND cv.status=1;"
   ```
   Quick fix: overwrite the file in-place via api-gateway pod:
   ```bash
   POD=$(kubectl -n exp get pods -l app=rcabench-api-gateway -o jsonpath='{.items[0].metadata.name}')
   cat aegislab/manifests/byte-cluster/initial-data/<sys>.yaml | \
     kubectl -n exp exec -i $POD -c api-gateway -- tee <value_file_path> > /dev/null
   ```
   Then nuke + re-install affected ns. Long-term: re-snapshot on reseed when overlay bytes change (#360 fix A, ~40 LOC).

8. **Stuck RP tasks (state=2) accumulate as zombies**. Backend's stuck-task reconciler doesn't catch RP tasks that lost their namespace (e.g. mid-install ns nuke). Pile-up saturates the RP token bucket and starves new submits. Drain with:
   ```sql
   UPDATE tasks SET state=-1, updated_at=NOW()
     WHERE type=1 AND state=2 AND updated_at < NOW() - INTERVAL 30 MINUTE;
   ```

## EIP recovery (if LB drops)

**There is no kubectl-side recovery.** If `curl http://118.196.98.67:8082` times out, the VKE LB controller has lost the binding and re-applying annotations on `rcabench-edge-proxy` will NOT bring it back. The user must re-bind the EIP **manually in the Volcengine VKE console** (Service → CLB → attach EIP).

Don't waste time trying `kubectl annotate` workarounds — flag the issue to the user and wait for manual cutover. Long-term fix is #342 (split edge-proxy into its own helm release so it's never touched by `helm upgrade rcabench`).
