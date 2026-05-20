// manifestgen reads per-system chaos-experiment data files and emits one
// aegis-chaos Point Manifest per (system, service) pair under
// aegislab/manifests/aegis-chaos/<system>/<service>.yaml.
//
// Source data lives in a separate Go module (chaos-experiment), so we
// parse it as text via go/parser AST instead of importing — keeps this
// tool self-contained, matching capgen's pattern.
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

// systemNamespace maps the chaos-experiment SystemType key to the
// canonical (non-multi-instance) namespace used for seed manifests.
var systemNamespace = map[string]string{
	"ts":        "ts",
	"oteldemo":  "otel-demo",
	"media":     "media-microsvc",
	"hs":        "hotel-reservation",
	"sn":        "social-network",
	"ob":        "online-boutique",
	"sockshop":  "sock-shop",
	"teastore":  "teastore",
}

// systemDisplay is the value written into metadata.system. It is the
// canonical SystemType (e.g. "otel-demo"), not the folder name.
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

type httpEndpoint struct {
	Method string
	Route  string
	Port   string
}

type classMethod struct {
	Class  string
	Method string
}

type systemData struct {
	namespace string
	// service → endpoints (deduplicated by (method, route, port))
	endpoints map[string][]httpEndpoint
	// service → class/method pairs (deduplicated)
	methods map[string][]classMethod
	// union of every service name observed anywhere in this system
	services map[string]struct{}
	// presentFamilies records which capability-family source files existed
	// at load time. Empty data after a present file means the source was
	// truncated or the AST var name drifted — the run should fail loudly
	// rather than emit silent zero-point manifests.
	presentFamilies map[string]bool
}

type point struct {
	Capability string         `json:"capability"`
	Target     map[string]any `json:"target"`
}

type manifestSpec struct {
	ReplaceScope string  `json:"replace_scope"`
	Points       []point `json:"points"`
}

type manifestMeta struct {
	System       string `json:"system"`
	Service      string `json:"service"`
	Instance     string `json:"instance"`
	ChartVersion string `json:"chart_version"`
}

type manifest struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   manifestMeta `json:"metadata"`
	Spec       manifestSpec `json:"spec"`
}

// stats tallies counts for the final summary.
type stats struct {
	systems          int
	servicesBySystem map[string]int
	pointsBySystem   map[string]int
	skippedSystems   []string
	// httpPointsBySystem counts http_request_* across the system; sourced
	// from serviceendpoints/grpcoperations files.
	httpPointsBySystem map[string]int
	// jvmPointsBySystem counts jvm_method_latency; sourced from javaclassmethods.
	jvmPointsBySystem map[string]int
}

func main() {
	chaosRoot := flag.String("chaos-root", "../../../chaos-experiment/internal", "path to chaos-experiment/internal")
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
		servicesBySystem:   map[string]int{},
		pointsBySystem:     map[string]int{},
		httpPointsBySystem: map[string]int{},
		jvmPointsBySystem:  map[string]int{},
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

		services := sortedKeys(data.services)
		for _, svc := range services {
			pts := buildPoints(svc, data)
			if len(pts) == 0 {
				continue
			}
			m := manifest{
				APIVersion: apiVersion,
				Kind:       kind,
				Metadata: manifestMeta{
					System:       systemDisplay[sysKey],
					Service:      svc,
					Instance:     manifestInstance,
					ChartVersion: chartVersion,
				},
				Spec: manifestSpec{ReplaceScope: replaceScope, Points: pts},
			}
			if err := writeManifest(filepath.Join(sysDir, svc+".yaml"), m); err != nil {
				return err
			}
			st.servicesBySystem[sysKey]++
			st.pointsBySystem[sysKey] += len(pts)
			for _, p := range pts {
				switch p.Capability {
				case "http_request_delay", "http_request_abort":
					st.httpPointsBySystem[sysKey]++
				case "jvm_method_latency":
					st.jvmPointsBySystem[sysKey]++
				}
			}
		}
		st.systems++

		// Sanity floor: a present family file must yield non-zero points.
		// Mismatch means the source was truncated, the AST var name drifted,
		// or the loader silently swallowed a parse error.
		var mismatches []string
		if (data.presentFamilies["serviceendpoints"] || data.presentFamilies["grpcoperations"]) && st.httpPointsBySystem[sysKey] == 0 {
			mismatches = append(mismatches, "http (serviceendpoints/grpcoperations file present, 0 http_request_* points)")
		}
		if data.presentFamilies["javaclassmethods"] && st.jvmPointsBySystem[sysKey] == 0 {
			mismatches = append(mismatches, "jvm (javaclassmethods file present, 0 jvm_method_latency points)")
		}
		if len(mismatches) > 0 {
			return fmt.Errorf("system %s: capability-family floor failed: %s", sysKey, strings.Join(mismatches, "; "))
		}
	}

	printSummary(os.Stdout, st)
	return nil
}

