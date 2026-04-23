package chaossystem

import (
	"context"
	"testing"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestGetSystemChart_ReturnsSeededDynamicValues pins the backend-side bug fix
// for PR #126 follow-up: the chart-by-name response must include the seed
// helm_config.values entries so aegisctl pedestal chart install can apply
// per-aegis overrides (registry, image tag, OTLP endpoint). Before the fix,
// DynamicValues was never preloaded from the many2many join and the DTO
// factory did not copy it, so the response silently omitted values.
func TestGetSystemChart_ReturnsSeededDynamicValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Container{},
		&model.ContainerVersion{},
		&model.HelmConfig{},
		&model.ParameterConfig{},
		&model.HelmConfigValue{},
	); err != nil {
		t.Skipf("sqlite AutoMigrate unsupported for model set: %v", err)
	}

	// Seed a pedestal container + active version + helm config whose
	// chart_name matches what seed YAML produces for hs.
	container := &model.Container{
		Name:     "hs",
		Type:     consts.ContainerTypePedestal,
		Status:   consts.CommonEnabled,
		IsPublic: true,
	}
	if err := db.Omit("active_name", "Versions").Create(container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}
	version := &model.ContainerVersion{
		Name:        "0.1.0",
		ContainerID: container.ID,
		Status:      consts.CommonEnabled,
		UserID:      1,
		Registry:    "docker.io",
		Repository:  "hs",
		Tag:         "0.1.0",
	}
	if err := db.Omit("active_version_key", "HelmConfig", "EnvVars").Create(version).Error; err != nil {
		t.Fatalf("create version: %v", err)
	}
	helm := &model.HelmConfig{
		ChartName:          "hotel-reservation",
		Version:            "0.1.0",
		ContainerVersionID: version.ID,
		RepoURL:            "https://lgu-se-internal.github.io/DeathStarBench",
		RepoName:           "lgu-dsb",
	}
	if err := db.Create(helm).Error; err != nil {
		t.Fatalf("create helm config: %v", err)
	}

	// Two distinct helm values, mirroring the shape of the seed data.
	registryDefault := "docker.io/opspai"
	imageTagDefault := "20260423-61074ea"
	params := []*model.ParameterConfig{
		{
			Key:          "global.dockerRegistry",
			Type:         consts.ParameterTypeFixed,
			Category:     consts.ParameterCategoryHelmValues,
			ValueType:    consts.ValueDataTypeString,
			DefaultValue: &registryDefault,
			Overridable:  true,
		},
		{
			Key:          "global.defaultImageVersion",
			Type:         consts.ParameterTypeFixed,
			Category:     consts.ParameterCategoryHelmValues,
			ValueType:    consts.ValueDataTypeString,
			DefaultValue: &imageTagDefault,
			Overridable:  true,
		},
	}
	for _, p := range params {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create parameter %s: %v", p.Key, err)
		}
		if err := db.Create(&model.HelmConfigValue{HelmConfigID: helm.ID, ParameterConfigID: p.ID}).Error; err != nil {
			t.Fatalf("link %s: %v", p.Key, err)
		}
	}

	svc := &Service{repo: NewRepository(db)}
	resp, err := svc.GetSystemChart(context.Background(), "hs")
	if err != nil {
		t.Fatalf("GetSystemChart: %v", err)
	}
	if resp == nil {
		t.Fatalf("GetSystemChart: nil response")
	}

	// Shape assertions: values map should be nested per ParseHelmKey
	// semantics ("global.dockerRegistry" -> {global: {dockerRegistry: ...}}).
	if resp.Values == nil {
		t.Fatalf("expected Values to be populated; got nil (DynamicValues likely not preloaded or not copied in DTO)")
	}
	globalMap, ok := resp.Values["global"].(map[string]any)
	if !ok {
		t.Fatalf("expected Values.global to be a map, got %T: %v", resp.Values["global"], resp.Values)
	}
	if got := globalMap["dockerRegistry"]; got != registryDefault {
		t.Errorf("Values.global.dockerRegistry = %v, want %q", got, registryDefault)
	}
	if got := globalMap["defaultImageVersion"]; got != imageTagDefault {
		t.Errorf("Values.global.defaultImageVersion = %v, want %q", got, imageTagDefault)
	}
}
