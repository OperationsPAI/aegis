package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	containerapi "aegis/core/domain/container"
	"aegis/core/domain/chaossystem"
	"aegis/platform/consts"
	"aegis/platform/model"

	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// memEtcd is a minimal etcd double for the round-trip test. The
// chaossystem.Service.OnboardSystem path needs Get/Put/Delete; ExportSeed
// reads only from gorm + viper.
type memEtcd struct {
	data map[string]string
}

func (m *memEtcd) Get(_ context.Context, key string) (string, error) {
	if v, ok := m.data[key]; ok {
		return v, nil
	}
	return "", nil
}
func (m *memEtcd) Put(_ context.Context, key, value string, _ time.Duration) error {
	m.data[key] = value
	return nil
}
func (m *memEtcd) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

// TestExportSeedRoundTrip is the issue #458 acceptance test: the YAML
// produced by chaossystem.Service.ExportSeed must round-trip losslessly
// through the same seed-loader helpers that `aegisctl system register
// --from-seed` uses (loadSeedDoc + extractSystemSeed + validateSystemSeed).
// Catches drift where ExportSeed omits a field that the seed loader
// requires (value_type, scope, the seven injection.system.* keys, etc.).
func TestExportSeedRoundTrip(t *testing.T) {
	db := openRoundTripDB(t)
	svc := chaossystem.NewServiceWithEtcd(db, &memEtcd{data: map[string]string{}})

	const sysCode = "rt"
	const display = "RoundTrip Bench"
	const nsPattern = `^rt\d+$`
	const extractPattern = `^(rt)(\d+)$`
	const appLabel = "app"

	// Seed Viper so chaossystem.lookupByName resolves rt — OnboardSystem
	// re-reads identity from Viper for the response, mirroring how the
	// config watcher would refresh it in prod.
	seedViper(t, sysCode, display, nsPattern, extractPattern, appLabel)

	containerType := consts.ContainerTypePedestal
	isPublic := true
	req := &chaossystem.OnboardSystemReq{
		System: chaossystem.CreateChaosSystemReq{
			Name:           sysCode,
			DisplayName:    display,
			NsPattern:      nsPattern,
			ExtractPattern: extractPattern,
			AppLabelKey:    appLabel,
			Count:          2,
			IsBuiltin:      false,
			Description:    "Round-trip fixture",
		},
		Container: containerapi.CreateContainerReq{
			Name:     sysCode,
			Type:     &containerType,
			IsPublic: &isPublic,
			VersionReq: &containerapi.CreateContainerVersionReq{
				Name:     "1.2.3",
				ImageRef: "docker.io/opspai/rt:1.2.3",
				HelmConfigRequest: &containerapi.CreateHelmConfigReq{
					Version:   "1.2.3",
					ChartName: "rt-chart",
					RepoName:  "rt-repo",
					RepoURL:   "https://charts.example.com",
				},
			},
		},
	}
	if _, err := svc.OnboardSystem(context.Background(), req); err != nil {
		t.Fatalf("OnboardSystem: %v", err)
	}

	exp, err := svc.ExportSeed(context.Background(), sysCode)
	if err != nil {
		t.Fatalf("ExportSeed: %v", err)
	}
	if exp == nil || exp.YAML == "" {
		t.Fatal("ExportSeed: empty YAML")
	}

	// Write to a temp file and feed through the same seed loader the
	// `aegisctl system register --from-seed` command uses.
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "data.yaml")
	if err := os.WriteFile(yamlPath, []byte(exp.YAML), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	doc, err := loadSeedDoc(yamlPath)
	if err != nil {
		t.Fatalf("loadSeedDoc: %v", err)
	}
	parsed, err := extractSystemSeed(doc, sysCode)
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	if err := validateSystemSeed(parsed); err != nil {
		t.Fatalf("validateSystemSeed: %v", err)
	}

	if parsed.Name != sysCode {
		t.Errorf("Name: got %q want %q", parsed.Name, sysCode)
	}
	if parsed.DisplayName != display {
		t.Errorf("DisplayName: got %q want %q", parsed.DisplayName, display)
	}
	if parsed.NsPattern != nsPattern {
		t.Errorf("NsPattern: got %q want %q", parsed.NsPattern, nsPattern)
	}
	if parsed.ExtractPattern != extractPattern {
		t.Errorf("ExtractPattern: got %q want %q", parsed.ExtractPattern, extractPattern)
	}
	if parsed.AppLabelKey != appLabel {
		t.Errorf("AppLabelKey: got %q want %q", parsed.AppLabelKey, appLabel)
	}
	if parsed.Count != 2 {
		t.Errorf("Count: got %d want 2", parsed.Count)
	}
	if parsed.IsBuiltin {
		t.Errorf("IsBuiltin: got true want false")
	}
	if parsed.Status != int(consts.CommonEnabled) {
		t.Errorf("Status: got %d want %d", parsed.Status, consts.CommonEnabled)
	}
}

func openRoundTripDB(t *testing.T) *gorm.DB {
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
	if err := db.Exec(`INSERT INTO roles (name, display_name, description, is_system, status, created_at, updated_at) VALUES (?, '', '', 0, 1, datetime('now'), datetime('now'))`, consts.RoleContainerAdmin.String()).Error; err != nil {
		t.Fatalf("seed role: %v", err)
	}
	return db
}

func seedViper(t *testing.T, name, display, nsPattern, extractPattern, appLabel string) {
	t.Helper()
	prev := viper.Get("injection.system")
	viper.Set("injection.system."+name, map[string]any{
		"count":           1,
		"ns_pattern":      nsPattern,
		"extract_pattern": extractPattern,
		"display_name":    display,
		"app_label_key":   appLabel,
		"is_builtin":      false,
		"status":          int(consts.CommonEnabled),
	})
	t.Cleanup(func() { viper.Set("injection.system", prev) })
}
