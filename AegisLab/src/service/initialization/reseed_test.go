package initialization

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newReseedTestDB spins up a sqlite DB with only the tables reseed touches.
// We deliberately skip the full model.AutoMigrate set — Container /
// ContainerVersion rows in production use MySQL-only generated columns that
// sqlite rejects. Instead we hand-create minimal tables with just the
// columns reseed reads / writes.
func newReseedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.DynamicConfig{}, &model.ConfigHistory{}); err != nil {
		t.Fatalf("migrate dynamic_configs: %v", err)
	}
	// Minimal containers / container_versions / helm_configs tables —
	// enough columns for reseed to look rows up and insert new ones.
	stmts := []string{
		`CREATE TABLE containers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type INTEGER NOT NULL,
			is_public INTEGER NOT NULL DEFAULT 0,
			status INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE container_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			name_major INTEGER DEFAULT 0,
			name_minor INTEGER DEFAULT 0,
			name_patch INTEGER DEFAULT 0,
			github_link TEXT,
			registry TEXT DEFAULT 'docker.io',
			namespace TEXT,
			repository TEXT,
			tag TEXT,
			command TEXT,
			usage_count INTEGER DEFAULT 0,
			container_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			status INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME,
			updated_at DATETIME,
			active_version_key TEXT
		)`,
		`CREATE TABLE helm_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chart_name TEXT NOT NULL,
			version TEXT NOT NULL,
			container_version_id INTEGER NOT NULL,
			repo_url TEXT NOT NULL,
			repo_name TEXT NOT NULL,
			local_path TEXT,
			checksum TEXT,
			value_file TEXT
		)`,
		`CREATE TABLE parameter_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			system_id INTEGER,
			system_id_key INTEGER NOT NULL DEFAULT 0,
			config_key TEXT NOT NULL,
			type INTEGER NOT NULL,
			category INTEGER NOT NULL,
			value_type INTEGER NOT NULL,
			description TEXT,
			default_value TEXT,
			template_string TEXT,
			required INTEGER NOT NULL DEFAULT 0,
			overridable INTEGER NOT NULL DEFAULT 1,
			UNIQUE(system_id_key, config_key, type, category)
		)`,
		`CREATE TABLE container_version_env_vars (
			container_version_id INTEGER NOT NULL,
			parameter_config_id INTEGER NOT NULL,
			created_at DATETIME,
			PRIMARY KEY (container_version_id, parameter_config_id)
		)`,
		`CREATE TABLE helm_config_values (
			helm_config_id INTEGER NOT NULL,
			parameter_config_id INTEGER NOT NULL,
			created_at DATETIME,
			PRIMARY KEY (helm_config_id, parameter_config_id)
		)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("create test table: %v", err)
		}
	}
	return db
}

// mustExec runs a setup INSERT/UPDATE and fails the test on error. Setup
// failures (schema/constraint drift) otherwise surface as opaque
// downstream assertions.
func mustExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("setup exec failed: %v\n  sql: %s", err, sql)
	}
}

// writeSeedFile dumps a minimal InitialData YAML to a temp file and returns
// its path. Keeps tests self-contained — we don't share the production
// data.yaml fixture because it's too rich and gets updated out-of-band.
func writeSeedFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	return path
}

// TestReseedInsertsNewContainerVersion verifies the "new version in
// data.yaml" path: a new container_versions row is INSERTed and its
// helm_config created. The pre-existing version row is left untouched.
func TestReseedInsertsNewContainerVersion(t *testing.T) {
	db := newReseedTestDB(t)

	// Seed a container + one pre-existing version + helm_config.
	if err := db.Exec(`INSERT INTO containers (id, name, type, status) VALUES (1, 'ts', 2, 1)`).Error; err != nil {
		t.Fatalf("seed container: %v", err)
	}
	if err := db.Exec(`INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (10, '0.1.0', 1, 0, 1)`).Error; err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if err := db.Exec(`INSERT INTO helm_configs (chart_name, version, container_version_id, repo_url, repo_name) VALUES ('trainticket', '0.1.0', 10, 'https://x', 'r')`).Error; err != nil {
		t.Fatalf("seed helm: %v", err)
	}

	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: ts
    is_public: true
    status: 1
    versions:
      - name: 0.1.0
        helm_config:
          version: 0.1.0
          chart_name: trainticket
          repo_name: r
          repo_url: https://x
      - name: 0.2.0
        helm_config:
          version: 0.2.0
          chart_name: trainticket
          repo_name: r
          repo_url: https://x
          values:
            - key: global.imageRegistry
              type: 0
              category: 1
              value_type: 0
              default_value: pair-cn-shanghai.cr.volces.com/opspai
              required: false
              overridable: true
            - key: global.imageTag
              type: 0
              category: 1
              value_type: 0
              default_value: 20260423-3ecac5f
              required: false
              overridable: true
`)

	report, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if report.NewVersions != 1 {
		t.Fatalf("NewVersions = %d, want 1 (actions=%+v)", report.NewVersions, report.Actions)
	}

	// New row present.
	var count int64
	db.Raw(`SELECT COUNT(*) FROM container_versions WHERE container_id = 1`).Scan(&count)
	if count != 2 {
		t.Fatalf("want 2 container_versions rows, got %d", count)
	}
	var v020 struct{ ID int }
	if err := db.Raw(`SELECT id FROM container_versions WHERE container_id = 1 AND name = '0.2.0'`).Scan(&v020).Error; err != nil || v020.ID == 0 {
		t.Fatalf("0.2.0 version row missing (err=%v id=%d)", err, v020.ID)
	}
	// New helm_config row bound to it.
	var helmCount int64
	db.Raw(`SELECT COUNT(*) FROM helm_configs WHERE container_version_id = ?`, v020.ID).Scan(&helmCount)
	if helmCount != 1 {
		t.Fatalf("want 1 helm_config for new version, got %d", helmCount)
	}
	var linked []struct {
		Key          string
		DefaultValue *string
	}
	if err := db.Raw(`
		SELECT pc.config_key AS key, pc.default_value
		FROM parameter_configs pc
		JOIN helm_config_values hcv ON hcv.parameter_config_id = pc.id
		JOIN helm_configs hc ON hc.id = hcv.helm_config_id
		WHERE hc.container_version_id = ?
		ORDER BY pc.config_key
	`, v020.ID).Scan(&linked).Error; err != nil {
		t.Fatalf("list linked helm values: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("expected 2 linked helm values, got %d (%+v)", len(linked), linked)
	}
	if linked[0].Key != "global.imageRegistry" || linked[1].Key != "global.imageTag" {
		t.Fatalf("unexpected helm values linked: %+v", linked)
	}

	// Existing 0.1.0 row and its helm_config are untouched.
	var v010Helm struct {
		ChartName string
		Version   string
	}
	db.Raw(`SELECT chart_name, version FROM helm_configs WHERE container_version_id = 10`).Scan(&v010Helm)
	if v010Helm.Version != "0.1.0" {
		t.Fatalf("existing helm_config mutated: %+v", v010Helm)
	}

	// Idempotence: a second reseed is a no-op.
	r2, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	if r2.NewVersions != 0 || len(r2.Actions) != 0 {
		t.Fatalf("expected clean idempotent rerun, got %+v", r2)
	}
}

