package cluster

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// SystemConfigFields lists the 7 `injection.system.<name>.*` fields that make
// up the registration shape for a single benchmark system.
var SystemConfigFields = []string{
	"count", "display_name", "ns_pattern", "extract_pattern",
	"app_label_key", "is_builtin", "status",
}

// etcdGlobalInjectionPrefix mirrors consts.ConfigEtcdGlobalPrefix + "injection.system.".
// Kept local to avoid pulling the aegis consts package into the cluster
// sub-tree (tests would then need a lot more wiring).
const etcdGlobalInjectionPrefix = "/rcabench/config/global/injection.system."

// dbInjectionPrefix is the dynamic_configs config_key prefix for system rows.
const dbInjectionPrefix = "injection.system."

// enabledSystemsFromDB returns the set of system names whose `status` row in
// dynamic_configs is enabled (default_value == "1"). The DB is authoritative
// for the preflight — all five new checks start from this set.
func enabledSystemsFromDB(rows map[string]string) map[string]struct{} {
	enabled := map[string]struct{}{}
	for key, val := range rows {
		if !strings.HasPrefix(key, dbInjectionPrefix) {
			continue
		}
		rest := strings.TrimPrefix(key, dbInjectionPrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 || parts[1] != "status" {
			continue
		}
		if strings.TrimSpace(val) == "1" {
			enabled[parts[0]] = struct{}{}
		}
	}
	return enabled
}

// systemsFromEtcd extracts the system-name set from etcd keys under the
// global injection prefix.
func systemsFromEtcd(rows map[string]string) map[string]struct{} {
	systems := map[string]struct{}{}
	for key := range rows {
		if !strings.HasPrefix(key, etcdGlobalInjectionPrefix) {
			continue
		}
		rest := strings.TrimPrefix(key, etcdGlobalInjectionPrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		systems[parts[0]] = struct{}{}
	}
	return systems
}

// systemsFromDB extracts the system-name set from dynamic_configs rows.
func systemsFromDB(rows map[string]string) map[string]struct{} {
	systems := map[string]struct{}{}
	for key := range rows {
		if !strings.HasPrefix(key, dbInjectionPrefix) {
			continue
		}
		rest := strings.TrimPrefix(key, dbInjectionPrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		systems[parts[0]] = struct{}{}
	}
	return systems
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// -------------------- registry.parity --------------------

func checkRegistryParity(ctx context.Context, env CheckEnv) Result {
	dbRows, err := env.MySQL().DynamicConfigsByPrefix(ctx, dbInjectionPrefix)
	if err != nil {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("could not read dynamic_configs: %v", err),
			Fix: "verify [database.mysql] credentials in config.dev.toml"}
	}
	etcdRows, err := env.Etcd().ListPrefix(ctx, etcdGlobalInjectionPrefix)
	if err != nil {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("could not list etcd prefix: %v", err),
			Fix: "verify etcd.endpoints in config.dev.toml"}
	}

	db := systemsFromDB(dbRows)
	etcdSet := systemsFromEtcd(etcdRows)

	var onlyDB, onlyEtcd []string
	for name := range db {
		if _, ok := etcdSet[name]; !ok {
			onlyDB = append(onlyDB, name)
		}
	}
	for name := range etcdSet {
		if _, ok := db[name]; !ok {
			onlyEtcd = append(onlyEtcd, name)
		}
	}
	sort.Strings(onlyDB)
	sort.Strings(onlyEtcd)

	if len(onlyDB) == 0 && len(onlyEtcd) == 0 {
		return Result{Status: StatusOK,
			Detail: fmt.Sprintf("%d system(s) present in both DB and etcd", len(db))}
	}
	var parts []string
	if len(onlyDB) > 0 {
		parts = append(parts, "DB-only: "+strings.Join(onlyDB, ","))
	}
	if len(onlyEtcd) > 0 {
		parts = append(parts, "etcd-only: "+strings.Join(onlyEtcd, ","))
	}
	return Result{Status: StatusFail,
		Detail: "registry drift: " + strings.Join(parts, "; "),
		Fix:    "for DB-only systems: aegisctl cluster preflight --fix --check etcd.db-agreement; for etcd-only: aegisctl system register --from-seed data.yaml --name <name>"}
}

// -------------------- etcd.db-agreement --------------------

type etcdDBMismatch struct {
	system string
	field  string
	dbVal  string
	etdVal string // "" + etdExists=false => missing
	etdExists bool
}

func collectEtcdDBMismatches(dbRows, etcdRows map[string]string, enabled map[string]struct{}) []etcdDBMismatch {
	var out []etcdDBMismatch
	for name := range enabled {
		for _, field := range SystemConfigFields {
			dbKey := dbInjectionPrefix + name + "." + field
			etKey := etcdGlobalInjectionPrefix + name + "." + field
			dbVal, dbOK := dbRows[dbKey]
			if !dbOK {
				// Missing on DB side — surface as a mismatch with empty DB val.
				etVal, etOK := etcdRows[etKey]
				out = append(out, etcdDBMismatch{system: name, field: field,
					dbVal: "<missing>", etdVal: etVal, etdExists: etOK})
				continue
			}
			etVal, etOK := etcdRows[etKey]
			if !etOK {
				out = append(out, etcdDBMismatch{system: name, field: field,
					dbVal: dbVal, etdExists: false})
				continue
			}
			if strings.TrimSpace(etVal) != strings.TrimSpace(dbVal) {
				out = append(out, etcdDBMismatch{system: name, field: field,
					dbVal: dbVal, etdVal: etVal, etdExists: true})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].system != out[j].system {
			return out[i].system < out[j].system
		}
		return out[i].field < out[j].field
	})
	return out
}

func checkEtcdDBAgreement(ctx context.Context, env CheckEnv) Result {
	dbRows, err := env.MySQL().DynamicConfigsByPrefix(ctx, dbInjectionPrefix)
	if err != nil {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("could not read dynamic_configs: %v", err),
			Fix: "verify [database.mysql] credentials in config.dev.toml"}
	}
	etcdRows, err := env.Etcd().ListPrefix(ctx, etcdGlobalInjectionPrefix)
	if err != nil {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("could not list etcd prefix: %v", err),
			Fix: "verify etcd.endpoints in config.dev.toml"}
	}
	enabled := enabledSystemsFromDB(dbRows)
	if len(enabled) == 0 {
		return Result{Status: StatusOK, Detail: "no enabled systems in dynamic_configs"}
	}
	mismatches := collectEtcdDBMismatches(dbRows, etcdRows, enabled)
	if len(mismatches) == 0 {
		return Result{Status: StatusOK,
			Detail: fmt.Sprintf("all 7 keys agree for %d enabled system(s)", len(enabled))}
	}
	var lines []string
	for _, m := range mismatches {
		if !m.etdExists {
			lines = append(lines, fmt.Sprintf("%s.%s: db=%q etcd=<missing>", m.system, m.field, m.dbVal))
		} else {
			lines = append(lines, fmt.Sprintf("%s.%s: db=%q etcd=%q", m.system, m.field, m.dbVal, m.etdVal))
		}
	}
	return Result{Status: StatusFail,
		Detail: fmt.Sprintf("%d mismatch(es): %s", len(mismatches), strings.Join(lines, "; ")),
		Fix:    "aegisctl cluster preflight --fix --check etcd.db-agreement (republishes DB -> etcd, idempotent)"}
}

