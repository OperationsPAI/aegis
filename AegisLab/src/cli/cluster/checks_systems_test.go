package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// seedSystemDB writes the 7 keys for one system into the fake dynamic_configs
// map, with the given status ("1" = enabled).
func seedSystemDB(m map[string]string, name string, fields map[string]string) {
	base := dbInjectionPrefix + name + "."
	for _, f := range SystemConfigFields {
		if v, ok := fields[f]; ok {
			m[base+f] = v
		} else {
			m[base+f] = ""
		}
	}
}

func seedSystemEtcd(m map[string]string, name string, fields map[string]string) {
	base := etcdGlobalInjectionPrefix + name + "."
	for _, f := range SystemConfigFields {
		if v, ok := fields[f]; ok {
			m[base+f] = v
		}
	}
}

func fullFields(display, ns, extract, appLabel string, count int, builtin bool, status int) map[string]string {
	b := "false"
	if builtin {
		b = "true"
	}
	s := "0"
	if status == 1 {
		s = "1"
	}
	return map[string]string{
		"display_name":    display,
		"ns_pattern":      ns,
		"extract_pattern": extract,
		"app_label_key":   appLabel,
		"count":           "1",
		"is_builtin":      b,
		"status":          s,
		// count default varies; the test table overrides when needed.
		"count_override": "",
	}
}

// helpers ---------------------------------------------------------------

func newParityEnv() *fakeEnv {
	return &fakeEnv{
		sql: &fakeMySQL{dynamicConfigs: map[string]string{}},
		etd: &fakeEtcd{values: map[string]string{}},
	}
}

// Tests ----------------------------------------------------------------

func TestCheckRegistryParity_OnlyDB(t *testing.T) {
	env := newParityEnv()
	seedSystemDB(env.sql.dynamicConfigs, "clickhouse", fullFields("ClickHouse", "^cc\\d+$", "", "app", 1, true, 1))
	// etcd is empty.
	res := checkRegistryParity(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s (detail=%q)", res.Status, res.Detail)
	}
	if !strings.Contains(res.Detail, "DB-only") || !strings.Contains(res.Detail, "clickhouse") {
		t.Errorf("expected DB-only clickhouse in detail: %q", res.Detail)
	}
}

func TestCheckRegistryParity_OnlyEtcd(t *testing.T) {
	env := newParityEnv()
	seedSystemEtcd(env.etd.values, "ghost", fullFields("Ghost", "^gg\\d+$", "", "app", 1, false, 1))
	res := checkRegistryParity(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "etcd-only") || !strings.Contains(res.Detail, "ghost") {
		t.Errorf("expected etcd-only ghost: %q", res.Detail)
	}
}

func TestCheckRegistryParity_Agree(t *testing.T) {
	env := newParityEnv()
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TrainTicket", "^ts\\d+$", "", "app", 1, true, 1))
	seedSystemEtcd(env.etd.values, "ts", fullFields("TrainTicket", "^ts\\d+$", "", "app", 1, true, 1))
	res := checkRegistryParity(context.Background(), env)
	if res.Status != StatusOK {
		t.Fatalf("expected OK, got %s (detail=%q)", res.Status, res.Detail)
	}
}

func TestCheckEtcdDBAgreement_MissingOnEtcd(t *testing.T) {
	env := newParityEnv()
	fields := fullFields("TrainTicket", "^ts\\d+$", "^(ts)(\\d+)$", "app", 1, true, 1)
	seedSystemDB(env.sql.dynamicConfigs, "ts", fields)
	// etcd has only 6 of 7 keys — display_name missing.
	partial := map[string]string{}
	for k, v := range fields {
		partial[k] = v
	}
	delete(partial, "display_name")
	seedSystemEtcd(env.etd.values, "ts", partial)

	res := checkEtcdDBAgreement(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "display_name") {
		t.Errorf("expected display_name in detail: %q", res.Detail)
	}
	if !strings.Contains(res.Fix, "aegisctl cluster preflight --fix --check etcd.db-agreement") {
		t.Errorf("fix should reference preflight --fix: %q", res.Fix)
	}

	// --fix should republish DB -> etcd.
	if err := fixEtcdDBAgreement(context.Background(), env); err != nil {
		t.Fatalf("fix: %v", err)
	}
	res = checkEtcdDBAgreement(context.Background(), env)
	if res.Status != StatusOK {
		t.Fatalf("after --fix expected OK, got %s (detail=%q)", res.Status, res.Detail)
	}
}