func buildPoints(service string, d *systemData) []point {
	ns := d.namespace
	// WHY container=service: chaos-experiment data carries no container
	// name, but the 8 benchmark charts all follow the k8s convention
	// deployment.name == pod.label.app == container[0].name. Defaulting
	// container=service lets us emit container_kill / cpu_stress /
	// memory_stress / time_skew whose seed target_schema requires
	// `container`. If a benchmark ever deviates, the rendered chaos-mesh
	// CR will no-op against a non-existent container — loud runtime
	// failure beats silent fudging at manifest time.
	appOnly := map[string]any{"namespace": ns, "app": service}
	withContainer := map[string]any{"namespace": ns, "app": service, "container": service}
	pts := []point{
		{Capability: "container_kill", Target: cloneTarget(withContainer)},
		{Capability: "cpu_stress", Target: cloneTarget(withContainer)},
		{Capability: "memory_stress", Target: cloneTarget(withContainer)},
		{Capability: "pod_failure", Target: cloneTarget(appOnly)},
		{Capability: "pod_kill", Target: cloneTarget(appOnly)},
		{Capability: "time_skew", Target: cloneTarget(withContainer)},
	}
	// WHY-skip network_*: seed schema needs {source_app, target_service}; no curated peer in data.
	// WHY-skip dns_error: seed schema needs domain_patterns; no per-service domain data.
	// WHY-skip jvm_mysql_*: seed schema needs class+method; databaseoperations only has db/table/sql_type.

	endpoints := d.endpoints[service]
	for _, e := range endpoints {
		port, err := strconv.Atoi(e.Port)
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(e.Method))
		if !isAllowedHTTPMethod(method) {
			continue
		}
		path := strings.TrimSpace(e.Route)
		if path == "" {
			continue
		}
		target := map[string]any{
			"namespace": ns,
			"app":       service,
			"port":      port,
			"method":    method,
			"path":      path,
		}
		pts = append(pts, point{Capability: "http_request_delay", Target: cloneTarget(target)})
		pts = append(pts, point{Capability: "http_request_abort", Target: cloneTarget(target)})
	}

	for _, cm := range d.methods[service] {
		target := map[string]any{
			"namespace": ns,
			"app":       service,
			"class":     cm.Class,
			"method":    cm.Method,
		}
		pts = append(pts, point{Capability: "jvm_method_latency", Target: target})
	}

	sortPoints(pts)
	return pts
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

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func printSummary(w *os.File, st stats) {
	totalServices, totalPoints := 0, 0
	for _, k := range orderedSystems {
		if st.servicesBySystem[k] == 0 {
			continue
		}
		fmt.Fprintf(w, "system=%-10s services=%-4d points=%d\n", systemDisplay[k], st.servicesBySystem[k], st.pointsBySystem[k])
		totalServices += st.servicesBySystem[k]
		totalPoints += st.pointsBySystem[k]
	}
	fmt.Fprintf(w, "TOTAL: systems=%d services=%d points=%d\n", st.systems, totalServices, totalPoints)
	if len(st.skippedSystems) > 0 {
		fmt.Fprintf(w, "skipped systems (no data): %s\n", strings.Join(st.skippedSystems, ", "))
	}
}

