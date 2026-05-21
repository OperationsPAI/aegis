package initialization

import (
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newDynamicConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.DynamicConfig{}, &model.ConfigHistory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestInitializeDynamicConfigs_IdempotentOnSecondBoot pins that re-running the
// declarative seed over an already-populated DB is observationally a no-op:
// no duplicate rows, no spurious UpdatedAt bumps. Replaces the producerSeedRequired
// gate that previously hid this seed behind a `containers`-table check.
func TestInitializeDynamicConfigs_IdempotentOnSecondBoot(t *testing.T) {
	db := newDynamicConfigTestDB(t)
	data := &InitialData{
		DynamicConfigs: []InitialDynamicConfig{
			{
				Key:          "database.clickhouse.host",
				DefaultValue: "clickhouse.monitoring.svc.cluster.local",
				ValueType:    consts.ConfigValueTypeString,
				Scope:        consts.ConfigScopeConsumer,
				Category:     "database.clickhouse",
				Description:  "ClickHouse database host",
			},
		},
	}

	first, err := initializeDynamicConfigs(db, data)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first seed: want 1 row, got %d", len(first))
	}

	var beforeRow model.DynamicConfig
	if err := db.Where("config_key = ?", data.DynamicConfigs[0].Key).First(&beforeRow).Error; err != nil {
		t.Fatalf("lookup pre-second-seed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	second, err := initializeDynamicConfigs(db, data)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second seed: want 1 row returned, got %d", len(second))
	}

	var afterRows []model.DynamicConfig
	if err := db.Where("config_key = ?", data.DynamicConfigs[0].Key).Find(&afterRows).Error; err != nil {
		t.Fatalf("lookup post-second-seed: %v", err)
	}
	if len(afterRows) != 1 {
		t.Fatalf("second seed inserted duplicates: %d rows", len(afterRows))
	}
	if !afterRows[0].UpdatedAt.Equal(beforeRow.UpdatedAt) {
		t.Fatalf("idempotent seed must not touch UpdatedAt: before=%v after=%v", beforeRow.UpdatedAt, afterRows[0].UpdatedAt)
	}
}

// TestInitializeDynamicConfigs_AddsNewRowOnSubsequentBoot pins the additive
// behavior the dropped gate previously blocked: a fresh row in data.yaml lands
// in the DB on the next boot.
func TestInitializeDynamicConfigs_AddsNewRowOnSubsequentBoot(t *testing.T) {
	db := newDynamicConfigTestDB(t)
	first := &InitialData{
		DynamicConfigs: []InitialDynamicConfig{
			{Key: "k1", DefaultValue: "v1", ValueType: consts.ConfigValueTypeString,
				Scope: consts.ConfigScopeConsumer, Category: "test"},
		},
	}
	if _, err := initializeDynamicConfigs(db, first); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	second := &InitialData{
		DynamicConfigs: append(first.DynamicConfigs, InitialDynamicConfig{
			Key: "k2", DefaultValue: "v2", ValueType: consts.ConfigValueTypeString,
			Scope: consts.ConfigScopeConsumer, Category: "test",
		}),
	}
	if _, err := initializeDynamicConfigs(db, second); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var k2 model.DynamicConfig
	if err := db.Where("config_key = ?", "k2").First(&k2).Error; err != nil {
		t.Fatalf("new row k2 not seeded on second boot: %v", err)
	}
	if k2.DefaultValue != "v2" {
		t.Fatalf("k2 default_value: want v2 got %q", k2.DefaultValue)
	}
}