func TestCheckEtcdDBAgreement_ValueMismatch(t *testing.T) {
	env := newParityEnv()
	dbFields := fullFields("Tea", "^tea\\d+$", "^(tea)(\\d+)$", "app", 1, false, 1)
	seedSystemDB(env.sql.dynamicConfigs, "tea", dbFields)
	etFields := fullFields("OldTea", "^tea\\d+$", "^(tea)(\\d+)$", "app", 1, false, 1)
	seedSystemEtcd(env.etd.values, "tea", etFields)

	res := checkEtcdDBAgreement(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "display_name") || !strings.Contains(res.Detail, "OldTea") {
		t.Errorf("expected mismatch detail to name the conflicting field+value: %q", res.Detail)
	}

	// --fix republishes DB (authoritative) -> etcd.
	if err := fixEtcdDBAgreement(context.Background(), env); err != nil {
		t.Fatalf("fix: %v", err)
	}
	// Confirm DB value was NOT overwritten from etcd.
	if env.sql.dynamicConfigs[dbInjectionPrefix+"tea.display_name"] != "Tea" {
		t.Errorf("DB display_name must stay authoritative, got %q",
			env.sql.dynamicConfigs[dbInjectionPrefix+"tea.display_name"])
	}
	// Confirm etcd value WAS updated from DB.
	if env.etd.values[etcdGlobalInjectionPrefix+"tea.display_name"] != "Tea" {
		t.Errorf("etcd should have been republished from DB, got %q",
			env.etd.values[etcdGlobalInjectionPrefix+"tea.display_name"])
	}
}

func TestCheckEtcdDBAgreement_NoEnabledSystems(t *testing.T) {
	env := newParityEnv()
	// Only a disabled system.
	fields := fullFields("X", "^x\\d+$", "", "app", 1, false, 0)
	seedSystemDB(env.sql.dynamicConfigs, "x", fields)
	res := checkEtcdDBAgreement(context.Background(), env)
	if res.Status != StatusOK {
		t.Fatalf("expected OK with no enabled systems, got %s", res.Status)
	}
}

func TestCheckFixturesPerSystem_MissingPedestal(t *testing.T) {
	env := newParityEnv()
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TT", "^ts\\d+$", "", "app", 1, true, 1))
	env.sql.fixtures = map[string]SystemFixtureSummary{
		"ts": {PedestalCount: 0, BenchmarkCount: 1, BenchmarkVersionCount: 1},
	}
	res := checkFixturesPerSystem(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "no pedestal") {
		t.Errorf("expected 'no pedestal' in detail: %q", res.Detail)
	}
}

func TestCheckFixturesPerSystem_EmptyCommand(t *testing.T) {
	env := newParityEnv()
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TT", "^ts\\d+$", "", "app", 1, true, 1))
	env.sql.fixtures = map[string]SystemFixtureSummary{
		"ts": {PedestalCount: 1, PedestalVersionCount: 1, BenchmarkCount: 1, BenchmarkVersionCount: 2, BenchmarkVersionsEmptyCmd: 2},
	}
	res := checkFixturesPerSystem(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s (detail=%q)", res.Status, res.Detail)
	}
	if !strings.Contains(res.Detail, "empty command") {
		t.Errorf("expected empty-command gap: %q", res.Detail)
	}
}

func TestCheckFixturesPerSystem_AllPresent(t *testing.T) {
	env := newParityEnv()
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TT", "^ts\\d+$", "", "app", 1, true, 1))
	env.sql.fixtures = map[string]SystemFixtureSummary{
		"ts": {PedestalCount: 1, PedestalVersionCount: 1, BenchmarkCount: 1, BenchmarkVersionCount: 1},
	}
	res := checkFixturesPerSystem(context.Background(), env)
	if res.Status != StatusOK {
		t.Fatalf("expected OK, got %s (detail=%q)", res.Status, res.Detail)
	}
}

