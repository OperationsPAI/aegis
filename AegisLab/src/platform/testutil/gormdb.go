package testutil

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// NewSQLiteGormDB returns an in-memory SQLite-backed gorm.DB suitable for
// tests that exercise repository code without standing up MySQL. The DB is
// closed via t.Cleanup.
func NewSQLiteGormDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err != nil {
			return
		}
		_ = sqlDB.Close()
	})
	return db
}
