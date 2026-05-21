package initialization

import (
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestInitializeAdminUser_SkipsCreateWhenAdminAlreadyExists pins the SSO
// re-boot path: an admin row with a real (non-empty) password is already in
// the DB. ConvertToDBUser builds a Password="" model.User; if Create runs,
// User.BeforeCreate calls crypto.HashPassword("") which rejects strings <8
// chars and fails the boot before the dup-key path is reachable. So the
// re-boot path MUST look the user up first, not rely on ErrAlreadyExists.
func TestInitializeAdminUser_SkipsCreateWhenAdminAlreadyExists(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Role{}, &model.UserRole{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// SSO bootstrap path: real password, real row, predates the seed run.
	preExisting := &model.User{
		Username: AdminUsername,
		Email:    "admin@aegis.local",
		Password: "a-real-bcrypt-hash-from-sso-bootstrap",
		FullName: "Admin",
		IsActive: true,
		Status:   consts.CommonEnabled,
	}
	if err := db.Omit("active_username").Create(preExisting).Error; err != nil {
		t.Fatalf("seed pre-existing admin: %v", err)
	}

	if err := db.Omit("active_name").Create(&model.Role{Name: string(consts.RoleSuperAdmin), Status: consts.CommonEnabled}).Error; err != nil {
		t.Fatalf("seed super_admin role: %v", err)
	}

	store := newBootstrapStore(db)
	data := &InitialData{
		AdminUser: InitialDataUser{
			Username: AdminUsername,
			Email:    "admin@aegis.local",
			FullName: "Admin",
			IsActive: true,
			Status:   consts.CommonEnabled,
		},
	}

	got, err := initializeAdminUser(store, data)
	if err != nil {
		t.Fatalf("initializeAdminUser on re-boot: %v", err)
	}
	if got.ID != preExisting.ID {
		t.Fatalf("returned user ID: want pre-existing %d, got %d (Create ran?)", preExisting.ID, got.ID)
	}
	if got.Password != preExisting.Password {
		t.Fatalf("admin password was clobbered: want %q, got %q", preExisting.Password, got.Password)
	}

	var count int64
	if err := db.Model(&model.User{}).Where("username = ?", AdminUsername).Count(&count).Error; err != nil {
		t.Fatalf("count admin rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 admin row, got %d", count)
	}
}

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
