# 08. `aegisctl chaos` Smoke Test

## Result

Status: `[BLOCKED]`

Subtasks 1-3 are implemented and verified locally:

- `chaos-experiment/pkg/chaoscli` builds and its tests pass.
- `chaos-exp` exposes the shared Cobra tree and `--dry-run` prints the generated spec.
- `aegisctl chaos` is wired to the same tree and `--dry-run` emits the `InjectSpec` YAML shape accepted by `aegisctl inject submit`.

The end-to-end live submit in the current kind environment is blocked by the running backend deployment state, not by the new CLI wiring.

## Commands Run

```bash
kubectl config current-context
/tmp/aegisctl-test2 status
/tmp/aegisctl-test2 chaos --help
/tmp/aegisctl-test2 chaos network delay \
  --project pair_diagnosis \
  --namespace ts \
  --app ts-auth-service \
  --target-service ts-verification-code-service \
  --duration 1m \
  -o json
kubectl logs -n default deploy/aegislab-backend-producer --tail=25
```

## Observed Output

`status` confirms the local backend is reachable at `http://127.0.0.1:18082`.

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

The backend log shows the running deployment is not aligned with the current local code and metadata:

```text
Failed to submit fault injection: failed to parse injection spec batch 0:
mismatched system type sn for pedestal ts at index 0
```

Additional environment evidence:

- `GET /api/v2/systems` in the running backend returns only 6 systems and does not include `otel-demo`.
- `kubectl get all -n exp` shows the benchmark namespace is empty in this cluster.

## Blocker

The current kind cluster is not running a backend deployment rebuilt from the local `AegisLab` changes, and the active backend metadata/workload state is incomplete for this smoke test.

Before rerunning subtask 4, update the environment to:

1. Redeploy the backend from the updated `AegisLab` checkout on branch `workbuddy/issue-14`.
2. Ensure the target system metadata is loaded in the running backend.
3. Ensure a compatible benchmark workload is actually deployed for the chosen namespace/app pair.

Once those are in place, rerun the submit and confirm the record appears via:

```bash
/tmp/aegisctl-test2 inject list --project pair_diagnosis
```