// TestReseedChartDriftOnExistingVersionNotApplied pins the history-
// preservation contract: when data.yaml changes chart_name/version for an
// already-seeded container_version name, the DB is NOT mutated. The drift
// is surfaced as a skipped action so operators see it.
func TestReseedChartDriftOnExistingVersionNotApplied(t *testing.T) {
	db := newReseedTestDB(t)
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (1, 'ts', 2, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (10, '0.1.0', 1, 0, 1)`)
	mustExec(t, db, `INSERT INTO helm_configs (chart_name, version, container_version_id, repo_url, repo_name) VALUES ('trainticket', '0.1.0', 10, 'https://x', 'r')`)

	// data.yaml bumps chart_name but keeps the container_version name.
	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: ts
    is_public: true
    status: 1
    versions:
      - name: 0.1.0
        helm_config:
          version: 0.2.0
          chart_name: trainticket-v2
          repo_name: r
          repo_url: https://x
`)
	report, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}

	// DB is untouched.
	var got struct {
		ChartName string
		Version   string
	}
	db.Raw(`SELECT chart_name, version FROM helm_configs WHERE container_version_id = 10`).Scan(&got)
	if got.ChartName != "trainticket" || got.Version != "0.1.0" {
		t.Fatalf("helm_config mutated on drift: %+v", got)
	}
	// But drift is reported.
	driftFound := false
	for _, a := range report.Actions {
		if a.Layer == "helm_configs" && a.Applied == false {
			driftFound = true
		}
	}
	if !driftFound {
		t.Fatalf("expected unapplied helm_config drift action, got %+v", report.Actions)
	}
}

