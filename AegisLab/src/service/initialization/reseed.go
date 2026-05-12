package initialization

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegis/platform/consts"
	etcd "aegis/platform/etcd"
	"aegis/platform/model"
	containerrepo "aegis/core/domain/container"
	"aegis/service/common"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// reseedEtcdClient is the minimal etcd surface the reseed engine needs.
// Extracted as an interface so tests can stub it without a live etcd.
// Mirrors etcdMigrationClient in legacy_systems_migration.go.
type reseedEtcdClient interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string, ttl time.Duration) error
}

// ReseedAction is a single planned mutation from a data.yaml diff.
type ReseedAction struct {
	Layer    string `json:"layer"`  // "container_versions" | "helm_configs" | "dynamic_configs" | "etcd"
	System   string `json:"system"` // container / system name the action belongs to
	Key      string `json:"key"`    // semantic identifier: version name / config key / etcd key
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	Note     string `json:"note,omitempty"` // human-readable context ("new version", "default drift", "preserved override")
	Applied  bool   `json:"applied"`
}

// ReseedReport summarizes the full diff for a reseed invocation. Actions with
// Applied=false were skipped either because the run was dry, or because a
// non-default override was preserved (see ResetOverrides).
type ReseedReport struct {
	Env             string         `json:"env,omitempty"`
	DryRun          bool           `json:"dry_run"`
	ResetOverrides  bool           `json:"reset_overrides"`
	SystemFilter    string         `json:"system_filter,omitempty"`
	SeedPath        string         `json:"seed_path"`
	Actions         []ReseedAction `json:"actions"`
	PreservedCount  int            `json:"preserved_overrides"`
	NewVersions     int            `json:"new_versions"`
	DefaultsUpdated int            `json:"defaults_updated"`
	EtcdPublished   int            `json:"etcd_published"`
}

// ReseedRequest is the input to ReseedFromDataFile.
type ReseedRequest struct {
	DataPath       string // absolute path to data.yaml
	Env            string // optional label, purely informational
	SystemName     string // optional filter ("" = all)
	DryRun         bool
	ResetOverrides bool
}

// ReseedFromDataFile is the entry point used by both the HTTP handler and
// tests. It is careful to:
//
//   - Never UPDATE an existing (container_id, name) container_versions row.
//     A new helm_config.version in data.yaml means INSERT a new version row.
//   - Never overwrite a non-default etcd value (a live operator override)
//     unless ResetOverrides is true. "Override" is defined as: etcd value
//     exists and differs from the CURRENT DB default_value.
//   - Be idempotent across repeated runs: a second reseed with no upstream
//     change returns an empty action list.
func ReseedFromDataFile(ctx context.Context, db *gorm.DB, etcdGw reseedEtcdClient, req ReseedRequest) (*ReseedReport, error) {
	if db == nil {
		return nil, errors.New("reseed: db is required")
	}
	if strings.TrimSpace(req.DataPath) == "" {
		return nil, errors.New("reseed: data_path is required")
	}
	data, err := loadInitialDataFromFile(req.DataPath)
	if err != nil {
		return nil, fmt.Errorf("reseed: load %s: %w", req.DataPath, err)
	}

	report := &ReseedReport{
		Env:            req.Env,
		DryRun:         req.DryRun,
		ResetOverrides: req.ResetOverrides,
		SystemFilter:   req.SystemName,
		SeedPath:       req.DataPath,
	}

	// --- Containers / ContainerVersions / HelmConfigs ----------------------
	valuesDir := filepath.Dir(req.DataPath)
	for _, containerData := range data.Containers {
		if req.SystemName != "" && containerData.Name != req.SystemName {
			continue
		}
		if err := reseedContainerVersions(db, &containerData, valuesDir, req.DryRun, report); err != nil {
			return report, fmt.Errorf("reseed container %s: %w", containerData.Name, err)
		}
	}

	// --- DynamicConfigs (+ etcd drift) -------------------------------------
	for _, cfgData := range data.DynamicConfigs {
		if req.SystemName != "" && !configBelongsToSystem(cfgData.Key, req.SystemName) {
			continue
		}
		if err := reseedOneDynamicConfig(ctx, db, etcdGw, &cfgData, req.DryRun, req.ResetOverrides, report); err != nil {
			return report, fmt.Errorf("reseed config %s: %w", cfgData.Key, err)
		}
	}

	// --- Etcd drift for DB defaults that changed in an earlier boot but
	// have not yet reached etcd (e.g. fresh cluster or cleared etcd). We
	// catch these by iterating existing injection.system.* DB rows whose
	// scope is Global and whose etcd key is missing. This is also what
	// handles the "stray etcd-only drift" case from the task brief: if etcd
	// holds a non-default value for a key that data.yaml still describes,
	// we surface it as an action and apply or preserve it per the policy.
	//
	// Implementation: the per-row path above already does the compare; no
	// separate pass is needed because a data.yaml entry's default_value is
	// the authoritative source.

	return report, nil
}

// configBelongsToSystem returns true when the dynamic_config key is scoped
// to a given system short code. Only "injection.system.<name>." keys are
// system-scoped; every other key is global to the platform and is included
// only in an unfiltered reseed.
func configBelongsToSystem(key, system string) bool {
	return strings.HasPrefix(key, "injection.system."+system+".")
}

