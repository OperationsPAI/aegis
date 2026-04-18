# 08. `aegisctl chaos` Smoke Test

## Result

Status: `[BLOCKED]`

Subtasks 1-3 are implemented and verified locally:

- `chaos-experiment/pkg/chaoscli` builds and its tests pass.
- `chaos-exp` exposes the shared Cobra tree and `--dry-run` prints the generated spec.
- `aegisctl chaos` is wired to the same tree and `--dry-run` emits the `InjectSpec` YAML shape accepted by `aegisctl inject submit`.

The end-to-end live submit in the current kind environment is still blocked by the running backend deployment state, not by the new CLI wiring.

## Commands Run

```bash
kubectl config current-context
kubectl port-forward -n default svc/aegislab-backend-exp 18082:8080
/tmp/aegisctl-test auth login --server http://127.0.0.1:18082 --username admin --password admin123
/tmp/aegisctl-test status
/tmp/aegisctl-test chaos --help
/tmp/aegisctl-test chaos network delay \
  --project pair_diagnosis \
  --namespace ts \
  --app ts-auth-service \
  --target-service ts-verification-code-service \
  --duration 30s \
  -o json
/tmp/aegisctl-test inject list --project pair_diagnosis -o json
kubectl logs -n default deploy/aegislab-backend-producer --tail=25
```

## Observed Output

`kubectl config current-context` returns `kind-aegis-local`.

The port-forward succeeds:

```text
Forwarding from 127.0.0.1:18082 -> 8080
```

`auth login` succeeds and `status` confirms the local backend is reachable at `http://127.0.0.1:18082`.

`chaos --help` shows the new subtree:

```text
Available Commands:
  http
  jvm
  network
  pod
  stress
```

The live submit currently fails:

```text
Error: API error 500: An unexpected error occurred
```

`inject list` remains empty after the failed submit:

```json
{
  "items": [],
  "pagination": {
    "page": 1,
    "size": 20,
    "total": 0,
    "total_pages": 0
  }
}
```

The backend log shows the running deployment is still not aligned with the current local workload metadata:

```text
Failed to submit fault injection: failed to parse injection spec batch 0:
mismatched system type sn for pedestal ts at index 0
```

Additional environment evidence:

- `kubectl get pods -n exp` returns `No resources found in exp namespace`.
- `aegisctl container list -o json` shows pedestal records for both `ts` and `otel-demo`, but the live cluster only has the backend stack and Chaos Mesh components running.

## Blocker

The current kind cluster is reachable, but the active backend metadata/workload state is incomplete for this smoke test. The CLI now builds and the backend submitter follows the same translate-then-inject path as `inject submit`, but the live backend still rejects the chosen pedestal/workload combination before creating an injection record.

Before rerunning subtask 4, update the environment to:

1. Ensure a compatible benchmark workload is actually deployed for the chosen namespace/app pair.
2. Ensure the target system metadata loaded by the running backend matches that workload.
3. Re-run the submit against that live workload and confirm the record appears in the project listing.

Once those are in place, rerun the submit and confirm the record appears via:

```bash
/tmp/aegisctl-test inject list --project pair_diagnosis
```
