package initialization

import (
	"testing"
	"time"

	"aegis/platform/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newServiceAccountTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ServiceAccount{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestSeedServiceAccounts_AddsRowOnSubsequentBoot pins the bug the unconditional
// seed call exists to fix: producerSeedRequired returns false once `containers`
// is non-empty, so without an unconditional upsert path, a fresh SA row added
// to data.yaml after first boot would never reach the DB.
func TestSeedServiceAccounts_AddsRowOnSubsequentBoot(t *testing.T) {
	db := newServiceAccountTestDB(t)

	first := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}
	if err := seedServiceAccounts(db, first); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	second := append(first, InitialDataServiceAccount{
		Name: "fault-monitor", Scopes: "chaos.read", Description: "monitor",
	})
	if err := seedServiceAccounts(db, second); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var sa model.ServiceAccount
	if err := db.Where("name = ?", "fault-monitor").First(&sa).Error; err != nil {
		t.Fatalf("lookup fault-monitor: %v", err)
	}
	if sa.Scopes != "chaos.read" {
		t.Fatalf("new SA not seeded: %+v", sa)
	}
}

// TestSeedServiceAccounts_PreservesManualRevocation pins the revoked_at column
// being absent from the OnConflict update list — without that omission, a
// re-seed would silently undo an operator's manual revocation.
func TestSeedServiceAccounts_PreservesManualRevocation(t *testing.T) {
	db := newServiceAccountTestDB(t)
	accounts := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}
	if err := seedServiceAccounts(db, accounts); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	revoked := time.Now().UTC().Truncate(time.Second)
	if err := db.Model(&model.ServiceAccount{}).Where("name = ?", "chaos-client").
		Update("revoked_at", revoked).Error; err != nil {
		t.Fatalf("set revoked_at: %v", err)
	}

	if err := seedServiceAccounts(db, accounts); err != nil {
		t.Fatalf("post-revoke seed: %v", err)
	}

	var sa model.ServiceAccount
	if err := db.Where("name = ?", "chaos-client").First(&sa).Error; err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if sa.RevokedAt == nil {
		t.Fatalf("revoked_at was cleared by seed — manual revocation must stick")
	}
	if !sa.RevokedAt.Equal(revoked) {
		t.Fatalf("revoked_at drifted: want %v got %v", revoked, *sa.RevokedAt)
	}
}

// TestSeedServiceAccounts_IdempotentOnUnchangedData pins the GORM-version-sensitive
// guarantee that clause.AssignmentColumns(["scopes","description"]) excludes
// updated_at from the SET — a re-seed over unchanged rows must not bump
// UpdatedAt, otherwise observers watching that column for real changes get
// false positives every boot.
func TestSeedServiceAccounts_IdempotentOnUnchangedData(t *testing.T) {
	db := newServiceAccountTestDB(t)
	accounts := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}
	if err := seedServiceAccounts(db, accounts); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	var first model.ServiceAccount
	if err := db.Where("name = ?", "chaos-client").First(&first).Error; err != nil {
		t.Fatalf("lookup first: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if err := seedServiceAccounts(db, accounts); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	var second model.ServiceAccount
	if err := db.Where("name = ?", "chaos-client").First(&second).Error; err != nil {
		t.Fatalf("lookup second: %v", err)
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("idempotent seed must not touch UpdatedAt: before=%v after=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

func TestSeedServiceAccounts_UpdatesScopesAndDescription(t *testing.T) {
	db := newServiceAccountTestDB(t)
	if err := seedServiceAccounts(db, []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	if err := seedServiceAccounts(db, []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write,chaos.webhook.write", Description: "v2"},
	}); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var sa model.ServiceAccount
	if err := db.Where("name = ?", "chaos-client").First(&sa).Error; err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if sa.Scopes != "chaos.inject.write,chaos.webhook.write" {
		t.Fatalf("scopes not updated: %q", sa.Scopes)
	}
	if sa.Description != "v2" {
		t.Fatalf("description not updated: %q", sa.Description)
	}
}

func TestSeedServiceAccounts_EmptyNameRejected(t *testing.T) {
	db := newServiceAccountTestDB(t)
	if err := seedServiceAccounts(db, []InitialDataServiceAccount{{Name: ""}}); err == nil {
		t.Fatalf("expected error for empty name, got nil")
	}
}