func TestCheckHelmSourceReachable_RepoReachable(t *testing.T) {
	env := newParityEnv()
	env.helm = &fakeHelm{}
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TT", "^ts\\d+$", "", "app", 1, true, 1))
	env.sql.helmSources = map[string]HelmChartSource{
		"ts": {ChartName: "trainticket", Version: "1.0.0", RepoURL: "https://example.com/charts"},
	}
	res := checkHelmSourceReachable(context.Background(), env)
	if res.Status != StatusOK {
		t.Fatalf("expected OK, got %s (detail=%q)", res.Status, res.Detail)
	}
}

func TestCheckHelmSourceReachable_RepoFails(t *testing.T) {
	env := newParityEnv()
	env.helm = &fakeHelm{failures: map[string]error{
		"https://broken.example.com": errors.New("GET .../index.yaml: status 404"),
	}}
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TT", "^ts\\d+$", "", "app", 1, true, 1))
	env.sql.helmSources = map[string]HelmChartSource{
		"ts": {ChartName: "trainticket", Version: "1.0.0", RepoURL: "https://broken.example.com"},
	}
	res := checkHelmSourceReachable(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "404") {
		t.Errorf("expected 404 in detail: %q", res.Detail)
	}
}

func TestCheckHelmSourceReachable_NoHelmRow(t *testing.T) {
	env := newParityEnv()
	env.helm = &fakeHelm{}
	seedSystemDB(env.sql.dynamicConfigs, "ts", fullFields("TT", "^ts\\d+$", "", "app", 1, true, 1))
	env.sql.helmMissing = map[string]bool{"ts": true}
	res := checkHelmSourceReachable(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "no helm_config") {
		t.Errorf("expected 'no helm_config' in detail: %q", res.Detail)
	}
}

const goodCollectorConfig = `
receivers:
  otlp:
    protocols:
      grpc: {}
exporters:
  clickhouse:
    endpoint: tcp://clickhouse:9000
  debug: {}
processors:
  k8sattributes: {}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [k8sattributes]
      exporters: [clickhouse]
    metrics:
      receivers: [otlp]
      exporters: [clickhouse]
    logs:
      receivers: [otlp]
      exporters:
        - clickhouse
        - debug
`

const badCollectorConfig = `
service:
  pipelines:
    traces:
      exporters: [clickhouse]
    metrics:
      exporters: [debug]
    logs:
      exporters: [debug]
`

func TestCheckOtelPipelineToClickHouse_Happy(t *testing.T) {
	env := newParityEnv()
	env.k8s = &fakeK8s{
		clusterRBAC: true,
		configMaps: map[string]map[string]string{
			"monitoring/otel-collector-collector": {"config.yaml": goodCollectorConfig},
		},
	}
	res := checkOtelPipelineToClickHouse(context.Background(), env)
	if res.Status != StatusOK {
		t.Fatalf("expected OK, got %s (detail=%q)", res.Status, res.Detail)
	}
}

func TestCheckOtelPipelineToClickHouse_NoRBAC(t *testing.T) {
	env := newParityEnv()
	env.k8s = &fakeK8s{clusterRBAC: false}
	res := checkOtelPipelineToClickHouse(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "k8sattributes") {
		t.Errorf("expected k8sattributes mention: %q", res.Detail)
	}
}

func TestCheckOtelPipelineToClickHouse_MissingPipeline(t *testing.T) {
	env := newParityEnv()
	env.k8s = &fakeK8s{
		clusterRBAC: true,
		configMaps: map[string]map[string]string{
			"monitoring/otel-collector-collector": {"config.yaml": badCollectorConfig},
		},
	}
	res := checkOtelPipelineToClickHouse(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s (detail=%q)", res.Status, res.Detail)
	}
	for _, want := range []string{"metrics", "logs"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("expected %q in detail: %q", want, res.Detail)
		}
	}
}

func TestCheckOtelPipelineToClickHouse_NoConfigMap(t *testing.T) {
	env := newParityEnv()
	env.k8s = &fakeK8s{clusterRBAC: true, configMaps: map[string]map[string]string{}}
	res := checkOtelPipelineToClickHouse(context.Background(), env)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s", res.Status)
	}
	if !strings.Contains(res.Detail, "collector ConfigMap not found") {
		t.Errorf("expected missing-CM detail: %q", res.Detail)
	}
}
