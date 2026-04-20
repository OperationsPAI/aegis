# Review for issue #79 — PR #88

## Parent PR
- Open PR verified: `gh pr list -R OperationsPAI/aegis --head workbuddy/issue-79 --state open --json number,url -q '.[0]'`
- Result: `{"number":88,"url":"https://github.com/OperationsPAI/aegis/pull/88"}`

## Cascade preconditions
No submodule pointers changed in `origin/main...origin/workbuddy/issue-79`, so there were no cascade preconditions to verify.

**command**: `git diff --submodule=short --stat origin/main...origin/workbuddy/issue-79 && echo '---' && git diff --raw origin/main...origin/workbuddy/issue-79 | awk '$1 ~ /^:/ && $5=="160000" {print}'`
**exit**: 0
**stdout** (first 20 lines):
```text
 AegisLab/src/cmd/aegisctl/README.md               |  25 +++
 AegisLab/src/cmd/aegisctl/cluster/checks_test.go  |  47 +++++
 AegisLab/src/cmd/aegisctl/cluster/env.go          |  16 ++
 AegisLab/src/cmd/aegisctl/cluster/live_env.go     | 106 ++++++++++
 AegisLab/src/cmd/aegisctl/cluster/prepare.go      | 242 ++++++++++++++++++++++
 AegisLab/src/cmd/aegisctl/cluster/prepare_test.go | 117 +++++++++++
 AegisLab/src/cmd/aegisctl/cmd/cluster.go          |  73 +++++++
 docs/troubleshooting/e2e-cluster-bootstrap.md     |   8 +-
 8 files changed, 633 insertions(+), 1 deletion(-)
---
```

## Per-AC verdicts

### AC 1: `aegisctl cluster prepare local-e2e` exists as a first-class command under the noun-verb tree
**verdict**: PASS
**command**: `cd AegisLab && devbox run -- bash -lc 'cd src && go run ./cmd/aegisctl cluster prepare local-e2e --help'`
**exit**: 0
**stdout** (first 20 lines):
```text
Info: Running script "bash" on /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-79/AegisLab
Welcome to devbox!
sync hooks: ✔️ 
local-e2e encodes the Aegis-specific repair/seed/config steps that a fresh
development cluster needs before guided local end-to-end validation.

By default the command previews intended changes without writing. Pass
--apply (or the alias --fix) to perform the actual writes. --dry-run can be
used explicitly to force a no-write preview, and --output json returns a
stable machine-readable summary with create/update/skip outcomes.

Usage:
  aegisctl cluster prepare local-e2e [flags]

Flags:
      --apply           Perform the local/e2e preparation writes instead of previewing them
      --config string   Path to a specific config TOML (defaults to config.$ENV_MODE.toml in cwd)
      --fix             Alias for --apply
  -h, --help            help for local-e2e
      --timeout int     Overall timeout for the local/e2e preparation run in seconds (default 30)
```

### AC 2: The command is idempotent across repeated runs on the same environment
**verdict**: PASS
**command**: `cd AegisLab && devbox run -- bash -lc 'cd src && go test ./cmd/aegisctl/cluster -run TestLocalE2EPrepareRunner_ApplyIsIdempotent$ -v'`
**exit**: 0
**stdout** (first 20 lines):
```text
Info: Running script "bash" on /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-79/AegisLab
Welcome to devbox!
sync hooks: ✔️ 
=== RUN   TestLocalE2EPrepareRunner_ApplyIsIdempotent
--- PASS: TestLocalE2EPrepareRunner_ApplyIsIdempotent (0.00s)
PASS
ok  	aegis/cmd/aegisctl/cluster	0.019s
```

