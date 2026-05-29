// manifestgen reads per-system chaos-experiment data files and emits
// aegis-chaos Point Manifests under aegislab/manifests/aegis-chaos/<system>/.
//
// Two manifest families are written per (system, service):
//   - <service>.yaml — the workload-agnostic base (pod/container/cpu/mem/time)
//     plus http_request_delay/abort and jvm_method_latency.
//   - <service>-<category>-A1b.yaml — derived chaos for http (response/replace
//     mutations), dns, network, jvm-method, and jvm-mysql.
//
// Source data lives in vendored copies of the chaos-experiment static
// data files under ./data; we parse them as text via go/parser AST rather
// than importing, so this tool stays self-contained and the chaos-experiment
// module need not be on disk.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrVarNotFound is returned by parseMapLiteral when the target var name
// is absent from the parsed Go file. We match this with errors.Is rather
// than the previous strings.Contains(err, "no such file") hack which
// would swallow it whenever a var name happened to share that substring.
var ErrVarNotFound = errors.New("manifestgen: ast variable not found")

const (
	defaultChartVersion = "seed-genesis"
	manifestInstance    = "seed"
	apiVersion          = "aegis-chaos/v1beta"
	kind                = "PointManifest"
	replaceScope        = "service"
)

// highCardinalitySegment matches a whole path segment that is a
// high-cardinality identifier:
//   - a UUID (8-4-4-4-12 hex)
//   - a bare numeric id (123)
//   - a short-prefix id code like a train number (D002, G1234, K85):
//     1-3 uppercase letters + digits. These blow up ts-ui-dashboard with
//     ~900 distinct codes. The uppercase requirement keeps version segments
//     (v1) and word routes (configs, stations) untouched.
var highCardinalitySegment = regexp.MustCompile(`^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|\d+|[A-Z]{1,3}\d+)$`)

// grpcRoutePattern matches a bare gRPC pseudo-route (/package.Service/Method).
// HTTPChaos response/request-mutation caps are skipped for these — chaos-mesh
// HTTPChaos operates on HTTP/1.x and never matches a single-segment gRPC route.
// Multi-segment REST paths that merely contain a dotted first segment (e.g.
// teastore's /tools.descartes.teastore.registry/rest/...) are NOT gRPC.
var grpcRoutePattern = regexp.MustCompile(`^/[A-Za-z_][A-Za-z0-9_.]*\.[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*$`)

// normalizePath folds high-cardinality segments to "*" so per-request
// endpoints collapse to one chaos point (mirrors the clickhouseanalyzer
// route normalization, e.g. adminorder/[uuid]/[A-Z]\d+ → adminorder/*/*).
// Splitting on "/" handles adjacent id segments that an overlapping regex
// replace would miss.
func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if highCardinalitySegment.MatchString(s) {
			segs[i] = "*"
		}
	}
	return strings.Join(segs, "/")
}

// systemDisplay is the value written into metadata.system, the output
// folder name, and the target namespace. It is the canonical SystemType
// (e.g. "otel-demo"); the seed manifests use it as the namespace because
// the single-instance seed deployment lives in a namespace named for the
// system code.
var systemDisplay = map[string]string{
	"ts":       "ts",
	"oteldemo": "otel-demo",
	"media":    "media",
	"hs":       "hs",
	"sn":       "sn",
	"ob":       "ob",
	"sockshop": "sockshop",
	"teastore": "teastore",
}

// orderedSystems is the canonical iteration order for deterministic output.
var orderedSystems = []string{"hs", "media", "ob", "oteldemo", "sn", "sockshop", "teastore", "ts"}

// appLabel maps a chaos-experiment service-data key to the pod app-label
// value used in chaos targets. For teastore the trace-derived data keys
// carry the deployment prefix (teastore-webui) but the pods are labelled
// with the short name (webui); chaos addresses pods by the app label, so
// targets and metadata.service use the stripped form. The deployment name
// (filename and target.container) keeps the full key.
func appLabel(sysKey, service string) string {
	if sysKey == "teastore" {
		return strings.TrimPrefix(service, "teastore-")
	}
	return service
}

