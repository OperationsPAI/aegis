package chaossystem

import (
	"context"
	"strings"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/model"
)

// TestExportSeedEmitsHelmConfigValues pins the issue #478 contract: the
// canonical runtime state is the helm_config_values rows, so an export-seed
// snapshot that drops them would silently lose every image override on a
// re-seed. The exported YAML must carry the helm_config.values entry verbatim.
func TestExportSeedEmitsHelmConfigValues(t *testing.T) {
	etcd := &fakeEtcd{data: map[string]string{}}
	svc, db := newOnboardService(t, etcd)

	const name = "ev"
	cleanup := seedSystemInViper(t, name, false)
	defer cleanup()

	if _, err := svc.OnboardSystem(context.Background(), sampleOnboardReq(name)); err != nil {
		t.Fatalf("OnboardSystem: %v", err)
	}

	// Link a helm_config_values override onto the onboarded pedestal's
	// helm_config, mirroring what reseed / a chart bump would persist.
	helm, _, err := svc.repo.GetPedestalHelmConfigByName(name, "")
	if err != nil || helm == nil {
		t.Fatalf("GetPedestalHelmConfigByName: helm=%v err=%v", helm, err)
	}
	def := "pair-diag-cn-guangzhou.cr.volces.com/pair"
	pc := &model.ParameterConfig{
		Key:          "global.image.repository",
		Type:         consts.ParameterTypeFixed,
		Category:     consts.ParameterCategoryHelmValues,
		ValueType:    consts.ValueDataTypeString,
		DefaultValue: &def,
		Overridable:  true,
	}
	if err := db.Create(pc).Error; err != nil {
		t.Fatalf("create parameter_config: %v", err)
	}
	if err := db.Create(&model.HelmConfigValue{HelmConfigID: helm.ID, ParameterConfigID: pc.ID}).Error; err != nil {
		t.Fatalf("link helm_config_value: %v", err)
	}

	exp, err := svc.ExportSeed(context.Background(), name)
	if err != nil {
		t.Fatalf("ExportSeed: %v", err)
	}
	if !strings.Contains(exp.YAML, "global.image.repository") {
		t.Fatalf("exported YAML dropped helm_config_values key:\n%s", exp.YAML)
	}
	if !strings.Contains(exp.YAML, def) {
		t.Fatalf("exported YAML dropped helm_config_values default_value:\n%s", exp.YAML)
	}
}
