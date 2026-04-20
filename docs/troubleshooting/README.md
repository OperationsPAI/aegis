# Aegis Troubleshooting Docs

Cross-repo runbooks for problems that span AegisLab + chaos-experiment +
rcabench-platform. Kept at the repo-root `aegis/docs/` level because the
fixes touch multiple submodules.

Start with [`../../AegisLab/docs/aegisctl-cli-spec.md`](../../AegisLab/docs/aegisctl-cli-spec.md) for the supported CLI-first validation path. Use the troubleshooting pages below when that contract fails in a real cluster or dev environment.

| File | Purpose |
|------|---------|
| [`e2e-cluster-bootstrap.md`](./e2e-cluster-bootstrap.md) | Fresh-cluster runbook: from kind up to a populated datapack, with every known pitfall inline |
| [`e2e-repair-record-2026-04-20.md`](./e2e-repair-record-2026-04-20.md) | Repair log for the 2026-04-20 full-flow validation after `git pull`, including code fixes, cluster workarounds, and the final successful trace |
| [`datapack-schema.md`](./datapack-schema.md) | The 12 parquets + 3 JSON files a datapack is *required* to contain, plus column-level notes (TZ, namespaces, etc.) |
| [`app-label-key.md`](./app-label-key.md) | Per-system `AppLabelKey` design — why `otel-demo` uses `app.kubernetes.io/name` while `ts` uses `app` |

Short-form pitfalls index lives in the auto-memory at
`~/.claude/projects/-home-ddq-AoyangSpace-aegis/memory/aegislab_e2e_pitfalls.md`.