// HTTPChaos response/request-mutation capabilities. Emitted per (app, port,
// method, path) into the http category; gRPC pseudo-routes are excluded.
var (
	httpRespCaps = []string{
		"http_response_abort",
		"http_response_delay",
		"http_response_replace_code",
		"http_response_patch_body",
		"http_response_replace_body",
	}
	httpReqMutationCaps = []string{
		"http_request_replace_method",
		"http_request_replace_path",
	}
	dnsCaps       = []string{"dns_error", "dns_random"}
	networkCaps   = []string{"network_delay", "network_loss", "network_duplicate", "network_corrupt", "network_bandwidth", "network_partition"}
	jvmMethodCaps = []string{"jvm_method_return", "jvm_method_exception", "jvm_cpu_stress", "jvm_memory_stress"}
	jvmMysqlCaps  = []string{"jvm_mysql_latency", "jvm_mysql_exception"}

	validMySQLTypes = map[string]bool{"select": true, "insert": true, "update": true, "delete": true, "replace": true, "all": true}
)

type httpEndpoint struct {
	Method        string
	Route         string
	Port          string
	ServerAddress string
	SpanName      string
	// grpc marks an endpoint folded from gRPC operations. Such endpoints
	// still seed http_request_delay/abort in the base manifest, but are
	// excluded from the http A1b response/replace family — chaos-mesh
	// HTTPChaos cannot act on gRPC/HTTP-2 calls.
	grpc bool
}

type classMethod struct {
	Class  string
	Method string
}

type dbOp struct {
	DBName   string
	Table    string
	SQLType  string
	DBSystem string
}

type grpcOp struct {
	Service       string
	ServerAddress string
}

type systemData struct {
	sysKey    string
	namespace string
	// service → HTTP/gRPC-folded endpoints (deduplicated)
	endpoints map[string][]httpEndpoint
	// service → class/method pairs (deduplicated)
	methods map[string][]classMethod
	// service → mysql database operations
	dbOps map[string][]dbOp
	// client-side gRPC operations across all services (DNS gRPC-only filter)
	grpcClientOps []grpcOp
	// union of every service name observed anywhere in this system
	services map[string]struct{}
	// presentFamilies records which capability-family source files existed
	// at load time. Empty data after a present file means the source was
	// truncated or the AST var name drifted — the run should fail loudly
	// rather than emit silent zero-point manifests.
	presentFamilies map[string]bool
}

type point struct {
	Capability string
	Target     map[string]any
}

type manifestMeta struct {
	System       string
	Service      string
	Instance     string
	ChartVersion string
}

// stats tallies counts for the final summary.
type stats struct {
	systems          int
	servicesBySystem map[string]int
	pointsBySystem   map[string]int
	skippedSystems   []string

	httpReqBySystem   map[string]int
	httpA1bBySystem   map[string]int
	dnsBySystem       map[string]int
	networkBySystem   map[string]int
	jvmMethodBySystem map[string]int
	jvmMysqlBySystem  map[string]int
}

func main() {
	chaosRoot := flag.String("chaos-root", "./data", "path to vendored chaos-experiment data (per-system subdirs)")
	outRoot := flag.String("out", "../../manifests/aegis-chaos", "output root directory")
	date := flag.String("date", defaultChartVersion, "chart_version stamp; defaults to seed-genesis for deterministic re-runs")
	flag.Parse()

	if err := run(*chaosRoot, *outRoot, *date); err != nil {
		fmt.Fprintln(os.Stderr, "manifestgen:", err)
		os.Exit(1)
	}
}

