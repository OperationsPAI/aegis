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

func getSA(t *testing.T, db *gorm.DB, name string) model.ServiceAccount {
	t.Helper()
	var sa model.ServiceAccount
	if err := db.Where("name = ?", name).First(&sa).Error; err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	return sa
}

func TestReconcileServiceAccounts_InsertsOnFirstSeed(t *testing.T) {
	db := newServiceAccountTestDB(t)
	accounts := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write,chaos.webhook.write", Description: "chaos sidecar"},
	}
	if err := ReconcileServiceAccounts(db, accounts); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	sa := getSA(t, db, "chaos-client")
	if sa.Scopes != accounts[0].Scopes || sa.Description != accounts[0].Description {
		t.Fatalf("row not seeded as expected: %+v", sa)
	}
}

func TestReconcileServiceAccounts_IdempotentOnUnchangedData(t *testing.T) {
	db := newServiceAccountTestDB(t)
	accounts := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}
	if err := ReconcileServiceAccounts(db, accounts); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	first := getSA(t, db, "chaos-client")

	// Force a measurable gap so UpdatedAt is observable if a write sneaks in.
	time.Sleep(10 * time.Millisecond)

	if err := ReconcileServiceAccounts(db, accounts); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	second := getSA(t, db, "chaos-client")
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("idempotent reconcile should not touch UpdatedAt: before=%v after=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

func TestReconcileServiceAccounts_AddsNewRowOnSubsequentBoot(t *testing.T) {
	db := newServiceAccountTestDB(t)

	// First boot — only one SA in data.yaml.
	first := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}
	if err := ReconcileServiceAccounts(db, first); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Second boot — a brand-new SA appears in data.yaml. This is the bug fix:
	// before Task #44 producerSeedRequired returned false (containers table
	// non-empty) and this row was lost.
	second := append(first, InitialDataServiceAccount{
		Name: "fault-monitor", Scopes: "chaos.read", Description: "monitor",
	})
	if err := ReconcileServiceAccounts(db, second); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	sa := getSA(t, db, "fault-monitor")
	if sa.Scopes != "chaos.read" {
		t.Fatalf("new SA not seeded: %+v", sa)
	}
}

func TestReconcileServiceAccounts_PreservesManualRevocation(t *testing.T) {
	db := newServiceAccountTestDB(t)
	accounts := []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}
	if err := ReconcileServiceAccounts(db, accounts); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Operator manually revokes the SA out-of-band.
	revoked := time.Now().UTC().Truncate(time.Second)
	if err := db.Model(&model.ServiceAccount{}).Where("name = ?", "chaos-client").
		Update("revoked_at", revoked).Error; err != nil {
		t.Fatalf("set revoked_at: %v", err)
	}

	// Reconcile again — data.yaml is silent about revocation.
	if err := ReconcileServiceAccounts(db, accounts); err != nil {
		t.Fatalf("post-revoke reconcile: %v", err)
	}

	sa := getSA(t, db, "chaos-client")
	if sa.RevokedAt == nil {
		t.Fatalf("revoked_at was cleared by reconcile — manual revocation must stick")
	}
	if !sa.RevokedAt.Equal(revoked) {
		t.Fatalf("revoked_at timestamp drifted: want %v got %v", revoked, *sa.RevokedAt)
	}
}

func TestReconcileServiceAccounts_UpdatesScopesAndDescription(t *testing.T) {
	db := newServiceAccountTestDB(t)
	if err := ReconcileServiceAccounts(db, []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	if err := ReconcileServiceAccounts(db, []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write,chaos.webhook.write", Description: "v2"},
	}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	sa := getSA(t, db, "chaos-client")
	if sa.Scopes != "chaos.inject.write,chaos.webhook.write" {
		t.Fatalf("scopes not updated: %q", sa.Scopes)
	}
	if sa.Description != "v2" {
		t.Fatalf("description not updated: %q", sa.Description)
	}
}

func TestReconcileServiceAccounts_DoesNotDeleteDriftedRows(t *testing.T) {
	db := newServiceAccountTestDB(t)
	if err := ReconcileServiceAccounts(db, []InitialDataServiceAccount{
		{Name: "legacy-sa", Scopes: "chaos.read", Description: "kept"},
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Subsequent boot: legacy-sa has been removed from data.yaml.
	if err := ReconcileServiceAccounts(db, []InitialDataServiceAccount{
		{Name: "chaos-client", Scopes: "chaos.inject.write", Description: "v1"},
	}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	// legacy-sa must still be present — deletion is the operator's job, not
	// the reconciler's.
	if _, err := getSAIfExists(db, "legacy-sa"); err != nil {
		t.Fatalf("legacy SA should not be deleted: %v", err)
	}
}

func getSAIfExists(db *gorm.DB, name string) (*model.ServiceAccount, error) {
	var sa model.ServiceAccount
	if err := db.Where("name = ?", name).First(&sa).Error; err != nil {
		return nil, err
	}
	return &sa, nil
}

func TestReconcileServiceAccounts_EmptyNameRejected(t *testing.T) {
	db := newServiceAccountTestDB(t)
	err := ReconcileServiceAccounts(db, []InitialDataServiceAccount{{Name: ""}})
	if err == nil {
		t.Fatalf("expected error for empty name, got nil")
	}
}
