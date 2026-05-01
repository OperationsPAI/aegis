package container

import (
	"testing"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestBatchGetContainerVersions_TieBreakerByID guards against issue #328:
// when two container_versions rows share (name_major, name_minor, name_patch)
// — the legacy 0/0/0 case where pre-semver rows were never reseeded — the
// selector must deterministically prefer the row with the highest id. Without
// the trailing `id DESC`, MySQL returned an implementation-defined row and
// pedestal version resolution would silently pick stale chart configs.
func TestBatchGetContainerVersions_TieBreakerByID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Container{}, &model.ContainerVersion{}); err != nil {
		t.Skipf("sqlite AutoMigrate unsupported: %v", err)
	}

	container := &model.Container{
		Name:     "legacy-bench",
		Type:     consts.ContainerTypeBenchmark,
		IsPublic: true,
		Status:   consts.CommonEnabled,
	}
	if err := db.Omit(containerCommonOmitFields).Create(container).Error; err != nil {
		t.Fatalf("seed container: %v", err)
	}

	// Insert two versions with identical (0,0,0) semver fields — the case
	// produced by legacy seed rows — using SkipHooks so BeforeCreate doesn't
	// re-derive major/minor/patch from Name.
	older := &model.ContainerVersion{
		Name:        "legacy-old",
		NameMajor:   0,
		NameMinor:   0,
		NamePatch:   0,
		ContainerID: container.ID,
		Tag:         "old",
		Repository:  "opspai/legacy",
		Status:      consts.CommonEnabled,
	}
	newer := &model.ContainerVersion{
		Name:        "legacy-new",
		NameMajor:   0,
		NameMinor:   0,
		NamePatch:   0,
		ContainerID: container.ID,
		Tag:         "new",
		Repository:  "opspai/legacy",
		Status:      consts.CommonEnabled,
	}
	tx := db.Session(&gorm.Session{SkipHooks: true}).Omit("active_version_key")
	if err := tx.Create(older).Error; err != nil {
		t.Fatalf("seed older version: %v", err)
	}
	if err := tx.Create(newer).Error; err != nil {
		t.Fatalf("seed newer version: %v", err)
	}
	if newer.ID <= older.ID {
		t.Fatalf("expected newer.ID > older.ID, got older=%d newer=%d", older.ID, newer.ID)
	}

	repo := NewRepository(db)
	versions, err := repo.batchGetContainerVersions(consts.ContainerTypeBenchmark, []string{"legacy-bench"}, 0)
	if err != nil {
		t.Fatalf("batchGetContainerVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	// ResolveContainerVersions consumes flatMap[name][0] when no explicit
	// version is requested, so the FIRST row from the ORDER BY must be the
	// one with the higher id.
	if versions[0].ID != newer.ID {
		t.Fatalf("tie-break failed: want id=%d (newer) first, got id=%d", newer.ID, versions[0].ID)
	}
}