func run(chaosRoot, outRoot, chartVersion string) error {
	st := stats{
		servicesBySystem:  map[string]int{},
		pointsBySystem:    map[string]int{},
		httpReqBySystem:   map[string]int{},
		httpA1bBySystem:   map[string]int{},
		dnsBySystem:       map[string]int{},
		networkBySystem:   map[string]int{},
		jvmMethodBySystem: map[string]int{},
		jvmMysqlBySystem:  map[string]int{},
	}

	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return err
	}

	for _, sysKey := range orderedSystems {
		data, err := loadSystem(chaosRoot, sysKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: skipping system %s: %v\n", sysKey, err)
			st.skippedSystems = append(st.skippedSystems, sysKey)
			continue
		}
		if len(data.services) == 0 {
			fmt.Fprintf(os.Stderr, "warn: system %s has zero services in data — skipping\n", sysKey)
			st.skippedSystems = append(st.skippedSystems, sysKey)
			continue
		}

		sysDir := filepath.Join(outRoot, systemDisplay[sysKey])
		if err := os.MkdirAll(sysDir, 0o755); err != nil {
			return err
		}
		if err := removeStaleA1b(sysDir); err != nil {
			return err
		}

		services := sortedKeys(data.services)
		for _, svc := range services {
			svcLabel := appLabel(sysKey, svc)
			base := buildBasePoints(svc, data, &st, sysKey)
			if len(base) > 0 {
				if err := writeManifest(filepath.Join(sysDir, svc+".yaml"), manifestMeta{
					System: systemDisplay[sysKey], Service: svcLabel, Instance: manifestInstance, ChartVersion: chartVersion,
				}, base); err != nil {
					return err
				}
				st.servicesBySystem[sysKey]++
				st.pointsBySystem[sysKey] += len(base)
			}

			categories := map[string][]point{
				"http":       buildHTTPA1bPoints(svc, data),
				"dns":        buildDNSPoints(svc, data),
				"network":    buildNetworkPoints(svc, data),
				"jvm-method": buildJVMMethodPoints(svc, data),
				"jvm-mysql":  buildJVMMysqlPoints(svc, data),
			}
			for _, cat := range []string{"dns", "http", "jvm-method", "jvm-mysql", "network"} {
				pts := categories[cat]
				if len(pts) == 0 {
					continue
				}
				sortPoints(pts)
				if err := writeManifest(filepath.Join(sysDir, fmt.Sprintf("%s-%s-A1b.yaml", svc, cat)), manifestMeta{
					System: systemDisplay[sysKey], Service: svcLabel, Instance: manifestInstance, ChartVersion: chartVersion,
				}, pts); err != nil {
					return err
				}
				st.pointsBySystem[sysKey] += len(pts)
				switch cat {
				case "http":
					st.httpA1bBySystem[sysKey] += len(pts)
				case "dns":
					st.dnsBySystem[sysKey] += len(pts)
				case "network":
					st.networkBySystem[sysKey] += len(pts)
				case "jvm-method":
					st.jvmMethodBySystem[sysKey] += len(pts)
				case "jvm-mysql":
					st.jvmMysqlBySystem[sysKey] += len(pts)
				}
			}
		}
		st.systems++

		// Sanity floor: a present family file must yield non-zero points.
		// Mismatch means the source was truncated, the AST var name drifted,
		// or the loader silently swallowed a parse error.
		var mismatches []string
		if (data.presentFamilies["serviceendpoints"] || data.presentFamilies["grpcoperations"]) && st.httpReqBySystem[sysKey] == 0 {
			mismatches = append(mismatches, "http (serviceendpoints/grpcoperations file present, 0 http_request_* points)")
		}
		if data.presentFamilies["javaclassmethods"] && st.jvmMethodBySystem[sysKey] == 0 {
			mismatches = append(mismatches, "jvm-method (javaclassmethods file present, 0 jvm-method A1b points)")
		}
		if len(mismatches) > 0 {
			return fmt.Errorf("system %s: capability-family floor failed: %s", sysKey, strings.Join(mismatches, "; "))
		}
	}

	printSummary(os.Stdout, st)
	return nil
}

