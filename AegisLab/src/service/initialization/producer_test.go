package initialization

import (
	"testing"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newProducerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.DynamicConfig{}, &model.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestProducerSeedRequired_IgnoresDynamicConfigsWithoutAdminUser(t *testing.T) {
	db := newProducerTestDB(t)

	if err := db.Create(&model.DynamicConfig{
		Key:          "database.clickhouse.host",
		DefaultValue: "clickhouse.monitoring.svc.cluster.local",
		ValueType:    consts.ConfigValueTypeString,
		Scope:        consts.ConfigScopeConsumer,
		Category:     "database.clickhouse",
		Description:  "ClickHouse database host",
	}).Error; err != nil {
		t.Fatalf("seed dynamic config: %v", err)
	}

	required, err := producerSeedRequired(db, &InitialData{
		AdminUser: InitialDataUser{Username: "admin"},
	})
	if err != nil {
		t.Fatalf("producerSeedRequired: %v", err)
	}
	if !required {
		t.Fatalf("producer seed should still be required when only dynamic configs exist")
	}
}

func TestProducerSeedRequired_SkipsWhenAdminUserExists(t *testing.T) {
	db := newProducerTestDB(t)

	if err := db.Omit("active_username").Create(&model.User{
		Username: "admin",
		Email:    "admin@rcabench.local",
		Password: "admin123",
		FullName: "System Admin",
		IsActive: true,
		Status:   consts.CommonEnabled,
	}).Error; err != nil {
		t.Fatalf("seed admin user: %v", err)
	}

	required, err := producerSeedRequired(db, &InitialData{
		AdminUser: InitialDataUser{Username: "admin"},
	})
	if err != nil {
		t.Fatalf("producerSeedRequired: %v", err)
	}
	if required {
		t.Fatalf("producer seed should be skipped after the admin user exists")
	}
}