func TestReseedBackfillsHelmValuesOnExistingVersion(t *testing.T) {
	db := newReseedTestDB(t)
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (1, 'sockshop', 2, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (10, '1.1.1', 1, 0, 1)`)
	mustExec(t, db, `INSERT INTO helm_configs (id, chart_name, version, container_version_id, repo_url, repo_name) VALUES (20, 'sockshop', '1.1.1', 10, 'https://x', 'r')`)

	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: sockshop
    is_public: true
    status: 1
    versions:
      - name: 1.1.1
        helm_config:
          version: 1.1.1
          chart_name: sockshop
          repo_name: r
          repo_url: https://x
          values:
            - key: global.imageRegistry
              type: 0
              category: 1
              value_type: 0
              default_value: pair-cn-shanghai.cr.volces.com/opspai
              required: false
              overridable: true
            - key: global.imageTag
              type: 0
              category: 1
              value_type: 0
              default_value: 20260423-3ecac5f
              required: false
              overridable: true
`)

	report, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}

	var linked []struct {
		Key string
	}
	if err := db.Raw(`
		SELECT pc.config_key AS key
		FROM parameter_configs pc
		JOIN helm_config_values hcv ON hcv.parameter_config_id = pc.id
		WHERE hcv.helm_config_id = 20
		ORDER BY pc.config_key
	`).Scan(&linked).Error; err != nil {
		t.Fatalf("list linked helm values: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("expected 2 linked helm values, got %d (%+v)", len(linked), linked)
	}
	if linked[0].Key != "global.imageRegistry" || linked[1].Key != "global.imageTag" {
		t.Fatalf("unexpected helm values linked: %+v", linked)
	}

	found := false
	for _, a := range report.Actions {
		if a.Layer == "helm_config_values" && a.Key == "sockshop@1.1.1:global.imageRegistry" && a.Applied {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected applied helm-value backfill action, got %+v", report.Actions)
	}

	r2, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	if len(r2.Actions) != 0 {
		t.Fatalf("expected idempotent rerun after helm-value backfill, got %+v", r2.Actions)
	}
}

func TestReseedBackfillsEnvVarsOnExistingVersion(t *testing.T) {
	db := newReseedTestDB(t)
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (1, 'clickhouse', 1, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (10, '1.0.0', 1, 0, 1)`)

	ns := "ts0"
	if err := db.Create(&model.ParameterConfig{
		Key:          "NAMESPACE",
		Type:         consts.ParameterTypeFixed,
		Category:     consts.ParameterCategoryEnvVars,
		ValueType:    consts.ValueDataTypeString,
		DefaultValue: &ns,
		Required:     true,
		Overridable:  true,
	}).Error; err != nil {
		t.Fatalf("seed namespace param: %v", err)
	}
	var namespaceCfg model.ParameterConfig
	if err := db.Where("config_key = ?", "NAMESPACE").First(&namespaceCfg).Error; err != nil {
		t.Fatalf("lookup namespace param: %v", err)
	}
	if err := db.Create(&model.ContainerVersionEnvVar{
		ContainerVersionID: 10,
		ParameterConfigID:  namespaceCfg.ID,
	}).Error; err != nil {
		t.Fatalf("seed namespace relation: %v", err)
	}

	optional := "normal_logs.parquet"
	seed := writeSeedFile(t, `
containers:
  - type: 1
    name: clickhouse
    is_public: true
    status: 1
    versions:
      - name: 1.0.0
        image_ref: docker.io/opspai/clickhouse_dataset:latest
        command: bash /entrypoint.sh
        env_vars:
          - key: NAMESPACE
            type: 0
            category: 0
            value_type: 0
            default_value: ts0
            required: true
            overridable: true
          - key: RCABENCH_OPTIONAL_EMPTY_PARQUETS
            type: 0
            category: 0
            value_type: 0
            default_value: normal_logs.parquet
            required: false
            overridable: true
`)

	report, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}

	var linked []struct {
		Key          string
		DefaultValue *string
	}
	if err := db.Raw(`
		SELECT pc.config_key AS key, pc.default_value
		FROM parameter_configs pc
		JOIN container_version_env_vars cve ON cve.parameter_config_id = pc.id
		WHERE cve.container_version_id = 10
		ORDER BY pc.config_key
	`).Scan(&linked).Error; err != nil {
		t.Fatalf("list linked env vars: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("expected 2 linked env vars, got %d (%+v)", len(linked), linked)
	}
	if linked[0].Key != "NAMESPACE" || linked[1].Key != "RCABENCH_OPTIONAL_EMPTY_PARQUETS" {
		t.Fatalf("unexpected env vars linked: %+v", linked)
	}
	if linked[1].DefaultValue == nil || *linked[1].DefaultValue != optional {
		t.Fatalf("optional env default = %v, want %q", linked[1].DefaultValue, optional)
	}

	found := false
	for _, a := range report.Actions {
		if a.Layer == "container_version_env_vars" && a.Key == "clickhouse@1.0.0:RCABENCH_OPTIONAL_EMPTY_PARQUETS" && a.Applied {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected applied env-var backfill action, got %+v", report.Actions)
	}

	r2, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	if len(r2.Actions) != 0 {
		t.Fatalf("expected idempotent rerun after env-var backfill, got %+v", r2.Actions)
	}
}

// fakeReseedEtcd is a trivial stub of reseedEtcdClient for tests.
type fakeReseedEtcd struct{ data map[string]string }

func newFakeReseedEtcd() *fakeReseedEtcd { return &fakeReseedEtcd{data: map[string]string{}} }

func (f *fakeReseedEtcd) Get(_ context.Context, key string) (string, error) {
	v, ok := f.data[key]
	if !ok {
		return "", errTestKeyNotFound{key: key}
	}
	return v, nil
}
func (f *fakeReseedEtcd) Put(_ context.Context, key, value string, _ time.Duration) error {
	f.data[key] = value
	return nil
}

// errTestKeyNotFound mimics the etcd gateway's "key not found: <key>" error
// shape so the reseed engine's string-matching absence check succeeds.
type errTestKeyNotFound struct{ key string }

func (e errTestKeyNotFound) Error() string { return "key not found: " + e.key }

// TestReseedDynamicConfigDriftAppliedAndPublished covers the happy path:
// DB has old default, etcd value equals old default (no override), seed
// bumps the default -> DB updates, etcd publishes new default.
func TestReseedDynamicConfigDriftAppliedAndPublished(t *testing.T) {
	db := newReseedTestDB(t)
	_ = db.Create(&model.DynamicConfig{
		Key:          "injection.system.ts.count",
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeGlobal,
		Category:     "injection.system.count",
	}).Error

	etcd := newFakeReseedEtcd()
	etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"] = "1"

	seed := writeSeedFile(t, `
dynamic_configs:
  - key: injection.system.ts.count
    default_value: "5"
    value_type: 2
    scope: 2
    category: injection.system.count
`)
	report, err := ReseedFromDataFile(context.Background(), db, etcd, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if report.DefaultsUpdated != 1 {
		t.Fatalf("DefaultsUpdated = %d, want 1 (actions=%+v)", report.DefaultsUpdated, report.Actions)
	}
	if report.EtcdPublished != 1 {
		t.Fatalf("EtcdPublished = %d, want 1", report.EtcdPublished)
	}
	var row model.DynamicConfig
	_ = db.Where("config_key = ?", "injection.system.ts.count").First(&row).Error
	if row.DefaultValue != "5" {
		t.Fatalf("DB default_value = %q, want 5", row.DefaultValue)
	}
	if got := etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"]; got != "5" {
		t.Fatalf("etcd value = %q, want 5", got)
	}
}

// TestReseedPreservesUserOverride covers the main safety property: etcd
// value differs from both old and new default -> the value is a user
// override and we DO NOT stomp it on reseed.
func TestReseedPreservesUserOverride(t *testing.T) {
	db := newReseedTestDB(t)
	_ = db.Create(&model.DynamicConfig{
		Key:          "injection.system.ts.count",
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeGlobal,
		Category:     "injection.system.count",
	}).Error

	etcd := newFakeReseedEtcd()
	// Live value = 99, which is a user override (neither the old nor new default).
	etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"] = "99"

	seed := writeSeedFile(t, `
dynamic_configs:
  - key: injection.system.ts.count
    default_value: "5"
    value_type: 2
    scope: 2
    category: injection.system.count
`)
	report, err := ReseedFromDataFile(context.Background(), db, etcd, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if got := etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"]; got != "99" {
		t.Fatalf("etcd override stomped: %q, want 99", got)
	}
	if report.PreservedCount != 1 {
		t.Fatalf("PreservedCount = %d, want 1", report.PreservedCount)
	}
	// DB default is still updated (operator still benefits from the new fallback).
	var row model.DynamicConfig
	_ = db.Where("config_key = ?", "injection.system.ts.count").First(&row).Error
	if row.DefaultValue != "5" {
		t.Fatalf("DB default_value = %q, want 5 (the DB should follow the seed even when etcd is overridden)", row.DefaultValue)
	}

	// --reset-overrides replaces the live value.
	_, err = ReseedFromDataFile(context.Background(), db, etcd, ReseedRequest{DataPath: seed, ResetOverrides: true})
	if err != nil {
		t.Fatalf("reseed --reset-overrides: %v", err)
	}
	if got := etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"]; got != "5" {
		t.Fatalf("etcd not reset with --reset-overrides: got %q", got)
	}
}

// TestReseedDryRunMakesNoWrites ensures the default dry-run path does not
// mutate the DB or etcd even when drift exists.
func TestReseedDryRunMakesNoWrites(t *testing.T) {
	db := newReseedTestDB(t)
	_ = db.Create(&model.DynamicConfig{
		Key:          "injection.system.ts.count",
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
		Scope:        consts.ConfigScopeGlobal,
		Category:     "injection.system.count",
	}).Error
	etcd := newFakeReseedEtcd()
	etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"] = "1"

	seed := writeSeedFile(t, `
dynamic_configs:
  - key: injection.system.ts.count
    default_value: "5"
    value_type: 2
    scope: 2
    category: injection.system.count
`)
	report, err := ReseedFromDataFile(context.Background(), db, etcd, ReseedRequest{DataPath: seed, DryRun: true})
	if err != nil {
		t.Fatalf("reseed dry-run: %v", err)
	}
	if len(report.Actions) == 0 {
		t.Fatalf("expected drift actions in dry-run")
	}
	for _, a := range report.Actions {
		if a.Applied {
			t.Fatalf("dry-run produced applied action: %+v", a)
		}
	}
	var row model.DynamicConfig
	_ = db.Where("config_key = ?", "injection.system.ts.count").First(&row).Error
	if row.DefaultValue != "1" {
		t.Fatalf("dry-run mutated DB: default_value = %q", row.DefaultValue)
	}
	if got := etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"]; got != "1" {
		t.Fatalf("dry-run mutated etcd: value = %q", got)
	}
}

// TestReseedFilterByName skips rows for other systems when --name is set.
func TestReseedFilterByName(t *testing.T) {
	db := newReseedTestDB(t)
	for _, k := range []string{"injection.system.ts.count", "injection.system.hr.count"} {
		_ = db.Create(&model.DynamicConfig{
			Key: k, DefaultValue: "1", ValueType: consts.ConfigValueTypeInt,
			Scope: consts.ConfigScopeGlobal, Category: "injection.system.count",
		}).Error
	}
	etcd := newFakeReseedEtcd()
	etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.ts.count"] = "1"
	etcd.data[consts.ConfigEtcdGlobalPrefix+"injection.system.hr.count"] = "1"

	seed := writeSeedFile(t, `
dynamic_configs:
  - key: injection.system.ts.count
    default_value: "5"
    value_type: 2
    scope: 2
    category: injection.system.count
  - key: injection.system.hr.count
    default_value: "7"
    value_type: 2
    scope: 2
    category: injection.system.count
`)
	report, err := ReseedFromDataFile(context.Background(), db, etcd, ReseedRequest{DataPath: seed, SystemName: "ts"})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if report.DefaultsUpdated != 1 {
		t.Fatalf("DefaultsUpdated = %d, want 1 (ts only)", report.DefaultsUpdated)
	}
	var hr model.DynamicConfig
	_ = db.Where("config_key = ?", "injection.system.hr.count").First(&hr).Error
	if hr.DefaultValue != "1" {
		t.Fatalf("hr row mutated despite --name=ts filter: %q", hr.DefaultValue)
	}
}

// TestReseedHelmConfigForVersionUpsertsChartFields covers the issue #201
// path: a data.yaml chart-level bump on the SAME container_version_id is
// applied in place. New parameter_configs + helm_config_values are added.
// This is what the byte-cluster operator was forced to do via raw SQL.
func TestReseedHelmConfigForVersionUpsertsChartFields(t *testing.T) {
	db := newReseedTestDB(t)
	if err := db.Exec(`INSERT INTO containers (id, name, type, status) VALUES (1, 'tea', 2, 1)`).Error; err != nil {
		t.Fatalf("seed container: %v", err)
	}
	if err := db.Exec(`INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (62, '0.1.1', 1, 0, 1)`).Error; err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if err := db.Exec(`INSERT INTO helm_configs (id, chart_name, version, container_version_id, repo_url, repo_name) VALUES (90, 'teastore', '0.1.1', 62, 'https://lgu-se-internal.github.io/TeaStore', 'lgu-tea')`).Error; err != nil {
		t.Fatalf("seed helm: %v", err)
	}

	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: tea
    is_public: true
    status: 1
    versions:
      - name: 0.1.1
        helm_config:
          version: 0.1.2
          chart_name: teastore
          repo_name: lgu-tea
          repo_url: https://lgu-se-internal.github.io/TeaStore
          values:
            - key: jmeter.waitForRegistryImage.registry
              type: 0
              category: 1
              value_type: 0
              default_value: pair-cn-shanghai.cr.volces.com/opspai
              overridable: true
            - key: jmeter.waitForRegistryImage.repository
              type: 0
              category: 1
              value_type: 0
              default_value: busybox
              overridable: true
            - key: jmeter.waitForRegistryImage.tag
              type: 0
              category: 1
              value_type: 0
              default_value: "1.36"
              overridable: true
            - key: jmeter.waitForRegistryImage.pullPolicy
              type: 0
              category: 1
              value_type: 0
              default_value: IfNotPresent
              overridable: true
`)

	// Dry-run first.
	dry, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 62,
		DryRun:             true,
	})
	if err != nil {
		t.Fatalf("dry-run reseed: %v", err)
	}
	if !dry.DryRun {
		t.Fatalf("dry-run flag lost: %+v", dry)
	}
	if len(dry.Actions) == 0 {
		t.Fatalf("expected planned actions for dry-run, got none")
	}
	for _, a := range dry.Actions {
		if a.Applied {
			t.Fatalf("dry-run produced applied action: %+v", a)
		}
	}
	// DB should be untouched after dry-run.
	var stillOld struct{ Version string }
	db.Raw(`SELECT version FROM helm_configs WHERE id = 90`).Scan(&stillOld)
	if stillOld.Version != "0.1.1" {
		t.Fatalf("dry-run mutated DB version: %q", stillOld.Version)
	}

	// Apply.
	report, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 62,
	})
	if err != nil {
		t.Fatalf("apply reseed: %v", err)
	}

	// helm_configs.version is bumped.
	var got struct {
		ChartName string
		Version   string
	}
	db.Raw(`SELECT chart_name, version FROM helm_configs WHERE id = 90`).Scan(&got)
	if got.Version != "0.1.2" {
		t.Fatalf("version not bumped: %+v", got)
	}

	// All 4 jmeter values are linked.
	var linked []struct{ Key string }
	if err := db.Raw(`
		SELECT pc.config_key AS key
		FROM parameter_configs pc
		JOIN helm_config_values hcv ON hcv.parameter_config_id = pc.id
		WHERE hcv.helm_config_id = 90
		ORDER BY pc.config_key
	`).Scan(&linked).Error; err != nil {
		t.Fatalf("list linked: %v", err)
	}
	if len(linked) != 4 {
		t.Fatalf("expected 4 linked values, got %d (%+v)", len(linked), linked)
	}
	if linked[0].Key != "jmeter.waitForRegistryImage.pullPolicy" {
		t.Fatalf("unexpected first key: %s", linked[0].Key)
	}

	// At least one applied helm_configs action.
	driftApplied := false
	for _, a := range report.Actions {
		if a.Layer == "helm_configs" && a.Applied && a.Note != "" {
			driftApplied = true
		}
	}
	if !driftApplied {
		t.Fatalf("expected applied helm_configs drift action, got %+v", report.Actions)
	}

	// Idempotence.
	r2, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 62,
	})
	if err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	if len(r2.Actions) != 0 {
		t.Fatalf("expected idempotent rerun, got %+v", r2.Actions)
	}
}

