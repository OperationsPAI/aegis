package group

// The group module does not own any database tables. It derives group
// statistics and SSE stream events from trace records plus Redis stream
// data, so there is no `group:"migrations"` contribution to register
// in Phase 4.
