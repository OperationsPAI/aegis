# aegisctl CLI Contract

This document defines the automation-facing contract for `aegisctl` commands
used in CI, scripts, and agent-driven workflows. It is the baseline that new
commands such as `cluster prepare` and `regression run` must inherit.

## Global automation mode

- `aegisctl --non-interactive ...` enables fail-fast, prompt-free execution.
- `AEGIS_NON_INTERACTIVE=true` provides the same behavior through the
  environment.
- Automation-facing commands must never pause for hidden stdin input when
  required values are missing. They must exit with a deterministic non-zero
  code and a stderr diagnostic instead.

## Stdout / stderr contract

- `stdout` is reserved for business data only.
- `stderr` is reserved for progress updates, warnings, and diagnostics.
- Commands that support `--output json` must keep `stdout` as valid JSON even
  while they are running.
- Human-readable tables are business data and therefore also belong on
  `stdout`.

## Exit codes

These codes apply to validation-oriented and automation-facing command paths:

| Code | Meaning | Typical examples |
| --- | --- | --- |
| `0` | Success | `auth login` succeeds, `trace get` returns data, `wait` completes successfully |
| `2` | Usage / validation error | Missing required flags or arguments, invalid `--check`, missing `--project` |
| `3` | Authentication / authorization failure | Missing token for an authenticated command, API returns `401` / `403` |
| `4` | Missing environment / dependency | `cluster preflight` finds required dependencies unavailable, missing config or required runtime environment |
| `5` | Terminal workflow failure | `wait` reaches `Failed`, `Error`, or `Cancelled` |
| `6` | Timeout | `wait` exceeds `--timeout` |

## Covered commands

The contract is currently enforced and covered for the following commands:

- `auth login`
- `cluster preflight`
- `inject guided`
- `wait`
- `trace get`

## Command-specific notes

### `auth login`

- Requires an explicit server plus credentials from flags or environment:
  `--server`, `--key-id`, `--key-secret` (or `AEGIS_KEY_ID`,
  `AEGIS_KEY_SECRET`).
- On validation failure it exits with code `2` and prints the reason on
  `stderr`.

### `cluster preflight`

- Emits the result table on `stdout`.
- Uses exit code `4` when a prerequisite is missing or failing.
- Uses exit code `2` for CLI validation issues such as an unknown `--check`
  value.

### `inject guided`

- The guided session response is emitted on `stdout` as JSON or YAML.
- `--apply` fails fast when envelope inputs such as `--project`,
  `--pedestal-tag`, or `--benchmark-tag` are missing.

### `wait`

- Prints progress messages to `stderr`.
- Prints only the terminal payload to `stdout`; with `--output json`, stdout
  stays parseable JSON.
- Uses exit code `5` for terminal workflow failure and `6` for timeout.

### `trace get`

- Prints trace business data to `stdout`.
- Authentication failures surface through exit code `3`.