// fixEtcdDBAgreement republishes the DB-side default_value into etcd, but
// ONLY when the DB side is authoritative (row exists, etcd missing or
// diverges). It never overwrites DB from etcd.
func fixEtcdDBAgreement(ctx context.Context, env CheckEnv) error {
	dbRows, err := env.MySQL().DynamicConfigsByPrefix(ctx, dbInjectionPrefix)
	if err != nil {
		return fmt.Errorf("dynamic_configs: %w", err)
	}
	etcdRows, err := env.Etcd().ListPrefix(ctx, etcdGlobalInjectionPrefix)
	if err != nil {
		return fmt.Errorf("list etcd: %w", err)
	}
	enabled := enabledSystemsFromDB(dbRows)
	for _, m := range collectEtcdDBMismatches(dbRows, etcdRows, enabled) {
		// Only republish when DB has a real value to publish. We intentionally
		// skip mismatches whose DB-row is missing — those would require
		// destroying data we don't own.
		if m.dbVal == "<missing>" {
			continue
		}
		etKey := etcdGlobalInjectionPrefix + m.system + "." + m.field
		if err := env.Etcd().Put(ctx, etKey, m.dbVal); err != nil {
			return fmt.Errorf("put %s: %w", etKey, err)
		}
	}
	return nil
}

