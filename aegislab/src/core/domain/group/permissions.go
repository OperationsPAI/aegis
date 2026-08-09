package group

// The group module does not own any PermissionRule declarations or
// default system-role grants. Its portal endpoints are gated by the
// trace module's existing middleware (`RequireTraceRead`), so there is
// no `group:"permissions"` / `group:"role_grants"` contribution to
// register in Phase 4.
