package chaos

import (
	"time"

	"aegis/platform/framework"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Migrations: table names are prefixed `chaos_` because aegis-chaos
// shares MySQL with the monolith aegislab, whose pre-existing
// `system_metadata` and adjacent tables would collide with bare
// `systems` etc. from the §7 SQL sketch.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "chaos",
		Entities: []any{
			&System{},
			&Service{},
			&ImportLock{},
			&Capability{},
			&Point{},
			&ExecutorRecord{},
			&InjectionBatch{},
			&Injection{},
		},
		PreMigrate: addPointSysUpdatedAtIndex,
	}
}

// addPointSysUpdatedAtIndex installs the composite index that backs the
// MAX(updated_at) probe driving resourcelookup's cross-process cache
// invalidation (#459). Running this in PreMigrate with explicit
// ALGORITHM=INPLACE, LOCK=NONE keeps the upgrade non-blocking on production
// — gorm's AutoMigrate would otherwise issue CREATE INDEX without those
// qualifiers and stall writers while the index builds. Idempotent: skips
// when the table is absent (greenfield) or the index already exists.
// SQLite (test) skips entirely; AutoMigrate's tag-derived DDL handles it.
func addPointSysUpdatedAtIndex(db *gorm.DB) error {
	if db.Name() != "mysql" {
		return nil
	}
	mig := db.Migrator()
	if !mig.HasTable("chaos_points") {
		return nil
	}
	var existing int
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE()
		  AND table_name = 'chaos_points'
		  AND index_name = 'idx_point_sys_updated_at'`).Scan(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	return db.Exec(
		"CREATE INDEX idx_point_sys_updated_at ON chaos_points (system_name, updated_at) ALGORITHM=INPLACE, LOCK=NONE",
	).Error
}

// SeedCapabilities inserts the step-1 Capability set. Conservative shape:
// pod_kill targets a pod-selector via {namespace, app}; params carry a
// duration_s knob; the observable contract asserts that the targeted pod
// restarted within the injection window.
func SeedCapabilities(db *gorm.DB) error {
	seed := []Capability{
		{
			Name:               "pod_kill",
			TargetSchema:       podKillTargetSchema(),
			ParamSchema:        podKillParamSchema(),
			ObservableContract: podKillObservableContract(),
			Status:             CapStable,
			CreatedAt:          time.Now().UTC(),
		},
	}
	seed = append(seed, SeedsNetwork...)
	seed = append(seed, SeedsPodChaosExtra...)
	seed = append(seed, SeedsStress...)
	seed = append(seed, SeedsTime...)
	seed = append(seed, SeedsDNS...)
	seed = append(seed, SeedsHTTP...)
	seed = append(seed, SeedsJVM...)
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"target_schema", "param_schema", "observable_contract", "status",
		}),
	}).Create(&seed).Error
}

func podKillTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
		},
	}
}

func podKillParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"duration_s": map[string]any{
				"type":    "integer",
				"minimum": 1,
				"maximum": 600,
				"default": 60,
			},
		},
	}
}

func podKillObservableContract() JSONMap {
	return JSONMap{
		"name": "pod_kill",
		"contract": map[string]any{
			"k8s_assertions": []any{
				map[string]any{"assertion": "target_pod.restart_count increases by >= 1 within injection_window_s"},
			},
			"injection_window_s": 60,
			"tolerance":          map[string]any{"false_positive_rate": 0.05},
		},
	}
}
