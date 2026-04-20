# aegisctl

Command-line client for the AegisLab (RCABench) platform. Designed for both human operators and AI agents to drive the RCA workflow from the terminal.

For the automation-facing CLI contract used by CI and agent workflows, see
`docs/aegisctl-cli-contract.md`.

## Build

```bash
just build-aegisctl
# or manually:
cd src && go build -o /tmp/aegisctl ./cmd/aegisctl
```

Note: `aegisctl` does not require `-tags duckdb_arrow`.

## Quick Start

```bash
# Login
aegisctl auth login --server http://HOST:8082 --username admin --password admin123

# Set default project
aegisctl context set --name default --default-project pair_diagnosis

# Browse resources
aegisctl project list
aegisctl container list
aegisctl dataset list
```

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
| `completion` | Generate shell completions |

## Canonical regression case

```bash
# Submit the curated otel-demo guided regression case
aegisctl regression run otel-demo-guided

# Preflight the local environment and wait for a pass/fail result
aegisctl regression run otel-demo-guided --ensure-env --wait

# CI/agent-friendly summary payload
aegisctl regression run otel-demo-guided --ensure-env --wait --output json
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `AEGIS_SERVER` | Server URL (overridden by `--server`) |
| `AEGIS_TOKEN` | Auth token (overridden by `--token`) |
| `AEGIS_PROJECT` | Default project name (overridden by `--project`) |
| `AEGIS_OUTPUT` | Output format: `table` or `json` (overridden by `-o`) |
| `AEGIS_TIMEOUT` | Request timeout in seconds (overridden by `--request-timeout`) |

## Related docs

- [`../../../../regression/README.md`](../../../../regression/README.md) - repo-tracked regression case format and canonical cases
- [`../../../README.md`](../../../README.md) - backend runtime modes and quick start
- [`../../../CONTRIBUTING.md`](../../../CONTRIBUTING.md) - module/plugin boundary rules
- [`../../../../docs/deployment/README.md`](../../../../docs/deployment/README.md) - deploy and smoke-test map
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
