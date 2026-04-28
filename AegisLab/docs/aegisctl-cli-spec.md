# aegisctl validation contract

This document is the single contract reference for the repo-supported local and
end-to-end validation path driven by `aegisctl`.

It is intentionally narrow: it documents the supported operator and automation
workflow that already exists in the repo. It does not introduce new validation
features beyond the current CLI surfaces.

## Scope and boundary

There are two different layers in the local developer workflow:

1. Kubernetes-native install/bootstrap steps
   - kind cluster creation
   - Helm / manifests / `kubectl` setup
   - service port-forwards
   - image mirroring and one-off cluster repair work
2. Aegis-specific validation steps
   - authenticate against the AegisLab API with `aegisctl`
   - verify API reachability and backend readiness
   - verify pedestal Helm metadata before restart / injection work
   - submit and monitor the supported validation workload contract

Use Kubernetes-native tools to make the environment exist. Use `aegisctl` to
validate AegisLab behavior once the environment exists.

That means:

- `kubectl`, `helm`, and deployment runbooks remain the source of truth for
  cluster bring-up.
- `aegisctl` is the source of truth for post-install validation of auth,
  readiness, preparation, submission, and machine-readable operator output.
- Archived docs that show raw HTTP requests or direct CRD application are
  historical evidence, not the normal supported validation path.

## Supported validation flow

### 0. Build the CLI

```bash
cd AegisLab
just build-aegisctl output=./bin/aegisctl
```

`aegisctl` does not require the `duckdb_arrow` build tag.

### 1. Authenticate

Supported login path is API-key exchange, not username/password.

```bash
./bin/aegisctl auth login \
  --server http://127.0.0.1:8082 \
  --key-id "$AEGIS_KEY_ID" \
  --key-secret "$AEGIS_KEY_SECRET" \
  -o json
```

Contract:

- saves the bearer token into `~/.aegisctl/config.yaml`
- writes machine-readable result data to stdout when `-o json` is used
- exits `0` on success, `1` on CLI / network / API failure

Follow-up inspection:

```bash
./bin/aegisctl auth status -o json
```

Expected JSON fields include `context`, `server`, `status`, `auth_type`,
`key_id`, and `expires_at`.

### 2. Check readiness

Use `aegisctl status` for API-level readiness:

```bash
./bin/aegisctl status -o json
```

Contract:

- `connected=true` means the CLI could fetch `/api/v2/auth/profile`
- `health` mirrors `/api/v2/system/health`
- unreachable health does **not** hard-fail the command; the failure is encoded
  in the JSON payload as `health.status=unreachable`
- table output goes to stdout; informational text goes to stderr

Use `aegisctl cluster preflight` for cluster dependency checks:

```bash
./bin/aegisctl cluster preflight
./bin/aegisctl cluster preflight --check redis.token-bucket-leaks
```

Contract:

- exit `0` when every executed check is OK
- exit `1` when any executed check fails
- this command validates Kubernetes-native dependencies directly; it is not an
  HTTP API readiness probe

### 3. Prepare the pedestal / benchmark metadata

Use the CLI-first prepare path instead of editing DB rows or invoking Helm by
hand for the normal validation workflow:

```bash
./bin/aegisctl pedestal helm verify --container-version-id 42 -o json
```

Contract:

- performs the repo-supported dry verification pass for the `helm_configs` row
- returns JSON `{ok, checks[]}` with `-o json`
- exits `0` when the verify pass succeeds, `1` when any verify check fails
- `--dry-run` prints the intended request contract without contacting the API

### 4. Create or inspect the validation workload spec

For the supported CLI-first flow, use `inject guided` to build the validation
payload locally and `--apply` only when the session is ready:

```bash
./bin/aegisctl inject guided --reset-config --no-save-config
./bin/aegisctl inject guided --next otel-demo0 --next frontend
./bin/aegisctl inject guided --apply \
  --project pair_diagnosis \
  --pedestal-name ts --pedestal-tag 1.0.0 \
  --benchmark-name otel-demo-bench --benchmark-tag 1.0.0 \
  --interval 10 --pre-duration 5
```

Contract:

- without `--apply`, the command is local-only session orchestration; it emits
  JSON or YAML to stdout and does not hit the API
- with `--apply`, the command submits the finalized envelope to
  `/api/v2/projects/{id}/injections/inject`
- with `--apply --dry-run`, the command prints the resolved request body instead
  of submitting it

Guided `--apply` is the only supported submission path. The backend accepts
guided configs exclusively; there is no YAML-spec `inject submit` command.

### 5. Wait for completion and inspect outputs

```bash
./bin/aegisctl wait <trace-id> --timeout 600 --interval 5
```

`wait` exit codes are stable and scriptable:

- `0`: completed successfully
- `2`: failed / error / cancelled terminal state
- `3`: timeout

### 6. Run algorithm-side validation / regression execution

For execution requests driven through the Aegis API:

```bash
./bin/aegisctl execute create --project pair_diagnosis --input ./execution.yaml -o json
```

Contract:

- accepts the checked-in YAML schema documented by `execute create --help`
- writes the API response body to stdout as JSON
- exits `0` on accepted submission, `1` on CLI / API / spec errors
- `--dry-run` prints the resolved request contract without sending the POST

For the broader repo-native regression suite, use:

```bash
cd AegisLab
just test-regression
```

This remains the repo's broader regression harness. The `aegisctl` contract here
is the supported CLI validation path around auth, readiness, prepare, submit,
and machine-readable command behavior.

## Output contract

Across the validation workflow:

- `--help` is the contract surface for humans, CI, and agents to discover flags
  and examples.
- `-o json` / `--output json` is the contract surface for machine-readable
  parsing.
- table output goes to stdout.
- informational messages go to stderr.
- commands that explicitly support `--dry-run` print the resolved request plan
  and skip the mutating API call.

## Stable validation surfaces

These are the command surfaces this document treats as stable for validation:

- `aegisctl auth login`
- `aegisctl auth status`
- `aegisctl status`
- `aegisctl cluster preflight`
- `aegisctl pedestal helm verify`
- `aegisctl inject guided`
- `aegisctl wait`
- `aegisctl execute create`

If behavior for one of these commands changes, update this file and the command
coverage in `src/cmd/aegisctl/cmd/*_test.go` in the same change.