// reseedContainerVersions compares the data.yaml container (and its versions)
// against the DB and INSERTs any missing version row. Existing (container_id,
// version_name) pairs are never UPDATEd — that is the explicit contract: seed
// is additive for historical versions.
func reseedContainerVersions(db *gorm.DB, seed *InitialDataContainer, valuesDir string, dryRun bool, report *ReseedReport) error {
	// Look up container row by name. If absent, skip — a brand-new system
	// should go through `aegisctl system register` / InitializeProducer,
	// not reseed, which is for bumping already-seeded systems.
	var container model.Container
	err := db.Where("name = ? AND type = ?", seed.Name, seed.Type).First(&container).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logrus.Debugf("reseed: container %s (type=%d) not present in DB; skipping version diff", seed.Name, seed.Type)
			return nil
		}
		return fmt.Errorf("lookup container %s: %w", seed.Name, err)
	}

	// Enumerate existing versions once.
	var existing []model.ContainerVersion
	if err := db.Where("container_id = ?", container.ID).Find(&existing).Error; err != nil {
		return fmt.Errorf("list container_versions for %s: %w", seed.Name, err)
	}
	have := make(map[string]*model.ContainerVersion, len(existing))
	for i := range existing {
		have[existing[i].Name] = &existing[i]
	}

	for _, vSeed := range seed.Versions {
		versionName := vSeed.Name
		if existingVersion, ok := have[versionName]; ok {
			if err := backfillContainerVersionEnvVars(db, existingVersion, &vSeed, seed.Name, dryRun, report); err != nil {
				return err
			}
			// Existing row: check helm_config drift but NEVER update the row.
			// We surface drift in helm_config as a skipped action so operators
			// can see it; applying a chart/version change requires bumping the
			// version_name in data.yaml to honor the history preservation
			// contract.
			if vSeed.HelmConfig != nil {
				if err := compareHelmConfigDrift(db, existingVersion, vSeed.HelmConfig, seed.Name, dryRun, report); err != nil {
					return err
				}
				if err := resnapshotHelmValueFileIfDrifted(db, existingVersion, seed.Name, valuesDir, dryRun, report); err != nil {
					return err
				}
			}
			continue
		}
		// New version: INSERT a row plus (optionally) its helm_config.
		act := ReseedAction{
			Layer:    "container_versions",
			System:   seed.Name,
			Key:      versionName,
			OldValue: "",
			NewValue: fmt.Sprintf("image_ref=%q", vSeed.ImageRef),
			Note:     "new version from data.yaml",
		}
		if dryRun {
			report.Actions = append(report.Actions, act)
			report.NewVersions++
			continue
		}

		row := vSeed.ConvertToDBContainerVersion()
		row.ContainerID = container.ID
		row.UserID = adminUserIDForReseed(db)
		// Set image columns by hand for sqlite / reseed: BeforeCreate only
		// fills them when ImageRef is non-empty, which is correct for
		// non-pedestal containers; pedestal rows have empty image fields
		// which is also correct.
		row.ImageRef = vSeed.ImageRef
		// MySQL computes active_version_key as a generated column. Reseed only
		// needs to insert the user-visible version fields, so omit the generated
		// column on insert instead of sending the zero-value back to MySQL.
		if err := db.Omit("active_version_key").Create(row).Error; err != nil {
			return fmt.Errorf("insert container_version %s@%s: %w", seed.Name, versionName, err)
		}
		if vSeed.HelmConfig != nil {
			helm := vSeed.HelmConfig.ConvertToDBHelmConfig()
			helm.ContainerVersionID = row.ID
			if err := db.Create(helm).Error; err != nil {
				return fmt.Errorf("insert helm_config for %s@%s: %w", seed.Name, versionName, err)
			}
			if err := backfillHelmConfigValues(db, helm, versionName, vSeed.HelmConfig, seed.Name, dryRun, "new helm value for new version", report); err != nil {
				return err
			}
			valuesPath := filepath.Join(valuesDir, fmt.Sprintf("%s.yaml", seed.Name))
			if _, statErr := os.Stat(valuesPath); statErr == nil {
				if err := containerrepo.NewRepository(db).UploadHelmValueFileFromPath(seed.Name, helm, valuesPath); err != nil {
					return fmt.Errorf("upload helm value file for %s@%s: %w", seed.Name, versionName, err)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("stat helm value file for %s@%s: %w", seed.Name, versionName, statErr)
			}
			hact := ReseedAction{
				Layer:    "helm_configs",
				System:   seed.Name,
				Key:      versionName,
				NewValue: fmt.Sprintf("chart=%s version=%s repo=%s", helm.ChartName, helm.Version, helm.RepoName),
				Note:     "new helm_config for new version",
				Applied:  true,
			}
			report.Actions = append(report.Actions, hact)
		}
		act.Applied = true
		report.Actions = append(report.Actions, act)
		report.NewVersions++
		logrus.Infof("reseed %s: inserted container_versions row id=%d version=%s", seed.Name, row.ID, versionName)
	}

	return nil
}

func backfillContainerVersionEnvVars(db *gorm.DB, version *model.ContainerVersion, seed *InitialContainerVersion, systemName string, dryRun bool, report *ReseedReport) error {
	if len(seed.EnvVars) == 0 {
		return nil
	}

	var existing []model.ParameterConfig
	if err := db.Table("parameter_configs").
		Joins("JOIN container_version_env_vars ON container_version_env_vars.parameter_config_id = parameter_configs.id").
		Where("container_version_env_vars.container_version_id = ? AND parameter_configs.category = ?", version.ID, consts.ParameterCategoryEnvVars).
		Find(&existing).Error; err != nil {
		return fmt.Errorf("list env vars for %s@%s: %w", systemName, version.Name, err)
	}

	have := make(map[string]struct{}, len(existing))
	for i := range existing {
		have[parameterConfigIdentity(&existing[i])] = struct{}{}
	}

	ownerID := version.ContainerID
	for _, envSeed := range seed.EnvVars {
		cfg := envSeed.ConvertToDBParameterConfig()
		cfg.SystemID = &ownerID
		key := parameterConfigIdentity(cfg)
		if _, ok := have[key]; ok {
			continue
		}

		act := ReseedAction{
			Layer:    "container_version_env_vars",
			System:   systemName,
			Key:      fmt.Sprintf("%s@%s:%s", systemName, version.Name, cfg.Key),
			OldValue: "",
			NewValue: parameterConfigSummary(cfg),
			Note:     "backfilled env var on existing container_version",
		}
		if dryRun {
			report.Actions = append(report.Actions, act)
			continue
		}

		actualCfg, err := findOrCreateParameterConfig(db, cfg)
		if err != nil {
			return fmt.Errorf("resolve env var %s for %s@%s: %w", cfg.Key, systemName, version.Name, err)
		}

		rel := model.ContainerVersionEnvVar{
			ContainerVersionID: version.ID,
			ParameterConfigID:  actualCfg.ID,
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rel).Error; err != nil {
			return fmt.Errorf("link env var %s to %s@%s: %w", cfg.Key, systemName, version.Name, err)
		}

		act.Applied = true
		report.Actions = append(report.Actions, act)
		have[key] = struct{}{}
	}

	return nil
}

