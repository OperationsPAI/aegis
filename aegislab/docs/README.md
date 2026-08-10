# aegislab docs

Operational runbooks and design notes scoped to **this** repo. Cross-repo
content (datapack schema, benchmark playbooks, app-label conventions)
lives at `../../docs/` — see [`../../docs/troubleshooting/README.md`](../../docs/troubleshooting/README.md).

| File | Topic | When to read |
|------|-------|--------------|
| [`aegisctl-share.md`](./aegisctl-share.md) | `aegisctl share upload` three-step flow (init → presign PUT → commit), streaming sha256, `--legacy` fallback | Diagnosing slow / failed share uploads; designing client retries; understanding `share_links.lifecycle_state` |
| [`dev-intercept.md`](./dev-intercept.md) | Laptop intercept via Telepresence v2 — `rcabench-dev-{frontend,api}` anchor Services and the `/dev/*` edge-proxy routes that feed them | Setting up a local intercept; debugging why `/dev/*` traffic isn't reaching your laptop |
| [`edge-proxy-host-aliasing.md`](./edge-proxy-host-aliasing.md) | Adding extra EIPs / forwarders / accelerator endpoints to the Caddy edge-proxy without restarting the pod; the `200 content-length: 0` host-mismatch symptom | New EIP returns empty 200 while direct EIP returns real content; CDN / Global Accelerator front-end fan-out |

## Conventions

- One topic per file. Cross-link instead of duplicating.
- Lead with the symptom or use-case, not the implementation. The file is
  worth reading only when the reader's situation matches the top three
  lines.
- Document the **wrong** turn too. A doc that says "we tried `:38082`
  first and Caddy started a new listener that wasn't reachable" saves
  the next person from repeating the misstep.

## Related indexes

- [`../README.md`](../README.md) — repo root, build / dev / deployment entry points
- [`../src/cli/README.md`](../src/cli/README.md) — `aegisctl` CLI surface and command tree
- [`../helm/README.md`](../helm/README.md) — chart layout, values surface
- [`../regression/README.md`](../regression/README.md) — regression / load harnesses
- [`../../docs/troubleshooting/README.md`](../../docs/troubleshooting/README.md) — cross-repo runbooks (datapack, benchmarks, app-labels)
