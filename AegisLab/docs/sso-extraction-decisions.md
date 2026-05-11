# SSO Extraction — Autonomous Decisions Log

Decisions made during PR-1 multi-agent rollout, per long-horizon escalation ladder.
Read when reviewing PR-1 to understand why things landed the way they did.

## 2026-05-11

### L2 (codebase research)

- **Wave 1-2 worktree branches partially leaked across agents.** Agents
  saw each other's uncommitted WIP in their worktree base. Mitigation:
  each agent's commit scoped tightly to declared files; coordination
  happened via the design doc (single source of truth). Result: ff or
  trivial ort merges; no real conflicts.
- **Wave 2 agent-adminapi pre-claimed `module/sso/module.go` for both
  its own handlers AND agent-oidc's not-yet-committed symbols.** The
  shared design doc made the contract explicit; when agent-oidc landed,
  it filled the symbols agent-adminapi had referenced and build went green.
- **`/v1/clients/{id}:rotate` → `/v1/clients/{id}/rotate`.** Gin path
  parser rejects colon-action on a param segment. Semantically equivalent.

### L3 (external research)

- **Skipped `zitadel/oidc/v3` library** (despite design doc specifying it).
  agent-oidc evaluated v3.47.5: 5+ Storage interfaces + AuthRequest
  persistence + SigningKey/KeySet adapters — heavier than PR-1's OP surface.
  Handwrote endpoints using existing `jwtkeys.Signer` + `utils.GenerateToken`.
  Spec-compatible for internal services. Re-evaluate when first 3rd-party
  client connects.

### L4 (north-star reasoning, [flagged])

- **[flagged]** `model.Resource` kept (audit_logs + rbac admin endpoints
  reference it). Originally intended for permission internals; now a
  generic taxonomy. Design doc §13 should clarify.
- **[flagged]** ssoclient caches BOTH allowed and denied check results for
  30s. Permission revoke takes up to 30s for victim. Acceptable per design
  §1 non-goal: webhook invalidation deferred.
- **[flagged]** Audit log writes use `_ =` (best-effort). If MySQL is down,
  writes silently vanish. Availability > completeness. Can detect gaps via
  request logs.
- **[flagged]** `/v1/users/{id}` returns 404 (not 403) when service admin
  asks about out-of-scope user. Privacy choice — don't leak user existence.

### L4 (Wave 4b cleanup gaps)

- **[flagged]** `module/team` retains TWO direct reads of SSO tables:
  `listTeamMembers` (joins `user_scoped_roles`) and `addMember` (looks up
  `model.User` by username). Refactoring requires new SSO endpoints
  (username→ID lookup, scope-member listing with user details) not yet
  built. Accept the gap; PR-2 should add the endpoints AND refactor
  these two sites. The "AegisLab never reads SSO tables" invariant has
  TWO documented exceptions until then.
- **[flagged]** `service/initialization` still seeds users/roles into MySQL
  from BOTH the SSO process AND the AegisLab producer init path (shared
  module). The producer-side seed is now dead-code-on-the-DB-side because
  SSO seeded first; not yet removed. PR-2 should split: SSO seeds
  identity, AegisLab seeds business-only.

### L4 (Wave 4b: pre-existing test failures verified, not regressions)

- `aegis/app/...` tests need `/etc/aegis/sso-private.pem` in test env.
  PR-2 should add a `testmain_test.go` that generates ephemeral RSA + sets
  the path, or shift those tests to integration.
- `aegis/module/.../TestModulePackagesAvoidForeignRepositoryConstructors`
  flags `module/sso` importing `rbac.Repository`. Boundary lint hit
  introduced when /v1/check shared rbac's CheckPermission. PR-2 options:
  widen lint allowlist OR extract `CheckPermission` into a shared
  `module/rbacquery` package.

## Things I did NOT decide alone

- **Frontend OIDC integration** — user explicitly opted out ("除了前端").
  Task #11 deleted. Browser clients will not work end-to-end until
  someone wires up OIDC in AegisLab-frontend.
- **Strategic deviation from "all consumers go through SSO"** — the two
  team-module gaps above. Documented but not silently shipped.