// TestReseedHelmConfigForVersionPreservesDefaultValueDrift pins the
// constraint from issue #201: when the seed default differs from an
// existing parameter_configs.default_value, NEVER overwrite — log + report.
func TestReseedHelmConfigForVersionPreservesDefaultValueDrift(t *testing.T) {
	db := newReseedTestDB(t)
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (1, 'tea', 2, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (62, '0.1.2', 1, 0, 1)`)
	mustExec(t, db, `INSERT INTO helm_configs (id, chart_name, version, container_version_id, repo_url, repo_name) VALUES (90, 'teastore', '0.1.2', 62, 'https://x', 'lgu-tea')`)

	manuallyEdited := "operator-mirrored.example.com/busybox"
	cfg := &model.ParameterConfig{
		Key:          "jmeter.waitForRegistryImage.repository",
		Type:         consts.ParameterTypeFixed,
		Category:     consts.ParameterCategoryHelmValues,
		ValueType:    consts.ValueDataTypeString,
		DefaultValue: &manuallyEdited,
		Required:     false,
		Overridable:  true,
	}
	if err := db.Create(cfg).Error; err != nil {
		t.Fatalf("seed pre-existing param: %v", err)
	}
	if err := db.Create(&model.HelmConfigValue{HelmConfigID: 90, ParameterConfigID: cfg.ID}).Error; err != nil {
		t.Fatalf("link pre-existing value: %v", err)
	}

	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: tea
    is_public: true
    status: 1
    versions:
      - name: 0.1.2
        helm_config:
          version: 0.1.2
          chart_name: teastore
          repo_name: lgu-tea
          repo_url: https://x
          values:
            - key: jmeter.waitForRegistryImage.repository
              type: 0
              category: 1
              value_type: 0
              default_value: busybox
              overridable: true
`)

	report, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 62,
	})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}

	// DB is unchanged: the manually-edited default_value wins.
	var got model.ParameterConfig
	if err := db.Where("config_key = ? AND category = ?", "jmeter.waitForRegistryImage.repository", consts.ParameterCategoryHelmValues).First(&got).Error; err != nil {
		t.Fatalf("look up param: %v", err)
	}
	if got.DefaultValue == nil || *got.DefaultValue != manuallyEdited {
		t.Fatalf("default_value clobbered: got=%v want=%q", got.DefaultValue, manuallyEdited)
	}

	// And the report surfaces a preserved-drift action with Applied=false.
	preserved := false
	for _, a := range report.Actions {
		if a.Layer == "parameter_configs" && a.Key == "tea@0.1.2:jmeter.waitForRegistryImage.repository" && !a.Applied {
			preserved = true
		}
	}
	if !preserved {
		t.Fatalf("expected preserved-drift action, got %+v", report.Actions)
	}
}

