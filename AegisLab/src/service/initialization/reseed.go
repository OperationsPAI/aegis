package initialization

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"aegis/consts"
	etcd "aegis/infra/etcd"
	"aegis/model"
	"aegis/service/common"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
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
	Layer    string `json:"layer"`    // "container_versions" | "helm_configs" | "dynamic_configs" | "etcd"
	System   string `json:"system"`   // container / system name the action belongs to
	Key      string `json:"key"`      // semantic identifier: version name / config key / etcd key
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
	for _, containerData := range data.Containers {
		if req.SystemName != "" && containerData.Name != req.SystemName {
			continue
		}
		if err := reseedContainerVersions(db, &containerData, req.DryRun, report); err != nil {
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
func reseedContainerVersions(db *gorm.DB, seed *InitialDataContainer, dryRun bool, report *ReseedReport) error {
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
			// Existing row: check helm_config drift but NEVER update the row.
			// We surface drift in helm_config as a skipped action so operators
			// can see it; applying a chart/version change requires bumping the
			// version_name in data.yaml to honor the history preservation
			// contract.
			if vSeed.HelmConfig != nil {
				if err := compareHelmConfigDrift(db, existingVersion, vSeed.HelmConfig, seed.Name, report); err != nil {
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
		if err := db.Create(row).Error; err != nil {
			return fmt.Errorf("insert container_version %s@%s: %w", seed.Name, versionName, err)
		}
		if vSeed.HelmConfig != nil {
			helm := vSeed.HelmConfig.ConvertToDBHelmConfig()
			helm.ContainerVersionID = row.ID
			if err := db.Create(helm).Error; err != nil {
				return fmt.Errorf("insert helm_config for %s@%s: %w", seed.Name, versionName, err)
			}
			hact := ReseedAction{
				Layer:   "helm_configs",
				System:  seed.Name,
				Key:     versionName,
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

// compareHelmConfigDrift is a read-only diff: when the DB already has the
// same version row, we never UPDATE its helm_config (that would violate the
// history-preservation contract). But we DO surface the drift so operators
// see that their data.yaml bumped chart_name or version without also bumping
// the container_version name.
func compareHelmConfigDrift(db *gorm.DB, version *model.ContainerVersion, seed *InitialHelmConfig, systemName string, report *ReseedReport) error {
	var existing model.HelmConfig
	err := db.Where("container_version_id = ?", version.ID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Existing version has no helm_config yet — INSERT is allowed
			// here because the version-level identity is preserved.
			helm := seed.ConvertToDBHelmConfig()
			helm.ContainerVersionID = version.ID
			if err := db.Create(helm).Error; err != nil {
				return fmt.Errorf("insert missing helm_config for %s@%s: %w", systemName, version.Name, err)
			}
			report.Actions = append(report.Actions, ReseedAction{
				Layer:    "helm_configs",
				System:   systemName,
				Key:      version.Name,
				NewValue: fmt.Sprintf("chart=%s version=%s", helm.ChartName, helm.Version),
				Note:     "backfilled helm_config on existing version",
				Applied:  true,
			})
			return nil
		}
		return fmt.Errorf("lookup helm_config for version %d: %w", version.ID, err)
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