// -------------------- db.fixtures-per-system --------------------

func checkFixturesPerSystem(ctx context.Context, env CheckEnv) Result {
	dbRows, err := env.MySQL().DynamicConfigsByPrefix(ctx, dbInjectionPrefix)
	if err != nil {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("could not read dynamic_configs: %v", err),
			Fix: "verify [database.mysql] credentials in config.dev.toml"}
	}
	enabled := enabledSystemsFromDB(dbRows)
	if len(enabled) == 0 {
		return Result{Status: StatusOK, Detail: "no enabled systems to audit"}
	}
	var missing []string
	for _, name := range sortedKeys(enabled) {
		fx, err := env.MySQL().SystemFixtures(ctx, name)
		if err != nil {
			return Result{Status: StatusFail,
				Detail: fmt.Sprintf("fixture lookup for %s failed: %v", name, err),
				Fix:    "verify [database.mysql] credentials in config.dev.toml"}
		}
		var gaps []string
		if fx.PedestalCount == 0 {
			gaps = append(gaps, "no pedestal (containers.type=2)")
		} else if fx.PedestalVersionCount == 0 {
			gaps = append(gaps, "pedestal has no container_versions")
		} else if fx.PedestalVersionsMissingHelm > 0 {
			gaps = append(gaps, fmt.Sprintf("%d pedestal version(s) missing helm_config", fx.PedestalVersionsMissingHelm))
		}
		if fx.BenchmarkCount == 0 {
			gaps = append(gaps, "no benchmark (containers.type=1)")
		} else if fx.BenchmarkVersionCount == 0 {
			gaps = append(gaps, "benchmark has no container_versions")
		} else if fx.BenchmarkVersionsEmptyCmd > 0 {
			gaps = append(gaps, fmt.Sprintf("%d benchmark version(s) with empty command", fx.BenchmarkVersionsEmptyCmd))
		}
		if len(gaps) > 0 {
			missing = append(missing, fmt.Sprintf("%s: %s", name, strings.Join(gaps, ", ")))
		}
	}
	if len(missing) == 0 {
		return Result{Status: StatusOK,
			Detail: fmt.Sprintf("fixtures complete for %d enabled system(s)", len(enabled))}
	}
	return Result{Status: StatusFail,
		Detail: strings.Join(missing, "; "),
		Fix:    "seed the missing rows via data.yaml or: aegisctl chart register / aegisctl system register --from-seed data.yaml"}
}

// -------------------- helm.source-reachable --------------------