// buildBasePoints emits the workload-agnostic baseline plus the canonical
// http_request_delay/abort pair and jvm_method_latency. http request points
// are emitted for every endpoint including gRPC pseudo-routes (unchanged
// from the historical base behavior).
func buildBasePoints(service string, d *systemData, st *stats, sysKey string) []point {
	ns := d.namespace
	// WHY container=service: chaos-experiment data carries no container
	// name, but the 8 benchmark charts all follow the k8s convention
	// deployment.name == pod.label.app == container[0].name. Defaulting
	// container=service lets us emit container_kill / cpu_stress /
	// memory_stress / time_skew whose seed target_schema requires
	// `container`. If a benchmark ever deviates, the rendered chaos-mesh
	// CR will no-op against a non-existent container — loud runtime
	// failure beats silent fudging at manifest time.
	app := appLabel(d.sysKey, service)
	appOnly := map[string]any{"namespace": ns, "app": app}
	withContainer := map[string]any{"namespace": ns, "app": app, "container": service}
	pts := []point{
		{Capability: "container_kill", Target: cloneTarget(withContainer)},
		{Capability: "cpu_stress", Target: cloneTarget(withContainer)},
		{Capability: "memory_stress", Target: cloneTarget(withContainer)},
		{Capability: "pod_failure", Target: cloneTarget(appOnly)},
		{Capability: "pod_kill", Target: cloneTarget(appOnly)},
		{Capability: "time_skew", Target: cloneTarget(withContainer)},
	}

	httpSeen := map[string]struct{}{}
	for _, e := range d.endpoints[service] {
		target, ok := httpTarget(ns, app, e)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%v|%s|%s", target["port"], target["method"], target["path"])
		if _, dup := httpSeen[key]; dup {
			continue
		}
		httpSeen[key] = struct{}{}
		pts = append(pts, point{Capability: "http_request_delay", Target: cloneTarget(target)})
		pts = append(pts, point{Capability: "http_request_abort", Target: cloneTarget(target)})
		st.httpReqBySystem[sysKey] += 2
	}

	for _, cm := range d.methods[service] {
		pts = append(pts, point{Capability: "jvm_method_latency", Target: map[string]any{
			"namespace": ns, "app": app, "class": cm.Class, "method": cm.Method,
		}})
	}

	sortPoints(pts)
	return pts
}

// buildHTTPA1bPoints emits the response/request-mutation HTTPChaos caps per
// (app, port, method, path). gRPC pseudo-routes are excluded — chaos-mesh
// HTTPChaos operates on HTTP/1.x and never matches them.
func buildHTTPA1bPoints(service string, d *systemData) []point {
	app := appLabel(d.sysKey, service)
	var pts []point
	seen := map[string]struct{}{}
	for _, e := range d.endpoints[service] {
		if e.grpc || grpcRoutePattern.MatchString(e.Route) {
			continue
		}
		target, ok := httpTarget(d.namespace, app, e)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%v|%s|%s", target["port"], target["method"], target["path"])
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		for _, cap := range httpRespCaps {
			pts = append(pts, point{Capability: cap, Target: cloneTarget(target)})
		}
		for _, cap := range httpReqMutationCaps {
			pts = append(pts, point{Capability: cap, Target: cloneTarget(target)})
		}
	}
	return pts
}

// httpTarget validates and normalizes one endpoint into an http chaos target.
func httpTarget(ns, service string, e httpEndpoint) (map[string]any, bool) {
	port, err := strconv.Atoi(e.Port)
	if err != nil || port <= 0 || port > 65535 {
		return nil, false
	}
	method := strings.ToUpper(strings.TrimSpace(e.Method))
	if !isAllowedHTTPMethod(method) {
		return nil, false
	}
	path := normalizePath(strings.TrimSpace(e.Route))
	if path == "" {
		return nil, false
	}
	return map[string]any{
		"namespace": ns,
		"app":       service,
		"port":      port,
		"method":    method,
		"path":      path,
	}, true
}

