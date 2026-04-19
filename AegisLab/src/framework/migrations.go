package framework

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
// that gorm.AutoMigrate() accepts today in infra/db/migration.go.
// During Phase 3 a module can either continue to appear in that central
// list OR move its entities here; Phase 4 moves them all.
type MigrationRegistrar struct {
	Module   string
	Entities []interface{}
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