// TestReseedHelmConfigForVersionPruneDeletesMissingLinks verifies that
// --prune removes helm_config_values links for keys that disappeared from
// the seed YAML. The parameter_configs row is intentionally NOT deleted.
func TestReseedHelmConfigForVersionPruneDeletesMissingLinks(t *testing.T) {
	db := newReseedTestDB(t)
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (1, 'tea', 2, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (62, '0.1.2', 1, 0, 1)`)
	mustExec(t, db, `INSERT INTO helm_configs (id, chart_name, version, container_version_id, repo_url, repo_name) VALUES (90, 'teastore', '0.1.2', 62, 'https://x', 'lgu-tea')`)

	staleVal := "deprecated"
	stale := &model.ParameterConfig{
		Key:          "stale.removed.key",
		Type:         consts.ParameterTypeFixed,
		Category:     consts.ParameterCategoryHelmValues,
		ValueType:    consts.ValueDataTypeString,
		DefaultValue: &staleVal,
		Overridable:  true,
	}
	if err := db.Create(stale).Error; err != nil {
		t.Fatalf("seed stale param: %v", err)
	}
	if err := db.Create(&model.HelmConfigValue{HelmConfigID: 90, ParameterConfigID: stale.ID}).Error; err != nil {
		t.Fatalf("link stale: %v", err)
	}

	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: tea
    is_public: true
    status: 1
    versions:
      - name: 0.1.2
        helm_config:
          version: 0.1.2
          chart_name: teastore
          repo_name: lgu-tea
          repo_url: https://x
          values:
            - key: kept.key
              type: 0
              category: 1
              value_type: 0
              default_value: alive
              overridable: true
`)

	// Without --prune, stale link survives.
	if _, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 62,
	}); err != nil {
		t.Fatalf("reseed (no prune): %v", err)
	}
	var stillLinked int64
	db.Raw(`SELECT COUNT(*) FROM helm_config_values WHERE helm_config_id = 90 AND parameter_config_id = ?`, stale.ID).Scan(&stillLinked)
	if stillLinked != 1 {
		t.Fatalf("stale link removed without --prune: count=%d", stillLinked)
	}

	// With --prune, the stale link is gone.
	report, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 62,
		Prune:              true,
	})
	if err != nil {
		t.Fatalf("reseed (prune): %v", err)
	}
	var afterPrune int64
	db.Raw(`SELECT COUNT(*) FROM helm_config_values WHERE helm_config_id = 90 AND parameter_config_id = ?`, stale.ID).Scan(&afterPrune)
	if afterPrune != 0 {
		t.Fatalf("stale link still present after --prune: count=%d", afterPrune)
	}

	// parameter_configs row itself stays — it might be referenced elsewhere.
	var paramStill int64
	db.Raw(`SELECT COUNT(*) FROM parameter_configs WHERE id = ?`, stale.ID).Scan(&paramStill)
	if paramStill != 1 {
		t.Fatalf("parameter_configs row deleted by prune; should be left alone (count=%d)", paramStill)
	}

	pruneApplied := false
	for _, a := range report.Actions {
		if a.Layer == "helm_config_values" && a.Note != "" && a.Applied && a.Key == "tea@0.1.2:stale.removed.key" {
			pruneApplied = true
		}
	}
	if !pruneApplied {
		t.Fatalf("expected applied prune action, got %+v", report.Actions)
	}
}