// buildDNSPoints derives (app, domain) pairs from each service's endpoint
// ServerAddress values (excluding self), then emits dns_error/dns_random.
// gRPC-only pairs are filtered out — DNS chaos cannot match them.
func buildDNSPoints(service string, d *systemData) []point {
	grpcOnly := d.grpcOnlyPairs()
	domains := map[string]struct{}{}
	for _, e := range d.endpoints[service] {
		if e.grpc || e.ServerAddress == "" || e.ServerAddress == service {
			continue
		}
		if grpcOnly[service+"->"+e.ServerAddress] {
			continue
		}
		domains[e.ServerAddress] = struct{}{}
	}
	app := appLabel(d.sysKey, service)
	var pts []point
	for _, domain := range sortedKeys(domains) {
		for _, cap := range dnsCaps {
			pts = append(pts, point{Capability: cap, Target: map[string]any{
				"namespace":       d.namespace,
				"app":             app,
				"domain_patterns": []any{domain},
			}})
		}
	}
	return pts
}

// buildNetworkPoints derives forward (source→target) pairs from each
// service's endpoint ServerAddress values (excluding self) and emits the 6
// network caps per pair.
func buildNetworkPoints(service string, d *systemData) []point {
	targets := map[string]struct{}{}
	for _, e := range d.endpoints[service] {
		if e.ServerAddress == "" || e.ServerAddress == service {
			continue
		}
		targets[appLabel(d.sysKey, e.ServerAddress)] = struct{}{}
	}
	app := appLabel(d.sysKey, service)
	var pts []point
	for _, target := range sortedKeys(targets) {
		for _, cap := range networkCaps {
			pts = append(pts, point{Capability: cap, Target: map[string]any{
				"namespace":      d.namespace,
				"source_app":     app,
				"target_service": target,
			}})
		}
	}
	return pts
}

func buildJVMMethodPoints(service string, d *systemData) []point {
	app := appLabel(d.sysKey, service)
	var pts []point
	for _, cm := range d.methods[service] {
		for _, cap := range jvmMethodCaps {
			pts = append(pts, point{Capability: cap, Target: map[string]any{
				"namespace": d.namespace, "app": app, "class": cm.Class, "method": cm.Method,
			}})
		}
	}
	return pts
}

func buildJVMMysqlPoints(service string, d *systemData) []point {
	app := appLabel(d.sysKey, service)
	var pts []point
	seen := map[string]struct{}{}
	for _, op := range d.dbOps[service] {
		if op.DBSystem != "mysql" || op.DBName == "" || op.Table == "" {
			continue
		}
		sqlType := strings.ToLower(strings.TrimSpace(op.SQLType))
		if !validMySQLTypes[sqlType] {
			sqlType = "all"
		}
		key := op.DBName + "|" + op.Table + "|" + sqlType
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		for _, cap := range jvmMysqlCaps {
			pts = append(pts, point{Capability: cap, Target: map[string]any{
				"namespace": d.namespace, "app": app, "db_name": op.DBName, "table": op.Table, "sql_type": sqlType,
			}})
		}
	}
	return pts
}

// grpcOnlyPairs returns the set of "source->target" pairs that communicate
// only via gRPC (have a client gRPC op but no HTTP route). DNS chaos is
// skipped for these.
func (d *systemData) grpcOnlyPairs() map[string]bool {
	grpcPairs := map[string]bool{}
	for _, op := range d.grpcClientOps {
		if op.Service == "" || op.ServerAddress == "" {
			continue
		}
		grpcPairs[op.Service+"->"+op.ServerAddress] = true
	}
	httpPairs := map[string]bool{}
	for svc, eps := range d.endpoints {
		for _, e := range eps {
			if e.ServerAddress == "" || e.ServerAddress == svc {
				continue
			}
			if e.Route != "" && !isGRPCRoutePattern(e.Route) {
				httpPairs[svc+"->"+e.ServerAddress] = true
			}
		}
	}
	out := map[string]bool{}
	for pair := range grpcPairs {
		if !httpPairs[pair] {
			out[pair] = true
		}
	}
	return out
}

