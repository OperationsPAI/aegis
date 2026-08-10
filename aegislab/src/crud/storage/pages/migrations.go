package pages

import "aegis/platform/framework"

// Migrations registers the page_sites table.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "pages",
		Entities: []any{&PageSite{}},
	}
}