// ---------- Data loading via go/parser ----------

// loadSystem parses the four data files under chaos-experiment/internal/<sys>/.
// Missing subdirs are tolerated (e.g. Go-stack systems lack
// javaclassmethods/); a missing root means the system has no data at all.
func loadSystem(chaosRoot, sysKey string) (*systemData, error) {
	root := filepath.Join(chaosRoot, sysKey)
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("system root not found: %w", err)
	}
	ns, ok := systemNamespace[sysKey]
	if !ok {
		return nil, fmt.Errorf("no namespace mapping for %s", sysKey)
	}
	d := &systemData{
		namespace:       ns,
		endpoints:       map[string][]httpEndpoint{},
		methods:         map[string][]classMethod{},
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
	if _, err := os.Stat(filepath.Join(root, "grpcoperations", "grpcoperations.go")); err == nil {
		d.presentFamilies["grpcoperations"] = true
	}
	if _, err := os.Stat(filepath.Join(root, "javaclassmethods", "javaclassmethods.go")); err == nil {
		d.presentFamilies["javaclassmethods"] = true
	}
	if err := loadHTTPEndpoints(filepath.Join(root, "grpcoperations", "grpcoperations.go"), d); err != nil {
		// gRPC operations file is optional; only complain if present and unparseable.
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
			k := e.Method + "|" + e.Route + "|" + e.Port
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
	cl, err := parseMapLiteral(path, varNameForFile(path))
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
			ep := httpEndpoint{
				Method: fields["RequestMethod"],
				Route:  fields["Route"],
				Port:   fields["ServerPort"],
			}
			// gRPC operations: map RPCMethod onto Route as "/RPCService/RPCMethod"; emit as POST.
			if ep.Route == "" {
				if rs := fields["RPCService"]; rs != "" {
					if rm := fields["RPCMethod"]; rm != "" {
						ep.Route = "/" + rs + "/" + rm
						ep.Method = "POST"
					}
				}
			}
			d.endpoints[svc] = append(d.endpoints[svc], ep)
		}
	}
	return nil
}

func loadDatabaseOperations(path string, d *systemData) error {
	cl, err := parseMapLiteral(path, "DatabaseOperations")
	if err != nil {
		return err
	}
	// We still register the service name so it gets the workload-agnostic
	// points (pod_kill / pod_failure), even though jvm_mysql_* is skipped
	// for lack of class+method in the source data.
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if svc, ok := stringLit(kv.Key); ok {
			d.services[svc] = struct{}{}
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

func varNameForFile(path string) string {
	base := filepath.Base(path)
	switch base {
	case "serviceendpoints.go":
		return "ServiceEndpoints"
	case "grpcoperations.go":
		return "GRPCOperations"
	}
	return ""
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

func writeManifest(path string, m manifest) error {
	// We control the structure; emit deterministic YAML by hand. Each
	// generated YAML is a JSON-superset (no anchors, no folded scalars),
	// so jsonschema validators accept it after the YAML→JSON pass that
	// the bundled CLI already does.
	var b strings.Builder
	b.WriteString("apiVersion: " + apiVersion + "\n")
	b.WriteString("kind: " + kind + "\n")
	b.WriteString("metadata:\n")
	b.WriteString("  chart_version: " + yamlString(m.Metadata.ChartVersion) + "\n")
	b.WriteString("  instance: " + yamlString(m.Metadata.Instance) + "\n")
	b.WriteString("  service: " + yamlString(m.Metadata.Service) + "\n")
	b.WriteString("  system: " + yamlString(m.Metadata.System) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  replace_scope: " + yamlString(m.Spec.ReplaceScope) + "\n")
	b.WriteString("  points:\n")
	for _, p := range m.Spec.Points {
		b.WriteString("    - capability: " + yamlString(p.Capability) + "\n")
		b.WriteString("      target:\n")
		keys := make([]string, 0, len(p.Target))
		for k := range p.Target {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("        " + k + ": " + yamlValue(p.Target[k]) + "\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
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