// isGRPCRoutePattern reports whether a route looks like a gRPC pseudo-route
// (/package.Service/Method): a leading slash and a dot before the next slash.
func isGRPCRoutePattern(route string) bool {
	if len(route) < 3 || route[0] != '/' {
		return false
	}
	for i := 1; i < len(route); i++ {
		if route[i] == '/' {
			return false
		}
		if route[i] == '.' {
			return true
		}
	}
	return false
}

func isAllowedHTTPMethod(m string) bool {
	switch m {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}

func cloneTarget(t map[string]any) map[string]any {
	out := make(map[string]any, len(t))
	for k, v := range t {
		out[k] = v
	}
	return out
}

func sortPoints(pts []point) {
	sort.SliceStable(pts, func(i, j int) bool {
		if pts[i].Capability != pts[j].Capability {
			return pts[i].Capability < pts[j].Capability
		}
		return targetKey(pts[i].Target) < targetKey(pts[j].Target)
	})
}

func targetKey(t map[string]any) string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, t[k])
	}
	return b.String()
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func removeStaleA1b(sysDir string) error {
	stale, err := filepath.Glob(filepath.Join(sysDir, "*-A1b.yaml"))
	if err != nil {
		return err
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

func printSummary(w *os.File, st stats) {
	totalServices, totalPoints := 0, 0
	for _, k := range orderedSystems {
		if st.servicesBySystem[k] == 0 {
			continue
		}
		fmt.Fprintf(w, "system=%-10s services=%-4d points=%-6d (httpReq=%d httpA1b=%d dns=%d net=%d jvmM=%d jvmSQL=%d)\n",
			systemDisplay[k], st.servicesBySystem[k], st.pointsBySystem[k],
			st.httpReqBySystem[k], st.httpA1bBySystem[k], st.dnsBySystem[k],
			st.networkBySystem[k], st.jvmMethodBySystem[k], st.jvmMysqlBySystem[k])
		totalServices += st.servicesBySystem[k]
		totalPoints += st.pointsBySystem[k]
	}
	fmt.Fprintf(w, "TOTAL: systems=%d services=%d points=%d\n", st.systems, totalServices, totalPoints)
	if len(st.skippedSystems) > 0 {
		fmt.Fprintf(w, "skipped systems (no data): %s\n", strings.Join(st.skippedSystems, ", "))
	}
}

// ---------- Data loading via go/parser ----------

// loadSystem parses the four data files under <chaosRoot>/<sys>/.
// Missing subdirs are tolerated (e.g. Go-stack systems lack javaclassmethods/).
func loadSystem(chaosRoot, sysKey string) (*systemData, error) {
	root := filepath.Join(chaosRoot, sysKey)
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("system root not found: %w", err)
	}
	ns, ok := systemDisplay[sysKey]
	if !ok {
		return nil, fmt.Errorf("no namespace mapping for %s", sysKey)
	}
	d := &systemData{
		sysKey:          sysKey,
		namespace:       ns,
		endpoints:       map[string][]httpEndpoint{},
		methods:         map[string][]classMethod{},
		dbOps:           map[string][]dbOp{},
		services:        map[string]struct{}{},
		presentFamilies: map[string]bool{},
	}

	sePath := filepath.Join(root, "serviceendpoints", "serviceendpoints.go")
	if _, err := os.Stat(sePath); err == nil {
		d.presentFamilies["serviceendpoints"] = true
	}
	if err := loadHTTPEndpoints(sePath, d); err != nil {
		return nil, fmt.Errorf("serviceendpoints: %w", err)
	}
	grpcPath := filepath.Join(root, "grpcoperations", "grpcoperations.go")
	if _, err := os.Stat(grpcPath); err == nil {
		d.presentFamilies["grpcoperations"] = true
	}
	if _, err := os.Stat(filepath.Join(root, "javaclassmethods", "javaclassmethods.go")); err == nil {
		d.presentFamilies["javaclassmethods"] = true
	}
	if err := loadGRPCOperations(grpcPath, d); err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, ErrVarNotFound) {
			return nil, fmt.Errorf("grpcoperations: %w", err)
		}
	}
	if err := loadDatabaseOperations(filepath.Join(root, "databaseoperations", "databaseoperations.go"), d); err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, ErrVarNotFound) {
			return nil, fmt.Errorf("databaseoperations: %w", err)
		}
	}
	if err := loadJavaClassMethods(filepath.Join(root, "javaclassmethods", "javaclassmethods.go"), d); err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, ErrVarNotFound) {
			return nil, fmt.Errorf("javaclassmethods: %w", err)
		}
	}

	dedupHTTP(d)
	dedupMethods(d)
	return d, nil
}

