package framework

import "gorm.io/gorm"

// MigrationRegistrar is what a module contributes for schema
// self-registration.
//
// A module provides it with:
//
//	fx.Provide(
//	    fx.Annotate(module.Migrations, fx.ResultTags(`group:"migrations"`)),
//	)
//
// `Entities` is the list the module owns — exactly the kind of pointer
// that gorm.AutoMigrate() accepts.
type MigrationRegistrar struct {
	Module   string
	Entities []interface{}
	// PreMigrate runs before AutoMigrate sees this module's entities.
	// Use it for one-shot SQL fixups that AutoMigrate cannot express
	// (e.g. dropping a unique index whose column set changed). Must be
	// idempotent — it is invoked on every boot.
	PreMigrate func(*gorm.DB) error
}

// FlattenPreMigrates returns the non-nil PreMigrate hooks in contribution
// order so callers can run them before the AutoMigrate sweep.
func FlattenPreMigrates(contribs []MigrationRegistrar) []func(*gorm.DB) error {
	out := make([]func(*gorm.DB) error, 0, len(contribs))
	for _, c := range contribs {
		if c.PreMigrate != nil {
			out = append(out, c.PreMigrate)
		}
	}
	return out
}

// FlattenMigrations concatenates every contributed entity list. Order is
// contribution order — gorm's AutoMigrate is order-independent for
// tables within the same call, so duplicates across the central list and
// this flatten are harmless (AutoMigrate is idempotent).
func FlattenMigrations(contribs []MigrationRegistrar) []interface{} {
	total := 0
	for _, c := range contribs {
		total += len(c.Entities)
	}
	out := make([]interface{}, 0, total)
	for _, c := range contribs {
		out = append(out, c.Entities...)
	}
	return out
}
