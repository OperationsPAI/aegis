package pedestal

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

// Migrations contributes the pedestal-owned helm_configs table. The
// helm_config_values join table stays with its parent-module migration policy
// and is intentionally not moved here in this Phase 4 step.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "pedestal",
		Entities: []interface{}{&model.HelmConfig{}},
	}
}