### AC 3: `--dry-run` performs no writes and emits a structured summary of intended actions
**verdict**: PASS
**command**: `rg -n 'PrepareSummary|json:"target"|json:"dry_run"|json:"results"|json:"outcome"|output.PrintJSON\(summary\)|--dry-run can be|stable machine-readable summary' AegisLab/src/cmd/aegisctl/cmd/cluster.go AegisLab/src/cmd/aegisctl/cluster/prepare.go`
**exit**: 0
**stdout** (first 20 lines):
```text
AegisLab/src/cmd/aegisctl/cluster/prepare.go:30:	Outcome     PrepareOutcome `json:"outcome"`
AegisLab/src/cmd/aegisctl/cluster/prepare.go:35:type PrepareSummary struct {
AegisLab/src/cmd/aegisctl/cluster/prepare.go:36:	Target  string          `json:"target"`
AegisLab/src/cmd/aegisctl/cluster/prepare.go:37:	DryRun  bool            `json:"dry_run"`
AegisLab/src/cmd/aegisctl/cluster/prepare.go:38:	Results []PrepareResult `json:"results"`
AegisLab/src/cmd/aegisctl/cmd/cluster.go:51:--apply (or the alias --fix) to perform the actual writes. --dry-run can be
AegisLab/src/cmd/aegisctl/cmd/cluster.go:53:stable machine-readable summary with create/update/skip outcomes.`
AegisLab/src/cmd/aegisctl/cmd/cluster.go:75:		summary := cluster.PrepareSummary{
AegisLab/src/cmd/aegisctl/cmd/cluster.go:82:			output.PrintJSON(summary)
```
**notes**: No-write behavior is additionally covered by `TestLocalE2EPrepareRunner_DryRunDoesNotMutate` in `AegisLab/src/cmd/aegisctl/cluster/prepare_test.go:8`.

### AC 4: `--fix` or `--apply` performs the actual Aegis-specific preparation actions
**verdict**: PASS
**command**: `rg -n 'CreateNamespace|CreateServiceAccount|CreatePVC|Etcd\(\)\.Put|NamespaceExists|ServiceAccountExists|PVCBound|Etcd\(\)\.Get' AegisLab/src/cmd/aegisctl/cluster/prepare.go`
**exit**: 0
**stdout** (first 20 lines):
```text
100:				exists, err := env.K8s().NamespaceExists(ctx, ns)
108:					if err := env.K8s().CreateNamespace(ctx, ns); err != nil {
121:				exists, err := env.K8s().ServiceAccountExists(ctx, ns, sa)
129:					if err := env.K8s().CreateServiceAccount(ctx, ns, sa); err != nil {
142:				exists, bound, err := env.K8s().PVCBound(ctx, ns, pvc)
156:					if err := env.K8s().CreatePVC(ctx, ns, pvc, PVCSpec{StorageClassName: defaultExperimentPVCClass, Size: defaultExperimentPVCSize}); err != nil {
192:				got, exists, err := env.Etcd().Get(ctx, actionKey)
206:					if err := env.Etcd().Put(ctx, actionKey, actionWant); err != nil {
```
**notes**: `TestLocalE2EPrepareRunner_ApplyIsIdempotent` in `AegisLab/src/cmd/aegisctl/cluster/prepare_test.go:52` also verifies that an apply run mutates the fake environment before the second run becomes all-skip.

### AC 5: `--output json` returns a stable machine-readable result that distinguishes create/update/skip outcomes
**verdict**: PASS
**command**: `rg -n 'PrepareCreate|PrepareUpdate|PrepareSkip|json:"outcome"|output.PrintJSON\(summary\)' AegisLab/src/cmd/aegisctl/cmd/cluster.go AegisLab/src/cmd/aegisctl/cluster/prepare.go`
**exit**: 0
**stdout** (first 20 lines):
```text
AegisLab/src/cmd/aegisctl/cluster/prepare.go:22:	PrepareCreate PrepareOutcome = "create"
AegisLab/src/cmd/aegisctl/cluster/prepare.go:23:	PrepareUpdate PrepareOutcome = "update"
AegisLab/src/cmd/aegisctl/cluster/prepare.go:24:	PrepareSkip   PrepareOutcome = "skip"
AegisLab/src/cmd/aegisctl/cluster/prepare.go:30:	Outcome     PrepareOutcome `json:"outcome"`
AegisLab/src/cmd/aegisctl/cluster/prepare.go:105:					return PrepareResult{Outcome: PrepareSkip, Detail: fmt.Sprintf("namespace %q already present", ns)}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:112:				return PrepareResult{Outcome: PrepareCreate, Applied: apply, Detail: fmt.Sprintf("namespace %q", ns)}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:126:					return PrepareResult{Outcome: PrepareSkip, Detail: fmt.Sprintf("ServiceAccount %s/%s already present", ns, sa)}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:133:				return PrepareResult{Outcome: PrepareCreate, Applied: apply, Detail: fmt.Sprintf("ServiceAccount %s/%s", ns, sa)}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:153:					return PrepareResult{Outcome: PrepareSkip, Detail: detail}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:160:				return PrepareResult{Outcome: PrepareCreate, Applied: apply, Detail: fmt.Sprintf("PVC %s/%s using storageClass=%q size=%s", ns, pvc, defaultExperimentPVCClass, defaultExperimentPVCSize)}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:197:					return PrepareResult{Outcome: PrepareSkip, Detail: fmt.Sprintf("%s already set to %q", actionKey, actionWant)}, nil
AegisLab/src/cmd/aegisctl/cluster/prepare.go:199:				outcome := PrepareCreate
AegisLab/src/cmd/aegisctl/cluster/prepare.go:202:					outcome = PrepareUpdate
AegisLab/src/cmd/aegisctl/cmd/cluster.go:82:			output.PrintJSON(summary)
```

### AC 6: The command scope is limited to Aegis-specific readiness work and does not attempt to wrap generic cluster install/lifecycle tooling
**verdict**: PASS
**command**: `rg -n 'intentionally does not wrap generic kind, helm, or kubectl|does not wrap generic `kind`, `helm`, or broad|namespace, service account, experiment PVC, required etcd keys|Aegis-specific local/e2e preparation contract' AegisLab/src/cmd/aegisctl/cmd/cluster.go AegisLab/src/cmd/aegisctl/README.md docs/troubleshooting/e2e-cluster-bootstrap.md`
**exit**: 0
**stdout** (first 20 lines):
```text
AegisLab/src/cmd/aegisctl/README.md:72:- `aegisctl cluster prepare local-e2e` previews or applies the Aegis-specific
AegisLab/src/cmd/aegisctl/README.md:74:  required etcd keys). It does not wrap generic `kind`, `helm`, or broad
AegisLab/src/cmd/aegisctl/cmd/cluster.go:40:This command intentionally does not wrap generic kind, helm, or kubectl
AegisLab/src/cmd/aegisctl/cmd/cluster.go:46:	Short: "Preview or apply the Aegis-specific local/e2e preparation contract",
docs/troubleshooting/e2e-cluster-bootstrap.md:322:- `aegisctl cluster prepare local-e2e --dry-run` — preview the Aegis-specific
docs/troubleshooting/e2e-cluster-bootstrap.md:324:- `aegisctl cluster prepare local-e2e --apply` — apply that Aegis-specific prep
```

### AC 7: Documentation clearly distinguishes `cluster preflight` (check-only) from `cluster prepare` (repair/seed/config apply)
**verdict**: PASS
**command**: `rg -n 'cluster preflight|cluster prepare local-e2e|check-only|checks reachability and configuration only|preview|apply' AegisLab/src/cmd/aegisctl/cmd/cluster.go AegisLab/src/cmd/aegisctl/README.md docs/troubleshooting/e2e-cluster-bootstrap.md`
**exit**: 0
**stdout** (first 20 lines):
```text
AegisLab/src/cmd/aegisctl/cmd/cluster.go:35:	Short: "Preview or apply Aegis-specific cluster preparation flows",
AegisLab/src/cmd/aegisctl/cmd/cluster.go:46:	Short: "Preview or apply the Aegis-specific local/e2e preparation contract",
AegisLab/src/cmd/aegisctl/cmd/cluster.go:50:By default the command previews intended changes without writing. Pass
AegisLab/src/cmd/aegisctl/cmd/cluster.go:102:Use "aegisctl cluster prepare local-e2e" when you want the apply/seed side of
AegisLab/src/cmd/aegisctl/README.md:68:`aegisctl cluster` separates verification from repair:
AegisLab/src/cmd/aegisctl/README.md:70:- `aegisctl cluster preflight` checks reachability and configuration only. It
AegisLab/src/cmd/aegisctl/README.md:72:- `aegisctl cluster prepare local-e2e` previews or applies the Aegis-specific
AegisLab/src/cmd/aegisctl/README.md:81:aegisctl cluster prepare local-e2e --dry-run
AegisLab/src/cmd/aegisctl/README.md:84:aegisctl cluster prepare local-e2e --apply
docs/troubleshooting/e2e-cluster-bootstrap.md:320:- `aegisctl cluster preflight` — check-only validation for the dependency and
docs/troubleshooting/e2e-cluster-bootstrap.md:322:- `aegisctl cluster prepare local-e2e --dry-run` — preview the Aegis-specific
docs/troubleshooting/e2e-cluster-bootstrap.md:324:- `aegisctl cluster prepare local-e2e --apply` — apply that Aegis-specific prep
```

### AC 8: Tests cover idempotency and dry-run behavior for representative checks/actions
**verdict**: PASS
**command**: `cd AegisLab && devbox run -- bash -lc 'cd src && go test ./cmd/aegisctl/cluster -run "TestLocalE2EPrepareRunner_(DryRunDoesNotMutate|ApplyIsIdempotent)$" -v'`
**exit**: 0
**stdout** (first 20 lines):
```text
Info: Running script "bash" on /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-79/AegisLab
Welcome to devbox!
sync hooks: ✔️ 
=== RUN   TestLocalE2EPrepareRunner_DryRunDoesNotMutate
--- PASS: TestLocalE2EPrepareRunner_DryRunDoesNotMutate (0.00s)
=== RUN   TestLocalE2EPrepareRunner_ApplyIsIdempotent
--- PASS: TestLocalE2EPrepareRunner_ApplyIsIdempotent (0.00s)
PASS
ok  	aegis/cmd/aegisctl/cluster	(cached)
```

## Overall
- PASS: 8 / 8
- FAIL: none
- UNVERIFIABLE: none