// TestReseedTwoSystemsSameKeyDifferentDefault pins issue #314: two systems
// declaring the same chart value path with different default_values must
// each land in DB as their own parameter_configs row with their own
// helm_config_values link, instead of colliding on the legacy 3-column
// unique index where only the first system's value would survive.
func TestReseedTwoSystemsSameKeyDifferentDefault(t *testing.T) {
	db := newReseedTestDB(t)
	// Two pre-existing systems sharing a DSB-style chart family that uses
	// the same `global.otel.endpoint` value path.
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (1, 'hs', 2, 1)`)
	mustExec(t, db, `INSERT INTO containers (id, name, type, status) VALUES (2, 'sn', 2, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (10, '0.1.0', 1, 0, 1)`)
	mustExec(t, db, `INSERT INTO container_versions (id, name, container_id, user_id, status) VALUES (20, '0.1.0', 2, 0, 1)`)
	mustExec(t, db, `INSERT INTO helm_configs (id, chart_name, version, container_version_id, repo_url, repo_name) VALUES (100, 'hs', '0.1.0', 10, 'https://x', 'r')`)
	mustExec(t, db, `INSERT INTO helm_configs (id, chart_name, version, container_version_id, repo_url, repo_name) VALUES (200, 'sn', '0.1.0', 20, 'https://x', 'r')`)

	seed := writeSeedFile(t, `
containers:
  - type: 2
    name: hs
    is_public: true
    status: 1
    versions:
      - name: 0.1.0
        helm_config:
          version: 0.1.0
          chart_name: hs
          repo_name: r
          repo_url: https://x
          values:
            - key: global.otel.endpoint
              type: 0
              category: 1
              value_type: 0
              default_value: opentelemetry-kube-stack-deployment-hs-collector.monitoring.svc.cluster.local:4318
              overridable: true
  - type: 2
    name: sn
    is_public: true
    status: 1
    versions:
      - name: 0.1.0
        helm_config:
          version: 0.1.0
          chart_name: sn
          repo_name: r
          repo_url: https://x
          values:
            - key: global.otel.endpoint
              type: 0
              category: 1
              value_type: 0
              default_value: opentelemetry-kube-stack-deployment-sn-collector.monitoring.svc.cluster.local:4318
              overridable: true
`)

	if _, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed}); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	// Two distinct parameter_configs rows must exist for the same key — one
	// per owning system.
	type row struct {
		ID           int
		SystemID     *int
		DefaultValue *string
	}
	var rows []row
	if err := db.Raw(`SELECT id, system_id, default_value FROM parameter_configs WHERE config_key = 'global.otel.endpoint' ORDER BY system_id`).Scan(&rows).Error; err != nil {
		t.Fatalf("list parameter_configs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 parameter_configs rows for global.otel.endpoint, got %d (%+v)", len(rows), rows)
	}

	// helm_config_values link must point hs's helm_config at hs's row, sn's at sn's.
	var hsLinked, snLinked string
	if err := db.Raw(`
		SELECT pc.default_value
		FROM parameter_configs pc
		JOIN helm_config_values hcv ON hcv.parameter_config_id = pc.id
		WHERE hcv.helm_config_id = 100 AND pc.config_key = 'global.otel.endpoint'`).Scan(&hsLinked).Error; err != nil {
		t.Fatalf("hs linked: %v", err)
	}
	if err := db.Raw(`
		SELECT pc.default_value
		FROM parameter_configs pc
		JOIN helm_config_values hcv ON hcv.parameter_config_id = pc.id
		WHERE hcv.helm_config_id = 200 AND pc.config_key = 'global.otel.endpoint'`).Scan(&snLinked).Error; err != nil {
		t.Fatalf("sn linked: %v", err)
	}
	if hsLinked != "opentelemetry-kube-stack-deployment-hs-collector.monitoring.svc.cluster.local:4318" {
		t.Fatalf("hs helm_config resolved wrong default: %q", hsLinked)
	}
	if snLinked != "opentelemetry-kube-stack-deployment-sn-collector.monitoring.svc.cluster.local:4318" {
		t.Fatalf("sn helm_config resolved wrong default: %q", snLinked)
	}

	// Idempotent: rerun produces no new actions.
	r2, err := ReseedFromDataFile(context.Background(), db, nil, ReseedRequest{DataPath: seed})
	if err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	if len(r2.Actions) != 0 {
		t.Fatalf("expected clean idempotent rerun for two-system seed, got %+v", r2.Actions)
	}
}

// TestReseedHelmConfigForVersionMissingVersion verifies the not-found path:
// an unknown container_version_id surfaces gorm.ErrRecordNotFound so the
// HTTP handler can map it to 404.
func TestReseedHelmConfigForVersionMissingVersion(t *testing.T) {
	db := newReseedTestDB(t)
	seed := writeSeedFile(t, `containers: []
`)
	_, err := ReseedHelmConfigForVersion(context.Background(), db, ReseedHelmConfigForVersionRequest{
		DataPath:           seed,
		ContainerVersionID: 9999,
	})
	if err == nil {
		t.Fatalf("expected error for missing version_id, got nil")
	}
}