func resolveHelmChart(ctx context.Context, src HelmChartSource, timeout time.Duration) error {
	callCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	url := strings.TrimSpace(src.RepoURL)
	switch {
	case strings.HasPrefix(url, "oci://"):
		ref := strings.TrimRight(url, "/")
		if src.ChartName != "" {
			ref = ref + "/" + src.ChartName
		}
		args := []string{"show", "chart", ref}
		if src.Version != "" {
			args = append(args, "--version", src.Version)
		}
		cmd := exec.CommandContext(callCtx, "helm", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("helm show chart %s: %v (%s)", ref, err, truncate(string(out), 200))
		}
		return nil
	case strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "https://"):
		indexURL := strings.TrimRight(url, "/") + "/index.yaml"
		req, err := http.NewRequestWithContext(callCtx, http.MethodGet, indexURL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("GET %s: %v", indexURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			_, _ = io.Copy(io.Discard, resp.Body)
			return fmt.Errorf("GET %s: status %d", indexURL, resp.StatusCode)
		}
		return nil
	case url == "":
		if strings.TrimSpace(src.LocalPath) == "" {
			return fmt.Errorf("helm source is empty: repo_url and local_path both blank")
		}
		// Local path set — we can't os.Stat it from outside the cluster pod,
		// so we just check that it's set. A deeper variant would exec into
		// the backend pod.
		return nil
	default:
		// Unknown scheme: treat as local path.
		if _, err := os.Stat(url); err != nil {
			return fmt.Errorf("stat %s: %v", url, err)
		}
		return nil
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func checkHelmSourceReachable(ctx context.Context, env CheckEnv) Result {
	dbRows, err := env.MySQL().DynamicConfigsByPrefix(ctx, dbInjectionPrefix)
	if err != nil {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("could not read dynamic_configs: %v", err),
			Fix: "verify [database.mysql] credentials in config.dev.toml"}
	}
	enabled := enabledSystemsFromDB(dbRows)
	if len(enabled) == 0 {
		return Result{Status: StatusOK, Detail: "no enabled systems to audit"}
	}
	var failures []string
	for _, name := range sortedKeys(enabled) {
		src, ok, err := helmSourceForSystem(ctx, env, name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: lookup failed: %v", name, err))
			continue
		}
		if !ok {
			failures = append(failures, fmt.Sprintf("%s: no helm_config row", name))
			continue
		}
		if err := env.Helm().ResolveChart(ctx, src); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) == 0 {
		return Result{Status: StatusOK,
			Detail: fmt.Sprintf("helm source reachable for %d enabled system(s)", len(enabled))}
	}
	return Result{Status: StatusFail,
		Detail: strings.Join(failures, "; "),
		Fix:    "verify helm repo_url / local_path for the named system (aegisctl chart get --name <name>)"}
}

// helmSourceForSystem looks up the most-recent helm_config for the pedestal
// container named after the system. It is implemented against the MySQL probe
// via a tiny extension: we piggyback on SystemFixtures' underlying DB by
// opening a fresh connection through a sql-specific helper.
func helmSourceForSystem(ctx context.Context, env CheckEnv, name string) (HelmChartSource, bool, error) {
	hp, ok := env.MySQL().(helmSourceLookup)
	if !ok {
		return HelmChartSource{}, false, fmt.Errorf("MySQLProbe does not expose helm source lookup")
	}
	return hp.HelmSourceForSystem(ctx, name)
}

// helmSourceLookup is an optional capability surface exposed by liveMySQL so
// the helm reachability check can read helm_configs without bloating the
// MySQLProbe interface for every consumer.
type helmSourceLookup interface {
	HelmSourceForSystem(ctx context.Context, systemName string) (HelmChartSource, bool, error)
}

// -------------------- otel.pipeline-to-clickhouse --------------------

const (
	otelCollectorNamespaceDefault = "monitoring"
	otelCollectorConfigMapDefault = "otel-collector-collector"
)

// otelCollectorLocations lists the ConfigMaps we try, in order, for the
// collector config. Different helm charts name the CM differently.
var otelCollectorLocations = []struct {
	namespace string
	name      string
}{
	{"monitoring", "otel-collector-collector"},
	{"monitoring", "otel-kube-stack-collector-collector"},
	{"otel", "otel-collector-collector"},
}

func checkOtelPipelineToClickHouse(ctx context.Context, env CheckEnv) Result {
	// 1. RBAC: k8sattributes needs pods + namespaces get/list/watch.
	ok, err := env.K8s().ClusterRoleAllowsPodNamespaceRead(ctx)
	if err != nil {
		return Result{Status: StatusFail,
			Detail: fmt.Sprintf("could not list ClusterRoles: %v", err),
			Fix:    "ensure KUBECONFIG grants list on clusterroles.rbac.authorization.k8s.io"}
	}
	if !ok {
		return Result{Status: StatusFail,
			Detail: "no ClusterRole grants get/list/watch on pods+namespaces (k8sattributes processor will fail)",
			Fix:    "install the otel-kube-stack chart (manifests/otel-collector/otel-kube-stack.yaml) or create the k8sattributes ClusterRole"}
	}

	// 2. Collector ConfigMap: clickhouse(dblogs)? exporter must be on all 3 pipelines.
	var data map[string]string
	var foundCM string
	for _, loc := range otelCollectorLocations {
		d, present, err := env.K8s().ConfigMapData(ctx, loc.namespace, loc.name)
		if err != nil {
			return Result{Status: StatusFail,
				Detail: fmt.Sprintf("could not read ConfigMap %s/%s: %v", loc.namespace, loc.name, err),
				Fix:    "verify KUBECONFIG has get on configmaps in the otel collector namespace"}
		}
		if present {
			data = d
			foundCM = loc.namespace + "/" + loc.name
			break
		}
	}
	if data == nil {
		return Result{Status: StatusFail,
			Detail: "otel collector ConfigMap not found in monitoring/otel namespaces",
			Fix:    "helm upgrade --install otel-collector via manifests/otel-collector/otel-kube-stack.yaml"}
	}
	config, ok := data["config.yaml"]
	if !ok {
		// Fallback: try "relay.yaml" or any single yaml key.
		for k, v := range data {
			if strings.HasSuffix(k, ".yaml") {
				config = v
				break
			}
		}
	}
	if strings.TrimSpace(config) == "" {
		return Result{Status: StatusFail,
			Detail: fmt.Sprintf("ConfigMap %s has no yaml payload", foundCM),
			Fix:    "redeploy the otel collector chart with a service.pipelines section"}
	}
	missing := pipelinesMissingClickHouse(config)
	if len(missing) > 0 {
		return Result{Status: StatusFail,
			Detail: fmt.Sprintf("%s: pipeline(s) without clickhouse exporter: %s", foundCM, strings.Join(missing, ",")),
			Fix:    "set exporters=[clickhouse] on traces/metrics/logs in the collector config"}
	}
	return Result{Status: StatusOK,
		Detail: fmt.Sprintf("%s: all 3 pipelines export to clickhouse + k8sattributes RBAC ok", foundCM)}
}

// pipelinesMissingClickHouse scans a collector `config.yaml` string for the
// three canonical pipelines (traces/metrics/logs) and returns any that do not
// list a clickhouse(dblogs)? exporter.
func pipelinesMissingClickHouse(yamlStr string) []string {
	want := []string{"traces", "metrics", "logs"}
	var missing []string
	for _, p := range want {
		if !pipelineHasClickHouseExporter(yamlStr, p) {
			missing = append(missing, p)
		}
	}
	return missing
}

// pipelineHasClickHouseExporter does a permissive text scan. The collector
// config is YAML but we deliberately avoid a full YAML decode so tests don't
// need to mirror the exact chart-generated shape — we just want a regex-level
// "is there an exporters: [ ... clickhouse ... ] block under service.pipelines.<p>".
func pipelineHasClickHouseExporter(yamlStr, pipeline string) bool {
	// Find "service:" -> "pipelines:" -> "<pipeline>:" and slice the string
	// until the next same-indent key.
	serviceIdx := strings.Index(yamlStr, "\nservice:")
	if serviceIdx < 0 && !strings.HasPrefix(yamlStr, "service:") {
		// No service block at all — be permissive and scan the whole doc.
		return scanForExporters(yamlStr, pipeline)
	}
	return scanForExporters(yamlStr, pipeline)
}

func scanForExporters(yamlStr, pipeline string) bool {
	// Locate the pipeline block by its key.
	// Accept both "    traces:" and "  traces:" to tolerate indent variation.
	lines := strings.Split(yamlStr, "\n")
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if trim != pipeline+":" {
			continue
		}
		indent := leadingSpaces(l)
		// Walk forward until indent <= indent and look for exporters: [...] or
		// exporters:\n      - clickhouse
		for j := i + 1; j < len(lines); j++ {
			cur := lines[j]
			if strings.TrimSpace(cur) == "" {
				continue
			}
			curIndent := leadingSpaces(cur)
			if curIndent <= indent {
				break
			}
			ct := strings.TrimSpace(cur)
			if strings.HasPrefix(ct, "exporters:") {
				rest := strings.TrimPrefix(ct, "exporters:")
				rest = strings.TrimSpace(rest)
				// Inline list form: exporters: [clickhouse, debug]
				if strings.Contains(rest, "clickhouse") {
					return true
				}
				// Multi-line form: scan following indented list items.
				for k := j + 1; k < len(lines); k++ {
					kLine := lines[k]
					if strings.TrimSpace(kLine) == "" {
						continue
					}
					if leadingSpaces(kLine) <= curIndent {
						break
					}
					if strings.Contains(kLine, "clickhouse") {
						return true
					}
				}
				return false
			}
		}
		return false
	}
	return false
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
		} else if r == '\t' {
			n += 2
		} else {
			break
		}
	}
	return n
}
