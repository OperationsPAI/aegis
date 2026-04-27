# aegisctl

Command-line client for the AegisLab (RCABench) platform. Designed for both
human operators and AI agents to drive the supported validation workflow from
the terminal.

For the automation-facing CLI contract used by CI and agent workflows, see
`docs/aegisctl-cli-contract.md`.

## Build

```bash
cd AegisLab
just build-aegisctl output=./bin/aegisctl
# or manually:
cd src && go build -o /tmp/aegisctl ./cmd/aegisctl
```

Note: `aegisctl` does not require `-tags duckdb_arrow`.

## Quick validation flow

```bash
# 1. Log in and persist a bearer token in the current context
printf %s\n "$AEGIS_PASSWORD" | ./bin/aegisctl auth login \
  --server http://HOST:8082 \
  --username admin \
  --password-stdin \
  -o json

# 2. Verify the stored auth context and API readiness
./bin/aegisctl auth status -o json
./bin/aegisctl status -o json

# 3. Verify the pedestal Helm metadata before a restart/inject cycle
./bin/aegisctl pedestal helm verify --container-version-id 42 -o json

# 4. Prepare or dry-run the validation workload contract
./bin/aegisctl inject guided --reset-config --no-save-config
./bin/aegisctl inject guided --apply --dry-run \
  --project pair_diagnosis \
  --pedestal-name ts --pedestal-tag 1.0.0 \
  --benchmark-name otel-demo-bench --benchmark-tag 1.0.0 \
  --interval 10 --pre-duration 5

# 5. Submit algorithm execution against a datapack or dataset
./bin/aegisctl execute create --project pair_diagnosis --input ./execution.yaml -o json
```

The full CLI contract, including output and exit-code expectations, lives in
[`../../../docs/aegisctl-cli-spec.md`](../../../docs/aegisctl-cli-spec.md).

## Subcommands

| Command | Description |
|---------|-------------|
| `auth` | Login, token management, whoami |
| `context` | Manage named configuration contexts |
| `project` | List, get, create projects |
| `container` | List, get, build containers |
| `inject` | Submit, list, get fault injections |
| `execute` | Submit, list algorithm executions |
| `task` | List, inspect background tasks |
| `trace` | List, get, watch execution traces |
| `dataset` | List, get, manage datasets |
| `eval` | List, get evaluation results |
| `wait` | Block until a resource reaches terminal state |
| `status` | View system status |
| `regression` | Run repo-tracked regression cases |
| `cluster` | Check or apply Aegis-specific cluster readiness steps |
| `pedestal` | Verify or edit pedestal Helm metadata |
| `completion` | Generate shell completions |

## Output behavior

- `--help` is the discovery surface for examples and flags.
- `--output json` / `-o json` is the machine-readable contract.
- Table output goes to stdout.
- Informational messages go to stderr.
- Dry-run capable validation commands print the request plan instead of
  mutating the API.

## Canonical regression case

```bash
# Submit the curated otel-demo guided regression case
aegisctl regression run otel-demo-guided

# Preflight the local environment and wait for a pass/fail result
aegisctl regression run otel-demo-guided --ensure-env --wait

# CI/agent-friendly summary payload
aegisctl regression run otel-demo-guided --ensure-env --wait --output json
```

## Environment variables

| Variable | Description |
|----------|-------------|
| `AEGIS_SERVER` | Server URL (overridden by `--server`) |
| `AEGIS_TOKEN` | Auth token (overridden by `--token`) |
| `AEGIS_USERNAME` | Username for `aegisctl auth login` |
| `AEGIS_PASSWORD` | Password for `aegisctl auth login` |
| `AEGIS_PASSWORD_FILE` | File containing the password for `aegisctl auth login` |
| `AEGIS_PROJECT` | Default project name (overridden by `--project`) |
| `AEGIS_OUTPUT` | Output format: `table` or `json` (overridden by `-o`) |
| `AEGIS_TIMEOUT` | Request timeout in seconds (overridden by `--request-timeout`) |
| `AEGIS_KEY_ID` | API key ID for `auth login` |
| `AEGIS_KEY_SECRET` | API key secret for `auth login` |

## Related docs

- [`../../../docs/aegisctl-cli-spec.md`](../../../docs/aegisctl-cli-spec.md) - single validation contract reference
- [`../../../../regression/README.md`](../../../../regression/README.md) - repo-tracked regression case format and canonical cases
- [`../../../README.md`](../../../README.md) - backend runtime modes and quick start
- [`../../../../docs/deployment/README.md`](../../../../docs/deployment/README.md) - deploy and validation runbook map
- [`../../../../docs/troubleshooting/README.md`](../../../../docs/troubleshooting/README.md) - cross-repo troubleshooting runbooks

## Cluster readiness commands

`aegisctl cluster` separates verification from repair:

- `aegisctl cluster preflight` checks reachability and configuration only. It
  reports missing prerequisites and has a small set of targeted remediations.
- `aegisctl cluster prepare local-e2e` previews or applies the Aegis-specific
  local/e2e preparation contract (namespace, service account, experiment PVC,
  required etcd keys). It does not wrap generic `kind`, `helm`, or broad
  `kubectl apply` lifecycle workflows.

Examples:

```bash
# Preview intended local/e2e prep actions
aegisctl cluster prepare local-e2e --dry-run

# Apply the Aegis-specific prep contract
aegisctl cluster prepare local-e2e --apply

# Consume a stable machine-readable summary
aegisctl cluster prepare local-e2e --output json
```
