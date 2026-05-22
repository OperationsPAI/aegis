package chaossystem

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	containerapi "aegis/core/domain/container"
	"aegis/platform/consts"
	"aegis/platform/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// flakyEtcd is a fakeEtcd that fails on the Nth Put. Used to assert that
// OnboardSystem rolls back already-published keys on a mid-flight failure.
type flakyEtcd struct {
	data        map[string]string
	putCalls    int
	failOnPutN  int
	deleteCalls []string
}

func (f *flakyEtcd) Get(_ context.Context, key string) (string, error) {
	if v, ok := f.data[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

func (f *flakyEtcd) Put(_ context.Context, key, value string, _ time.Duration) error {
	f.putCalls++
	if f.failOnPutN > 0 && f.putCalls >= f.failOnPutN {
		return fmt.Errorf("synthetic etcd put failure on call %d", f.putCalls)
	}
	f.data[key] = value
	return nil
}

func (f *flakyEtcd) Delete(_ context.Context, key string) error {
	f.deleteCalls = append(f.deleteCalls, key)
	delete(f.data, key)
	return nil
}

func newOnboardService(t *testing.T, etcd chaosSystemEtcd) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.DynamicConfig{},
		&model.ConfigHistory{},
		&model.Container{},
		&model.ContainerVersion{},
		&model.HelmConfig{},
		&model.ParameterConfig{},
		&model.ContainerLabel{},
		&model.ContainerVersionEnvVar{},
		&model.HelmConfigValue{},
		&model.UserScopedRole{},
		&model.Role{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Insert role via raw SQL because SQLite does not support the MySQL
	// `GENERATED ALWAYS AS` virtual column on the `active_name` field.
	if err := db.Exec(`INSERT INTO roles (name, display_name, description, is_system, status, created_at, updated_at) VALUES (?, '', '', 0, 1, datetime('now'), datetime('now'))`, consts.RoleContainerAdmin.String()).Error; err != nil {
		t.Fatalf("seed role: %v", err)
	}
	svc := &Service{repo: NewRepository(db), etcd: etcd}
	return svc, db
}

func sampleOnboardReq(name string) *OnboardSystemReq {
	containerType := consts.ContainerTypePedestal
	isPublic := true
	imageRef := "docker.io/opspai/" + name + ":1.0.0"
	return &OnboardSystemReq{
		System: CreateChaosSystemReq{
			Name:           name,
			DisplayName:    name + " bench",
			NsPattern:      "^" + name + `\d+$`,
			ExtractPattern: "^(" + name + `)(\d+)$`,
			AppLabelKey:    "app.kubernetes.io/name",
			Count:          1,
			IsBuiltin:      false,
		},
		Container: containerapi.CreateContainerReq{
			Name:     name,
			Type:     &containerType,
			IsPublic: &isPublic,
			VersionReq: &containerapi.CreateContainerVersionReq{
				Name:     "1.0.0",
				ImageRef: imageRef,
				HelmConfigRequest: &containerapi.CreateHelmConfigReq{
					Version:   "1.0.0",
					ChartName: name,
					RepoName:  name + "-repo",
					RepoURL:   "https://charts.example.com",
				},
			},
		},
	}
}

func TestOnboardSystemRollsBackPublishedEtcdKeysOnPutFailure(t *testing.T) {
	// Fail on the 4th Put. The DB tx commits first (so dynamic_configs +
	// container persist), then etcd publishes start. Three keys land, the
	// fourth fails — we expect those three to be Delete'd by the rollback
	// helper AND the committed DB rows to be reversed (dynamic_configs
	// hard-deleted, container soft-deleted) so the operator can re-issue
	// the onboard request.
	etcd := &flakyEtcd{data: map[string]string{}, failOnPutN: 4}
	svc, db := newOnboardService(t, etcd)

	_, err := svc.OnboardSystem(context.Background(), sampleOnboardReq("rb"))
	if err == nil {
		t.Fatal("expected OnboardSystem to surface etcd publish failure")
	}

	if len(etcd.deleteCalls) != 3 {
		t.Fatalf("expected 3 best-effort deletes for the keys already published, got %d (%v)",
			len(etcd.deleteCalls), etcd.deleteCalls)
	}
	for _, k := range etcd.deleteCalls {
		if _, still := etcd.data[k]; still {
			t.Errorf("etcd key %s still present after rollback delete", k)
		}
	}

	// dynamic_configs rows must be hard-deleted so a re-run does not hit
	// the unique-key constraint on config_key.
	var remaining int64
	if err := db.Model(&model.DynamicConfig{}).
		Where("config_key LIKE ?", "injection.system.rb.%").Count(&remaining).Error; err != nil {
		t.Fatalf("count dynamic_configs: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected 0 dynamic_configs rows after rollback, got %d", remaining)
	}

	// Container must be soft-deleted so a re-run's createContainerCore
	// does not collide on active_name.
	var c model.Container
	if err := db.Where("name = ?", "rb").First(&c).Error; err != nil {
		t.Fatalf("lookup container row: %v", err)
	}
	if c.Status != consts.CommonDeleted {
		t.Errorf("expected container status=%d (deleted), got %d", consts.CommonDeleted, c.Status)
	}
}

func TestOnboardSystemRejectsDuplicateSystemName(t *testing.T) {
	etcd := &fakeEtcd{data: map[string]string{}}
	svc, db := newOnboardService(t, etcd)

	// Pre-seed the anchor dynamic_config row so the duplicate check fires
	// before any container write.
	if err := db.Create(&model.DynamicConfig{
		Key:          systemKey("dup", fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeGlobal,
		Category:     "injection.system.count",
	}).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	_, err := svc.OnboardSystem(context.Background(), sampleOnboardReq("dup"))
	if err == nil {
		t.Fatal("expected OnboardSystem to reject duplicate system name")
	}
}

// TestOnboardSystemInTxDuplicateMapsTo409 simulates the race where two
// concurrent onboards both pass the pre-tx existence probe and the loser
// hits a uniqueness violation inside the gorm transaction. The current
// dynamic_configs schema does not declare a unique constraint on
// config_key, so the test installs one locally to exercise the error
// mapping path. The mapping must surface as consts.ErrAlreadyExists
// (handler returns 409) rather than leaking the raw gorm error as 500.
func TestOnboardSystemInTxDuplicateMapsTo409(t *testing.T) {
	etcd := &fakeEtcd{data: map[string]string{}}
	svc, db := newOnboardService(t, etcd)

	// Add a unique index on config_key to mirror what production would
	// need to make the race detectable. Without this the duplicate insert
	// silently succeeds — bounded by the pre-tx probe in practice but not
	// formally safe against concurrent onboards with the same code.
	if err := db.Exec(`CREATE UNIQUE INDEX idx_dynamic_configs_key ON dynamic_configs(config_key)`).Error; err != nil {
		t.Fatalf("install unique index: %v", err)
	}

	// Pre-seed a NON-anchor injection.system.race.* row. The pre-tx
	// existence probe only checks the anchor (fieldCount) so the request
	// proceeds into the tx, where the second txRepo.CreateConfig (for the
	// ns_pattern row) collides with this seeded row.
	if err := db.Create(&model.DynamicConfig{
		Key:          systemKey("race", fieldNsPattern),
		DefaultValue: "^race\\d+$",
		ValueType:    consts.ConfigValueTypeString,
		Scope:        consts.ConfigScopeGlobal,
		Category:     "injection.system.ns_pattern",
	}).Error; err != nil {
		t.Fatalf("seed race row: %v", err)
	}

	_, err := svc.OnboardSystem(context.Background(), sampleOnboardReq("race"))
	if err == nil {
		t.Fatal("expected in-tx duplicate to surface as ErrAlreadyExists")
	}
	if !errors.Is(err, consts.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}
