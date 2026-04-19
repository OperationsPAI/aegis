package sdk

// The SDK module intentionally contributes no MigrationRegistrar.
//
// Its read-only GORM models map to external SDK-managed tables (`data` and
// `evaluation_data`), and model comments explicitly forbid adding them to
// AutoMigrate. The Phase 4 module layout keeps this file as the canonical
// place to document that decision.