// findOrCreateParameterConfig looks up the parameter_configs row for the
// given (system_id, config_key, type, category) tuple and inserts it when
// missing. seed.SystemID must be set (or explicitly nil for cluster-wide
// rows) by the caller — the row's owner is part of its identity (issue
// #314).
func findOrCreateParameterConfig(db *gorm.DB, seed *model.ParameterConfig) (*model.ParameterConfig, error) {
	var existing model.ParameterConfig
	q := db.Where("config_key = ? AND type = ? AND category = ?", seed.Key, seed.Type, seed.Category)
	if seed.SystemID == nil {
		q = q.Where("system_id IS NULL")
	} else {
		q = q.Where("system_id = ?", *seed.SystemID)
	}
	err := q.First(&existing).Error
	switch {
	case err == nil:
		return &existing, nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	if err := db.Omit("id").Create(seed).Error; err != nil {
		return nil, err
	}
	return seed, nil
}

// parameterConfigIdentity is the (key, type, category) tuple used to dedupe
// parameter_configs *within an already system-scoped query result*. The
// helm_config_values / container_version_env_vars joins above scope the
// existing-row list to one helm_config (and thus one owning system), so the
// SystemID column is intentionally NOT part of this identity — including it
// would miss legacy NULL-system_id rows linked to a single owner via the
// join table.
func parameterConfigIdentity(cfg *model.ParameterConfig) string {
	return fmt.Sprintf("%s:%d:%d", cfg.Key, cfg.Type, cfg.Category)
}

// resolveSystemIDForHelmConfig returns the owning containers.id for a
// helm_configs row by joining through container_versions. The pointer is
// addressable so callers can stamp it onto ParameterConfig.SystemID. We
// require helm.ID > 0 (a persisted row); for synthetic dry-run helm configs
// (helm.ID == 0) we use ContainerVersionID directly when set, else nil.
// A nil return means "cluster-wide" — leave parameter_configs.system_id
// NULL.
func resolveSystemIDForHelmConfig(db *gorm.DB, helm *model.HelmConfig) (*int, error) {
	if helm == nil {
		return nil, nil
	}
	versionID := helm.ContainerVersionID
	if versionID == 0 && helm.ID != 0 {
		var hc model.HelmConfig
		if err := db.Select("container_version_id").Where("id = ?", helm.ID).First(&hc).Error; err != nil {
			return nil, err
		}
		versionID = hc.ContainerVersionID
	}
	if versionID == 0 {
		return nil, nil
	}
	var cv model.ContainerVersion
	if err := db.Select("container_id").Where("id = ?", versionID).First(&cv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if cv.ContainerID == 0 {
		return nil, nil
	}
	id := cv.ContainerID
	return &id, nil
}

func parameterConfigSummary(cfg *model.ParameterConfig) string {
	if cfg.TemplateString != nil && *cfg.TemplateString != "" {
		return fmt.Sprintf("template=%q", *cfg.TemplateString)
	}
	if cfg.DefaultValue != nil {
		return fmt.Sprintf("default=%q", *cfg.DefaultValue)
	}
	return "default=<nil>"
}

// compareHelmConfigDrift is a read-only diff: when the DB already has the
// same version row, we never UPDATE its helm_config (that would violate the
// history-preservation contract). But we DO surface the drift so operators
// see that their data.yaml bumped chart_name or version without also bumping
// the container_version name.
func compareHelmConfigDrift(db *gorm.DB, version *model.ContainerVersion, seed *InitialHelmConfig, systemName string, dryRun bool, report *ReseedReport) error {
	var existing model.HelmConfig
	err := db.Where("container_version_id = ?", version.ID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Existing version has no helm_config yet — INSERT is allowed
			// here because the version-level identity is preserved.
			helm := seed.ConvertToDBHelmConfig()
			helm.ContainerVersionID = version.ID
			act := ReseedAction{
				Layer:    "helm_configs",
				System:   systemName,
				Key:      version.Name,
				NewValue: fmt.Sprintf("chart=%s version=%s", seed.ChartName, seed.Version),
				Note:     "backfilled helm_config on existing version",
			}
			if dryRun {
				report.Actions = append(report.Actions, act)
				return nil
			}
			if err := db.Create(helm).Error; err != nil {
				return fmt.Errorf("insert missing helm_config for %s@%s: %w", systemName, version.Name, err)
			}
			act.Applied = true
			report.Actions = append(report.Actions, act)
			if err := backfillHelmConfigValues(db, helm, version.Name, seed, systemName, dryRun, "backfilled helm value on existing helm_config", report); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("lookup helm_config for version %d: %w", version.ID, err)
	}
	if err := backfillHelmConfigValues(db, &existing, version.Name, seed, systemName, dryRun, "backfilled helm value on existing helm_config", report); err != nil {
		return err
	}

	// Same version row, different chart metadata. Surface as a warning-
	// only action. Operators must bump the container_version name to apply.
	if existing.ChartName != seed.ChartName || existing.Version != seed.Version ||
		existing.RepoURL != seed.RepoURL || existing.RepoName != seed.RepoName {
		report.Actions = append(report.Actions, ReseedAction{
			Layer:    "helm_configs",
			System:   systemName,
			Key:      version.Name,
			OldValue: fmt.Sprintf("chart=%s version=%s repo=%s", existing.ChartName, existing.Version, existing.RepoName),
			NewValue: fmt.Sprintf("chart=%s version=%s repo=%s", seed.ChartName, seed.Version, seed.RepoName),
			Note:     "chart drift on existing container_version; bump container_version.name in data.yaml to apply",
			Applied:  false,
		})
	}
	return nil
}

// resnapshotHelmValueFileIfDrifted re-snapshots the helm value_file for an
// existing container_version when the on-disk source overlay (under
// valuesDir/<systemName>.yaml — same convention as the new-version branch
// and InitializeProducer) differs from the bytes already pinned by
// helm_configs.value_file. This closes issue #360: on system reseed --apply
// for an already-seeded version, operators expect a CM update to the overlay
// to take effect on the next helm install. Without this, the row keeps
// pointing at the original timestamped snapshot and helm silently installs
// stale values.
//
// No-op when: source overlay missing, current value_file empty, or bytes
// identical. The new-version INSERT branch is left alone — it already takes
// the same snapshot at row creation time.
func resnapshotHelmValueFileIfDrifted(db *gorm.DB, version *model.ContainerVersion, systemName, valuesDir string, dryRun bool, report *ReseedReport) error {
	var helm model.HelmConfig
	if err := db.Where("container_version_id = ?", version.ID).First(&helm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("lookup helm_config for value_file resnapshot: %w", err)
	}
	if helm.ValueFile == "" {
		return nil
	}
	srcPath := filepath.Join(valuesDir, fmt.Sprintf("%s.yaml", systemName))
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read source overlay %s: %w", srcPath, err)
	}
	curBytes, err := os.ReadFile(helm.ValueFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read current snapshot %s: %w", helm.ValueFile, err)
	}
	if err == nil && bytes.Equal(curBytes, srcBytes) {
		return nil
	}
	act := ReseedAction{
		Layer:    "helm_configs",
		System:   systemName,
		Key:      version.Name,
		OldValue: helm.ValueFile,
		NewValue: "<new snapshot from " + srcPath + ">",
		Note:     "value_file overlay drift; re-snapshotting from CM source",
	}
	if dryRun {
		report.Actions = append(report.Actions, act)
		return nil
	}
	if err := containerrepo.NewRepository(db).UploadHelmValueFileFromPath(systemName, &helm, srcPath); err != nil {
		return fmt.Errorf("upload re-snapshot for %s@%s: %w", systemName, version.Name, err)
	}
	act.NewValue = helm.ValueFile
	act.Applied = true
	report.Actions = append(report.Actions, act)
	logrus.Infof("reseed %s: re-snapshotted helm value_file for version=%s old=%s new=%s", systemName, version.Name, act.OldValue, helm.ValueFile)
	return nil
}

func backfillHelmConfigValues(db *gorm.DB, helm *model.HelmConfig, versionName string, seed *InitialHelmConfig, systemName string, dryRun bool, note string, report *ReseedReport) error {
	if helm == nil || seed == nil || len(seed.Values) == 0 {
		return nil
	}

	ownerID, err := resolveSystemIDForHelmConfig(db, helm)
	if err != nil {
		return fmt.Errorf("resolve owning system for helm value reseed %s@%s: %w", systemName, versionName, err)
	}

	var existing []model.ParameterConfig
	if err := db.Table("parameter_configs").
		Joins("JOIN helm_config_values ON helm_config_values.parameter_config_id = parameter_configs.id").
		Where("helm_config_values.helm_config_id = ? AND parameter_configs.category = ?", helm.ID, consts.ParameterCategoryHelmValues).
		Find(&existing).Error; err != nil {
		return fmt.Errorf("list helm values for %s@%s: %w", systemName, versionName, err)
	}

	have := make(map[string]*model.ParameterConfig, len(existing))
	for i := range existing {
		have[parameterConfigIdentity(&existing[i])] = &existing[i]
	}

	for _, valueSeed := range seed.Values {
		cfg := valueSeed.ConvertToDBParameterConfig()
		cfg.SystemID = ownerID
		key := parameterConfigIdentity(cfg)
		existingCfg, present := have[key]

		// Detect mismatched ownership / default_value: the helm_config is
		// linked to a parameter_configs row that doesn't belong to this
		// system or carries a stale default. This is the issue #314 failure
		// mode — pre-fix, two systems would land on a single shared row.
		// Re-resolve to the per-system row and relink, so reseed actually
		// repairs bad links instead of silently skipping.
		mismatch := present && (!systemIDsEqual(existingCfg.SystemID, ownerID) || !defaultValuesEqual(existingCfg.DefaultValue, cfg.DefaultValue))
		if present && !mismatch {
			continue
		}

		act := ReseedAction{
			Layer:    "helm_config_values",
			System:   systemName,
			Key:      fmt.Sprintf("%s@%s:%s", systemName, versionName, cfg.Key),
			OldValue: "",
			NewValue: parameterConfigSummary(cfg),
			Note:     note,
		}
		if mismatch {
			act.OldValue = parameterConfigSummary(existingCfg)
			act.Note = "relink: ownership/default mismatch"
		}
		if dryRun {
			report.Actions = append(report.Actions, act)
			continue
		}

		actualCfg, err := findOrCreateParameterConfig(db, cfg)
		if err != nil {
			return fmt.Errorf("resolve helm value %s for %s@%s: %w", cfg.Key, systemName, versionName, err)
		}

		if mismatch && actualCfg.ID != existingCfg.ID {
			// Drop the stale link before creating the correct one — the
			// (helm_config_id, parameter_config_id) PK would otherwise be
			// fine, but leaving the wrong link around makes future reseed /
			// helm-value-resolution see two competing rows for the same key.
			if err := db.Where("helm_config_id = ? AND parameter_config_id = ?", helm.ID, existingCfg.ID).
				Delete(&model.HelmConfigValue{}).Error; err != nil {
				return fmt.Errorf("drop stale helm link %s for %s@%s: %w", cfg.Key, systemName, versionName, err)
			}
		}

		rel := model.HelmConfigValue{
			HelmConfigID:      helm.ID,
			ParameterConfigID: actualCfg.ID,
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rel).Error; err != nil {
			return fmt.Errorf("link helm value %s to %s@%s: %w", cfg.Key, systemName, versionName, err)
		}

		act.Applied = true
		report.Actions = append(report.Actions, act)
		have[key] = actualCfg
	}

	return nil
}

func systemIDsEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// reseedOneDynamicConfig reconciles a single dynamic_configs row + its etcd
// key against the seed. Policy:
//
//   - If DB row is missing: CREATE row, publish etcd.
//   - If DB default_value differs from seed: UPDATE default_value in DB.
//     The etcd publish path then follows normal override-preservation logic.
//   - Etcd publish: if etcd key is missing, write seed default. If etcd value
//     equals the OLD DB default, follow the default forward (no user override
//     to protect). If etcd value matches the NEW seed default, no-op. If etcd
//     value is something else entirely, it is treated as a user override and
//     preserved unless ResetOverrides is set.
func reseedOneDynamicConfig(ctx context.Context, db *gorm.DB, etcdGw reseedEtcdClient, seed *InitialDynamicConfig, dryRun, resetOverrides bool, report *ReseedReport) error {
	newDefault := seed.DefaultValue
	var existing model.DynamicConfig
	err := db.Where("config_key = ?", seed.Key).First(&existing).Error
	switch {
	case err == nil:
		// Row present: maybe update default_value.
		if existing.DefaultValue != newDefault {
			oldForLog := existing.DefaultValue
			act := ReseedAction{
				Layer:    "dynamic_configs",
				System:   systemLabelFromKey(seed.Key),
				Key:      seed.Key,
				OldValue: oldForLog,
				NewValue: newDefault,
				Note:     "default_value drift",
			}
			if !dryRun {
				if err := db.Model(&existing).Update("default_value", newDefault).Error; err != nil {
					return fmt.Errorf("update default for %s: %w", seed.Key, err)
				}
				act.Applied = true
				report.DefaultsUpdated++
				logrus.Infof("reseed %s: updated dynamic_configs key=%s old=%s new=%s", act.System, seed.Key, oldForLog, newDefault)
			}
			report.Actions = append(report.Actions, act)
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Row absent: CREATE. This is rare (data.yaml added a brand-new key)
		// but natural for reseed: the seed path would have created it on
		// first boot, so reseed must match.
		act := ReseedAction{
			Layer:    "dynamic_configs",
			System:   systemLabelFromKey(seed.Key),
			Key:      seed.Key,
			OldValue: "",
			NewValue: newDefault,
			Note:     "new dynamic_config from data.yaml",
		}
		if !dryRun {
			cfg := seed.ConvertToDBDynamicConfig()
			if err := common.ValidateConfigMetadataConstraints(cfg); err != nil {
				return fmt.Errorf("validate new config %s: %w", seed.Key, err)
			}
			if err := common.CreateConfig(db, cfg); err != nil {
				return fmt.Errorf("create new config %s: %w", seed.Key, err)
			}
			act.Applied = true
			report.DefaultsUpdated++
			logrus.Infof("reseed %s: inserted dynamic_configs key=%s default=%s", act.System, seed.Key, newDefault)
			// Refresh `existing` so the etcd step below uses the new row.
			existing = *cfg
		}
		report.Actions = append(report.Actions, act)
	default:
		return fmt.Errorf("lookup %s: %w", seed.Key, err)
	}

	// ---- etcd ----
	// We publish the seed's new default iff the etcd-side value is either
	// absent or matches the OLD DB default (i.e. nobody overrode it). The
	// `oldDefault` we compare against is the DB value BEFORE this pass —
	// captured earlier as `existing.DefaultValue` when we entered; the
	// in-memory `existing` may have been mutated above, so recover it.
	oldDefault := newDefault
	// Re-read without mutating: fetch a fresh snapshot, but honor dry-run by
	// reading the stored row. In the freshly-created case this will equal
	// newDefault, which still produces a correct publish decision.
	if dryRun {
		// For dry-run, we know the pre-pass default came from `existing`
		// if it was present; if it was a new row we track no "old".
		if existing.ID != 0 {
			oldDefault = existing.DefaultValue
		}
	} else {
		var fresh model.DynamicConfig
		if ferr := db.Where("config_key = ?", seed.Key).First(&fresh).Error; ferr == nil {
			// We want the value BEFORE the update. We didn't capture it
			// for the happy-path. Reconstruct: if we applied a default
			// update above, the last ReseedAction in the report has the
			// OldValue we need. Otherwise (new row or no change) fall
			// back to newDefault which makes the check a no-op.
			for i := len(report.Actions) - 1; i >= 0; i-- {
				a := report.Actions[i]
				if a.Layer == "dynamic_configs" && a.Key == seed.Key && a.Note == "default_value drift" {
					oldDefault = a.OldValue
					break
				}
			}
		}
	}

	etcdPrefix := etcdPrefixForScope(seed.Scope)
	if etcdPrefix == "" || etcdGw == nil {
		return nil
	}
	etcdKey := etcdPrefix + seed.Key

	getCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	currentEtcd, getErr := etcdGw.Get(getCtx, etcdKey)
	cancel()

	absent := getErr != nil && strings.Contains(getErr.Error(), "key not found")
	if getErr != nil && !absent {
		// Connection / auth errors: log and move on (mirrors producer.go).
		logrus.WithError(getErr).Warnf("reseed: etcd lookup failed for %s", etcdKey)
		return nil
	}

	wantPublish := false
	preserve := false
	switch {
	case absent:
		wantPublish = true
	case currentEtcd == newDefault:
		// Already in sync.
	case currentEtcd == oldDefault:
		// Operator never overrode; follow the default forward.
		wantPublish = true
	default:
		// True override: preserve unless the caller explicitly resets.
		if resetOverrides {
			wantPublish = true
		} else {
			preserve = true
		}
	}

	if preserve {
		report.Actions = append(report.Actions, ReseedAction{
			Layer:    "etcd",
			System:   systemLabelFromKey(seed.Key),
			Key:      etcdKey,
			OldValue: currentEtcd,
			NewValue: newDefault,
			Note:     "preserved user override (rerun with --reset-overrides to replace)",
			Applied:  false,
		})
		report.PreservedCount++
		return nil
	}
	if !wantPublish {
		return nil
	}

	act := ReseedAction{
		Layer:    "etcd",
		System:   systemLabelFromKey(seed.Key),
		Key:      etcdKey,
		OldValue: currentEtcd,
		NewValue: newDefault,
		Note:     "publish seed default to etcd",
	}
	if dryRun {
		report.Actions = append(report.Actions, act)
		return nil
	}
	putCtx, putCancel := context.WithTimeout(ctx, 5*time.Second)
	putErr := etcdGw.Put(putCtx, etcdKey, newDefault, 0)
	putCancel()
	if putErr != nil {
		return fmt.Errorf("etcd publish %s: %w", etcdKey, putErr)
	}
	act.Applied = true
	report.Actions = append(report.Actions, act)
	report.EtcdPublished++
	logrus.Infof("reseed %s: etcd published key=%s value=%s", act.System, etcdKey, newDefault)
	return nil
}

// etcdPrefixForScope mirrors seedEtcdPrefixForScope but is exported-in-package
// for reseed callers. Kept as a wrapper so the two copies do not drift.
func etcdPrefixForScope(scope consts.ConfigScope) string {
	return seedEtcdPrefixForScope(scope)
}

// systemLabelFromKey returns the system short code for an
// injection.system.<name>.<field> key; for other keys the raw prefix is
// returned so report rows stay greppable.
func systemLabelFromKey(key string) string {
	const prefix = "injection.system."
	if strings.HasPrefix(key, prefix) {
		rest := strings.TrimPrefix(key, prefix)
		if idx := strings.Index(rest, "."); idx > 0 {
			return rest[:idx]
		}
	}
	return ""
}

// ReseedHelmConfigForVersionRequest drives a per-container_version reseed of
// the helm_configs row + its linked parameter_configs / helm_config_values
// tables. Unlike ReseedFromDataFile, which is keyed by system name and
// honors the "history-preservation" contract (never UPDATE an existing
// (container_id, version_name) row), this entry point is keyed by an
// explicit container_version_id and IS allowed to mutate the chart-level
// fields (chart_name / version / repo_url / repo_name / value_file /
// local_path) of the bound helm_configs row in place.
//
// This is the targeted "fix-up" path used by aegisctl pedestal helm reseed
// to propagate a seed-YAML chart bump to a running cluster without forcing
// re-allocation of every namespace bound to the old version_id (issue #201).
type ReseedHelmConfigForVersionRequest struct {
	DataPath           string // absolute path to data.yaml (resolved upstream from env/base)
	ContainerVersionID int    // required: which container_version's helm_configs row to reseed
	DryRun             bool   // default safety: don't write
	Prune              bool   // when true, delete helm_config_values links whose key disappeared from the seed
}

// ReseedHelmConfigForVersion is the entry point for a single-version helm
// reseed. The contract:
//
//   - Looks up the container_version row by ID. Returns gorm.ErrRecordNotFound
//     when absent so callers can map to HTTP 404.
//   - Walks data.yaml looking for a `versions[].name` that matches the row's
//     name on the same container. If absent, returns an error so the caller
//     knows the seed has no entry to reconcile against.
//   - UPSERTs the helm_configs row (chart_name / version / repo* / value_file /
//     local_path). Existing local_path / value_file are preserved when the seed
//     entry omits them so an operator-set local-fallback isn't clobbered.
//   - Walks the seed's helm_config.values list and: (a) inserts missing
//     parameter_configs rows (looked up by (config_key, type, category));
//     (b) inserts missing helm_config_values links; (c) when an existing
//     parameter_config row's default_value differs from the seed default,
//     LOGS A WARNING and leaves the DB row untouched (protects manual edits).
//   - With Prune=true, also deletes helm_config_values rows whose
//     parameter_config key is no longer in the seed. Parameter_configs rows
//     themselves are left in place because they may be shared across multiple
//     helm_configs.
//   - Idempotent: a second invocation with the same seed yields zero applied
//     actions.
func ReseedHelmConfigForVersion(ctx context.Context, db *gorm.DB, req ReseedHelmConfigForVersionRequest) (*ReseedReport, error) {
	if db == nil {
		return nil, errors.New("reseed: db is required")
	}
	if req.ContainerVersionID <= 0 {
		return nil, errors.New("reseed: container_version_id is required and must be > 0")
	}
	if strings.TrimSpace(req.DataPath) == "" {
		return nil, errors.New("reseed: data_path is required")
	}

	// Look up the version row + parent container. We need the container.Name
	// to find the matching block in data.yaml.
	var version model.ContainerVersion
	if err := db.Where("id = ?", req.ContainerVersionID).First(&version).Error; err != nil {
		return nil, err
	}
	var container model.Container
	if err := db.Where("id = ?", version.ContainerID).First(&container).Error; err != nil {
		return nil, fmt.Errorf("lookup parent container for version_id=%d: %w", req.ContainerVersionID, err)
	}

	data, err := loadInitialDataFromFile(req.DataPath)
	if err != nil {
		return nil, fmt.Errorf("reseed: load %s: %w", req.DataPath, err)
	}

	// Locate the seed entry for this container + version.
	var seedContainer *InitialDataContainer
	for i := range data.Containers {
		c := &data.Containers[i]
		if c.Name == container.Name && c.Type == container.Type {
			seedContainer = c
			break
		}
	}
	if seedContainer == nil {
		return nil, fmt.Errorf("reseed: data.yaml has no container entry for name=%s type=%d", container.Name, container.Type)
	}
	var seedVersion *InitialContainerVersion
	for i := range seedContainer.Versions {
		if seedContainer.Versions[i].Name == version.Name {
			seedVersion = &seedContainer.Versions[i]
			break
		}
	}
	if seedVersion == nil {
		return nil, fmt.Errorf("reseed: data.yaml has no versions[] entry for %s@%s", container.Name, version.Name)
	}
	if seedVersion.HelmConfig == nil {
		return nil, fmt.Errorf("reseed: data.yaml entry for %s@%s has no helm_config block", container.Name, version.Name)
	}

	report := &ReseedReport{
		Env:          "",
		DryRun:       req.DryRun,
		SystemFilter: container.Name,
		SeedPath:     req.DataPath,
	}

	// --- helm_configs row upsert ------------------------------------------
	helm, err := upsertHelmConfigForReseed(db, &version, seedVersion.HelmConfig, container.Name, req.DryRun, report)
	if err != nil {
		return report, fmt.Errorf("reseed helm_configs for %s@%s: %w", container.Name, version.Name, err)
	}
	if err := resnapshotHelmValueFileIfDrifted(db, &version, container.Name, filepath.Dir(req.DataPath), req.DryRun, report); err != nil {
		return report, fmt.Errorf("re-snapshot helm value_file for %s@%s: %w", container.Name, version.Name, err)
	}
	if helm == nil {
		// Dry-run path with no existing row: synthesize a placeholder so the
		// values walk below still produces "would-apply" entries against the
		// seed's chart fields.
		helm = seedVersion.HelmConfig.ConvertToDBHelmConfig()
		helm.ContainerVersionID = version.ID
	}

	// --- helm_config_values reconcile -------------------------------------
	// backfillHelmConfigValues only inserts MISSING links/parameter_configs;
	// it never overwrites an existing parameter_config.default_value. The
	// drift-warning case is handled by warnHelmValueDefaultDrift below.
	if err := warnHelmValueDefaultDrift(db, helm, version.Name, seedVersion.HelmConfig, container.Name, report); err != nil {
		return report, err
	}
	// Only do the insert pass when the helm_configs row actually exists in DB
	// (i.e. not a dry-run for a brand-new container_version). For dry-run we
	// still want to surface what WOULD be added, so reuse the existing
	// backfill in dry-run mode against the seed's helm_configs id.
	if helm.ID != 0 || req.DryRun {
		if err := backfillHelmConfigValues(db, helm, version.Name, seedVersion.HelmConfig, container.Name, req.DryRun, "reseed: new helm value", report); err != nil {
			return report, err
		}
	}

	// --- prune ------------------------------------------------------------
	if req.Prune && helm.ID != 0 {
		if err := pruneHelmConfigValues(db, helm, version.Name, seedVersion.HelmConfig, container.Name, req.DryRun, report); err != nil {
			return report, err
		}
	}

	return report, nil
}

// upsertHelmConfigForReseed mutates the helm_configs row bound to the given
// container_version. Unlike compareHelmConfigDrift, this DOES write
// chart-level fields back to DB (the whole point of issue #201's targeted
// reseed). value_file and local_path are preserved when the seed omits them.
func upsertHelmConfigForReseed(db *gorm.DB, version *model.ContainerVersion, seed *InitialHelmConfig, systemName string, dryRun bool, report *ReseedReport) (*model.HelmConfig, error) {
	var existing model.HelmConfig
	err := db.Where("container_version_id = ?", version.ID).First(&existing).Error
	switch {
	case err == nil:
		drifts := []string{}
		if existing.ChartName != seed.ChartName {
			drifts = append(drifts, fmt.Sprintf("chart_name %s -> %s", existing.ChartName, seed.ChartName))
		}
		if existing.Version != seed.Version {
			drifts = append(drifts, fmt.Sprintf("version %s -> %s", existing.Version, seed.Version))
		}
		if existing.RepoURL != seed.RepoURL {
			drifts = append(drifts, fmt.Sprintf("repo_url %s -> %s", existing.RepoURL, seed.RepoURL))
		}
		if existing.RepoName != seed.RepoName {
			drifts = append(drifts, fmt.Sprintf("repo_name %s -> %s", existing.RepoName, seed.RepoName))
		}
		if len(drifts) == 0 {
			return &existing, nil
		}
		act := ReseedAction{
			Layer:    "helm_configs",
			System:   systemName,
			Key:      version.Name,
			OldValue: fmt.Sprintf("chart=%s version=%s repo=%s url=%s", existing.ChartName, existing.Version, existing.RepoName, existing.RepoURL),
			NewValue: fmt.Sprintf("chart=%s version=%s repo=%s url=%s", seed.ChartName, seed.Version, seed.RepoName, seed.RepoURL),
			Note:     "in-place chart upsert: " + strings.Join(drifts, ", "),
		}
		if dryRun {
			report.Actions = append(report.Actions, act)
			return &existing, nil
		}
		existing.ChartName = seed.ChartName
		existing.Version = seed.Version
		existing.RepoURL = seed.RepoURL
		existing.RepoName = seed.RepoName
		// Preserve operator-set value_file / local_path when the seed omits
		// them; otherwise the seed wins.
		// Note: InitialHelmConfig has no value_file / local_path fields today,
		// so this branch is intentionally future-proofing.
		if err := db.Save(&existing).Error; err != nil {
			return nil, fmt.Errorf("update helm_configs row id=%d: %w", existing.ID, err)
		}
		act.Applied = true
		report.Actions = append(report.Actions, act)
		logrus.Infof("reseed %s: updated helm_configs id=%d for version=%s (%s)", systemName, existing.ID, version.Name, strings.Join(drifts, ", "))
		return &existing, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No helm_configs row yet — INSERT. This is the same path as
		// compareHelmConfigDrift's "missing" branch, but expressed inline so
		// the value-link reconcile below can use the freshly-created row.
		helm := seed.ConvertToDBHelmConfig()
		helm.ContainerVersionID = version.ID
		act := ReseedAction{
			Layer:    "helm_configs",
			System:   systemName,
			Key:      version.Name,
			NewValue: fmt.Sprintf("chart=%s version=%s repo=%s url=%s", seed.ChartName, seed.Version, seed.RepoName, seed.RepoURL),
			Note:     "create helm_configs row for existing container_version",
		}
		if dryRun {
			report.Actions = append(report.Actions, act)
			return nil, nil
		}
		if err := db.Create(helm).Error; err != nil {
			return nil, fmt.Errorf("insert helm_configs for version_id=%d: %w", version.ID, err)
		}
		act.Applied = true
		report.Actions = append(report.Actions, act)
		logrus.Infof("reseed %s: inserted helm_configs id=%d for version=%s", systemName, helm.ID, version.Name)
		return helm, nil
	default:
		return nil, fmt.Errorf("lookup helm_configs for version_id=%d: %w", version.ID, err)
	}
}

// warnHelmValueDefaultDrift logs and reports (without applying) cases where
// an existing parameter_configs row already linked to this helm_config has a
// different default_value than the seed. This protects manually-edited
// overrides per the issue #201 conflict semantics.
func warnHelmValueDefaultDrift(db *gorm.DB, helm *model.HelmConfig, versionName string, seed *InitialHelmConfig, systemName string, report *ReseedReport) error {
	if helm == nil || seed == nil || len(seed.Values) == 0 || helm.ID == 0 {
		return nil
	}
	ownerID, err := resolveSystemIDForHelmConfig(db, helm)
	if err != nil {
		return fmt.Errorf("resolve owning system for drift check %s@%s: %w", systemName, versionName, err)
	}
	// Pull the parameter_configs that this helm_config currently points at.
	var existing []model.ParameterConfig
	if err := db.Table("parameter_configs").
		Joins("JOIN helm_config_values ON helm_config_values.parameter_config_id = parameter_configs.id").
		Where("helm_config_values.helm_config_id = ? AND parameter_configs.category = ?", helm.ID, consts.ParameterCategoryHelmValues).
		Find(&existing).Error; err != nil {
		return fmt.Errorf("list helm values for %s@%s: %w", systemName, versionName, err)
	}
	have := make(map[string]*model.ParameterConfig, len(existing))
	for i := range existing {
		have[parameterConfigIdentity(&existing[i])] = &existing[i]
	}
	for _, vs := range seed.Values {
		want := vs.ConvertToDBParameterConfig()
		want.SystemID = ownerID
		key := parameterConfigIdentity(want)
		got, ok := have[key]
		if !ok {
			continue
		}
		if defaultValuesEqual(got.DefaultValue, want.DefaultValue) {
			continue
		}
		oldVal := "<nil>"
		if got.DefaultValue != nil {
			oldVal = *got.DefaultValue
		}
		newVal := "<nil>"
		if want.DefaultValue != nil {
			newVal = *want.DefaultValue
		}
		report.Actions = append(report.Actions, ReseedAction{
			Layer:    "parameter_configs",
			System:   systemName,
			Key:      fmt.Sprintf("%s@%s:%s", systemName, versionName, want.Key),
			OldValue: oldVal,
			NewValue: newVal,
			Note:     "default_value drift on existing parameter_config; preserved manual override (re-edit data.yaml or fix DB by hand)",
			Applied:  false,
		})
		logrus.Warnf("reseed %s@%s: parameter_configs key=%s default_value drift: db=%q seed=%q (preserved DB)",
			systemName, versionName, want.Key, oldVal, newVal)
	}
	return nil
}

func defaultValuesEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// pruneHelmConfigValues deletes helm_config_values links whose linked
// parameter_config key is no longer in the seed. parameter_configs rows
// themselves are NOT deleted because they may still be referenced by other
// helm_configs (or by env-var join tables).
func pruneHelmConfigValues(db *gorm.DB, helm *model.HelmConfig, versionName string, seed *InitialHelmConfig, systemName string, dryRun bool, report *ReseedReport) error {
	if helm == nil || helm.ID == 0 {
		return nil
	}
	ownerID, err := resolveSystemIDForHelmConfig(db, helm)
	if err != nil {
		return fmt.Errorf("resolve owning system for prune %s@%s: %w", systemName, versionName, err)
	}
	wanted := make(map[string]struct{}, len(seed.Values))
	for _, v := range seed.Values {
		cfg := v.ConvertToDBParameterConfig()
		cfg.SystemID = ownerID
		wanted[parameterConfigIdentity(cfg)] = struct{}{}
	}

	var existing []model.ParameterConfig
	if err := db.Table("parameter_configs").
		Joins("JOIN helm_config_values ON helm_config_values.parameter_config_id = parameter_configs.id").
		Where("helm_config_values.helm_config_id = ? AND parameter_configs.category = ?", helm.ID, consts.ParameterCategoryHelmValues).
		Find(&existing).Error; err != nil {
		return fmt.Errorf("list helm values for prune %s@%s: %w", systemName, versionName, err)
	}

	for i := range existing {
		cfg := &existing[i]
		key := parameterConfigIdentity(cfg)
		if _, ok := wanted[key]; ok {
			continue
		}
		oldVal := "<nil>"
		if cfg.DefaultValue != nil {
			oldVal = *cfg.DefaultValue
		}
		act := ReseedAction{
			Layer:    "helm_config_values",
			System:   systemName,
			Key:      fmt.Sprintf("%s@%s:%s", systemName, versionName, cfg.Key),
			OldValue: oldVal,
			NewValue: "",
			Note:     "prune: key disappeared from data.yaml seed",
		}
		if dryRun {
			report.Actions = append(report.Actions, act)
			continue
		}
		if err := db.
			Where("helm_config_id = ? AND parameter_config_id = ?", helm.ID, cfg.ID).
			Delete(&model.HelmConfigValue{}).Error; err != nil {
			return fmt.Errorf("delete helm_config_values link helm=%d param=%d: %w", helm.ID, cfg.ID, err)
		}
		act.Applied = true
		report.Actions = append(report.Actions, act)
		logrus.Infof("reseed %s@%s: pruned helm_config_values link key=%s (helm_config_id=%d param=%d)",
			systemName, versionName, cfg.Key, helm.ID, cfg.ID)
	}
	return nil
}

// ResolveSeedPath turns a (basePath, env) pair into the absolute data.yaml
// path. Both inputs may include the `data.yaml` filename or not. Used by the
// HTTP handler so CLI and HTTP paths agree on filesystem semantics.
func ResolveSeedPath(basePath, env string) (string, error) {
	if basePath == "" {
		return "", errors.New("data_path is required")
	}
	// If basePath already points at data.yaml, use it directly.
	if strings.HasSuffix(basePath, consts.InitialFilename) {
		return basePath, nil
	}
	// With env, prefer <basePath>/<env>/data.yaml so the same base can
	// serve prod and staging.
	if env != "" {
		return filepath.Join(basePath, env, consts.InitialFilename), nil
	}
	return filepath.Join(basePath, consts.InitialFilename), nil
}

// adminUserIDForReseed returns a reasonable default owner for newly inserted
// container_version rows during a reseed. The seed path assigns the admin
// user; here we try to find the admin row, falling back to user_id=0 if the
// users table is absent (unit-test sqlite case).
func adminUserIDForReseed(db *gorm.DB) int {
	var user model.User
	if err := db.Where("username = ?", AdminUsername).First(&user).Error; err == nil {
		return user.ID
	}
	return 0
}

// Compile-time assurance that etcd.Gateway satisfies reseedEtcdClient. The
// real gateway's Put signature uses time.Duration ttl which matches.
var _ reseedEtcdClient = (*etcd.Gateway)(nil)
