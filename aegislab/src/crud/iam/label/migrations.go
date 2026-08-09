package label

import (
	"aegis/platform/framework"
	"aegis/platform/model"
)

// Migrations is the label module's MigrationRegistrar. It owns the
// `labels` table via model.Label. Previously this entity was in the
// central slice in infra/db/migration.go; it is REMOVED from that
// slice in the same commit that lands this file.
//
// Note: join-table entities (ContainerLabel, DatasetLabel,
// ProjectLabel, FaultInjectionLabel, ExecutionInjectionLabel,
// ConfigLabel) are NOT owned by the label module — they belong to
// their parent entity's module (container, dataset, project, injection,
// execution, system). Phase 4 moves each join row alongside its parent.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "label",
		Entities: []interface{}{&model.Label{}},
	}
}