func dedupHTTP(d *systemData) {
	for svc, list := range d.endpoints {
		seen := map[string]struct{}{}
		out := make([]httpEndpoint, 0, len(list))
		for _, e := range list {
			k := e.Method + "|" + e.Route + "|" + e.Port + "|" + e.ServerAddress
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, e)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Route != out[j].Route {
				return out[i].Route < out[j].Route
			}
			if out[i].Method != out[j].Method {
				return out[i].Method < out[j].Method
			}
			return out[i].Port < out[j].Port
		})
		d.endpoints[svc] = out
	}
}

func dedupMethods(d *systemData) {
	for svc, list := range d.methods {
		seen := map[string]struct{}{}
		out := make([]classMethod, 0, len(list))
		for _, cm := range list {
			k := cm.Class + "|" + cm.Method
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, cm)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Class != out[j].Class {
				return out[i].Class < out[j].Class
			}
			return out[i].Method < out[j].Method
		})
		d.methods[svc] = out
	}
}

// parseMapLiteral parses a Go file and returns the AST CompositeLit of
// the first top-level var declaration matching varName.
func parseMapLiteral(path, varName string) (*ast.CompositeLit, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if name.Name != varName || i >= len(vs.Values) {
					continue
				}
				cl, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					return nil, fmt.Errorf("%s: var %s is not a composite literal", path, varName)
				}
				return cl, nil
			}
		}
	}
	return nil, fmt.Errorf("%s in %s: %w", varName, path, ErrVarNotFound)
}

func loadHTTPEndpoints(path string, d *systemData) error {
	cl, err := parseMapLiteral(path, "ServiceEndpoints")
	if err != nil {
		return err
	}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		svc, ok := stringLit(kv.Key)
		if !ok {
			continue
		}
		d.services[svc] = struct{}{}
		entries, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, entry := range entries.Elts {
			rec, ok := entry.(*ast.CompositeLit)
			if !ok {
				continue
			}
			fields := structFields(rec)
			d.endpoints[svc] = append(d.endpoints[svc], httpEndpoint{
				Method:        fields["RequestMethod"],
				Route:         fields["Route"],
				Port:          fields["ServerPort"],
				ServerAddress: fields["ServerAddress"],
				SpanName:      fields["SpanName"],
			})
		}
	}
	return nil
}

// loadGRPCOperations folds gRPC operations into the endpoint family as
// POST /RPCService/RPCMethod (a gRPC pseudo-route) and records client-side
// ops for the DNS gRPC-only filter.
func loadGRPCOperations(path string, d *systemData) error {
	cl, err := parseMapLiteral(path, "GRPCOperations")
	if err != nil {
		return err
	}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		svc, ok := stringLit(kv.Key)
		if !ok {
			continue
		}
		d.services[svc] = struct{}{}
		entries, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, entry := range entries.Elts {
			rec, ok := entry.(*ast.CompositeLit)
			if !ok {
				continue
			}
			fields := structFields(rec)
			route := ""
			if fields["RPCService"] != "" && fields["RPCMethod"] != "" {
				route = "/" + fields["RPCService"] + "/" + fields["RPCMethod"]
			}
			d.endpoints[svc] = append(d.endpoints[svc], httpEndpoint{
				Method:        "POST",
				Route:         route,
				Port:          fields["ServerPort"],
				ServerAddress: fields["ServerAddress"],
				SpanName:      fields["SpanName"],
				grpc:          true,
			})
			if strings.EqualFold(fields["SpanKind"], "client") {
				d.grpcClientOps = append(d.grpcClientOps, grpcOp{
					Service:       svc,
					ServerAddress: fields["ServerAddress"],
				})
			}
		}
	}
	return nil
}

