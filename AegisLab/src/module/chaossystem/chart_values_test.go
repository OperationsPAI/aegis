package chaossystem

import (
	"context"
	"strings"
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
	resp, err := svc.GetSystemChart(context.Background(), "hs", "")
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

// TestGetSystemChart_PerVersionRouting pins issue #190: when a system has
// multiple container_versions whose helm_configs carry different
// helm_config_values defaults, the by-name/{name}/chart endpoint must
// return the row matching ?version=, not the highest semver in every case.
//
// Reproduces the byte-cluster sockshop scenario:
//
//	sockshop has 1.1.1 (imageTag=tag-old) and 1.1.2 (imageTag=tag-new).
//	GET .../chart?version=1.1.1 -> tag-old
//	GET .../chart?version=1.1.2 -> tag-new
//	GET .../chart                -> tag-new (highest semver)
//
// Before the fix, both versions resolved to whichever helm_config_values
// row the highest container_version pointed at, so callers couldn't see
// the older version's tag at all and a freshly-seeded new version that
// hadn't been promoted yet would also surface for ?version=<old>.
func TestGetSystemChart_PerVersionRouting(t *testing.T) {
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

	container := &model.Container{
		Name:     "sockshop",
		Type:     consts.ContainerTypePedestal,
		Status:   consts.CommonEnabled,
		IsPublic: true,
	}
	if err := db.Omit("active_name", "Versions").Create(container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}

	type seedVersion struct {
		name     string
		imageTag string
	}
	versions := []seedVersion{
		{name: "1.1.1", imageTag: "20260423-OLD"},
		{name: "1.1.2", imageTag: "20260425-NEW"},
	}

	for _, v := range versions {
		cv := &model.ContainerVersion{
			Name:        v.name,
			ContainerID: container.ID,
			Status:      consts.CommonEnabled,
			UserID:      1,
			Registry:    "docker.io",
			Repository:  "sockshop",
			Tag:         v.name,
		}
		if err := db.Omit("active_version_key", "HelmConfig", "EnvVars").Create(cv).Error; err != nil {
			t.Fatalf("create version %s: %v", v.name, err)
		}
		helm := &model.HelmConfig{
			ChartName:          "sockshop",
			Version:            "1.1.1", // chart version is identical across cv rows; what differs is helm_config_values
			ContainerVersionID: cv.ID,
			RepoURL:            "https://opspai.github.io/charts",
			RepoName:           "opspai",
		}
		if err := db.Create(helm).Error; err != nil {
			t.Fatalf("create helm config for %s: %v", v.name, err)
		}
		// Each version owns its own ParameterConfig row so the seed-time
		// default reflects what reseed wrote for that specific version.
		// (The bug being pinned here is independent of how reseed shares
		// or doesn't share parameter_configs across versions; the endpoint
		// must route to the correct helm_config row first.)
		tag := v.imageTag
		// In the live byte cluster the parameter_configs row is shared
		// across versions because findOrCreateParameterConfig keys on
		// (config_key,type,category). The endpoint contract under #190 is
		// that the response reflects whatever DefaultValue the row pointed
		// at by helm_config_values currently holds, regardless of how the
		// row got there. Here we use distinct keys per version only to
		// satisfy the unique constraint in this in-memory test fixture; the
		// helm_config_values link still carries the per-version mapping.
		p := &model.ParameterConfig{
			Key:          "global.imageTag_" + strings.ReplaceAll(v.name, ".", "_"),
			Type:         consts.ParameterTypeFixed,
			Category:     consts.ParameterCategoryHelmValues,
			ValueType:    consts.ValueDataTypeString,
			DefaultValue: &tag,
			Overridable:  true,
		}
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create parameter for %s: %v", v.name, err)
		}
		if err := db.Create(&model.HelmConfigValue{HelmConfigID: helm.ID, ParameterConfigID: p.ID}).Error; err != nil {
			t.Fatalf("link parameter for %s: %v", v.name, err)
		}
	}

	svc := &Service{repo: NewRepository(db)}

	cases := []struct {
		name        string
		queryVer    string
		wantTag     string
		wantPedTag  string
	}{
		{"explicit old version", "1.1.1", "20260423-OLD", "1.1.1"},
		{"explicit new version", "1.1.2", "20260425-NEW", "1.1.2"},
		{"no version falls back to highest semver", "", "20260425-NEW", "1.1.2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.GetSystemChart(context.Background(), "sockshop", tc.queryVer)
			if err != nil {
				t.Fatalf("GetSystemChart(ss, %q): %v", tc.queryVer, err)
			}
			if resp == nil {
				t.Fatalf("GetSystemChart(ss, %q): nil response", tc.queryVer)
			}
			if resp.PedestalTag != tc.wantPedTag {
				t.Errorf("PedestalTag = %q, want %q", resp.PedestalTag, tc.wantPedTag)
			}
			gm, ok := resp.Values["global"].(map[string]any)
			if !ok {
				t.Fatalf("Values.global not a map: %T %v", resp.Values["global"], resp.Values)
			}
			// Each version's helm_config_values link must surface only
			// that version's parameter row. The bug pre-fix made every
			// query return the highest semver's row, so the wrong-version
			// keys would appear here too.
			wantKey := "imageTag_" + strings.ReplaceAll(tc.wantPedTag, ".", "_")
			otherKeys := []string{}
			for k := range gm {
				if k == wantKey {
					continue
				}
				if strings.HasPrefix(k, "imageTag_") {
					otherKeys = append(otherKeys, k)
				}
			}
			if len(otherKeys) > 0 {
				t.Errorf("Values.global leaked rows from other versions when querying %q: unwanted keys=%v full=%v", tc.queryVer, otherKeys, gm)
			}
			if got := gm[wantKey]; got != tc.wantTag {
				t.Errorf("Values.global.%s = %v, want %q (endpoint returned wrong helm_config_values for version %q)", wantKey, got, tc.wantTag, tc.queryVer)
			}
		})
	}

	// Unknown version must 404 rather than silently fall through to the
	// latest helm_config — that was the masking behaviour pre-fix.
	resp, err := svc.GetSystemChart(context.Background(), "sockshop", "9.9.9")
	if err == nil {
		t.Fatalf("expected ErrNotFound for unknown version, got resp=%+v", resp)
	}
}