func loadDatabaseOperations(path string, d *systemData) error {
	cl, err := parseMapLiteral(path, "DatabaseOperations")
	if err != nil {
		return err
	}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		svc, ok := stringLit(kv.Key)
		if !ok {
			continue
		}
		d.services[svc] = struct{}{}
		entries, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, entry := range entries.Elts {
			rec, ok := entry.(*ast.CompositeLit)
			if !ok {
				continue
			}
			fields := structFields(rec)
			d.dbOps[svc] = append(d.dbOps[svc], dbOp{
				DBName:   fields["DBName"],
				Table:    fields["DBTable"],
				SQLType:  fields["Operation"],
				DBSystem: fields["DBSystem"],
			})
		}
	}
	return nil
}

func loadJavaClassMethods(path string, d *systemData) error {
	cl, err := parseMapLiteral(path, "ServiceClassMethods")
	if err != nil {
		return err
	}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		svc, ok := stringLit(kv.Key)
		if !ok {
			continue
		}
		d.services[svc] = struct{}{}
		entries, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, entry := range entries.Elts {
			rec, ok := entry.(*ast.CompositeLit)
			if !ok {
				continue
			}
			fields := structFields(rec)
			cls := fields["ClassName"]
			meth := fields["MethodName"]
			if cls == "" || meth == "" {
				continue
			}
			d.methods[svc] = append(d.methods[svc], classMethod{Class: cls, Method: meth})
		}
	}
	return nil
}

func structFields(cl *ast.CompositeLit) map[string]string {
	out := map[string]string{}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if v, ok := stringLit(kv.Value); ok {
			out[id.Name] = v
		}
	}
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// ---------- Manifest write ----------

func writeManifest(path string, meta manifestMeta, points []point) error {
	// We control the structure; emit deterministic YAML by hand. Each
	// generated YAML is a JSON-superset (no anchors, no folded scalars),
	// so jsonschema validators accept it after the YAML→JSON pass that
	// the bundled CLI already does.
	var b strings.Builder
	b.WriteString("apiVersion: " + apiVersion + "\n")
	b.WriteString("kind: " + kind + "\n")
	b.WriteString("metadata:\n")
	b.WriteString("  chart_version: " + yamlString(meta.ChartVersion) + "\n")
	b.WriteString("  instance: " + yamlString(meta.Instance) + "\n")
	b.WriteString("  service: " + yamlString(meta.Service) + "\n")
	b.WriteString("  system: " + yamlString(meta.System) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  replace_scope: " + yamlString(replaceScope) + "\n")
	b.WriteString("  points:\n")
	for _, p := range points {
		b.WriteString("    - capability: " + yamlString(p.Capability) + "\n")
		b.WriteString("      target:\n")
		keys := make([]string, 0, len(p.Target))
		for k := range p.Target {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeTargetField(&b, k, p.Target[k])
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeTargetField(b *strings.Builder, key string, v any) {
	if list, ok := v.([]any); ok {
		b.WriteString("        " + key + ":\n")
		for _, item := range list {
			b.WriteString("          - " + yamlValue(item) + "\n")
		}
		return
	}
	b.WriteString("        " + key + ": " + yamlValue(v) + "\n")
}

func yamlString(s string) string {
	// Always JSON-quote to be safe; YAML accepts JSON-quoted strings.
	q, _ := json.Marshal(s)
	return string(q)
}

func yamlValue(v any) string {
	switch x := v.(type) {
	case string:
		return yamlString(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
