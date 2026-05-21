// migrate-chaos-metadata dumps per-system chaos metadata from the
// chaos-experiment in-memory providers (via the metadatasnapshot shim)
// into PointManifest YAML files under aegislab/manifests/aegis-chaos/<sys>/.
//
// Kept until Phase B for re-dumping when chaos_points target JSON schema
// widens. Genuinely deleted in Phase B alongside the chaos-experiment
// git mv.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/OperationsPAI/chaos-experiment/pkg/metadatasnapshot"
	sigsyaml "sigs.k8s.io/yaml"
)

// chaos-mesh HTTPChaos operates on HTTP/1.x; gRPC pseudo-routes
// (/pkg.Service/Method on HTTP/2) never match the rule. Mirrors the
// resourcelookup filter that drops gRPC-only pairs from DNS chaos.
var grpcRoutePattern = regexp.MustCompile(`^/[A-Za-z_][A-Za-z0-9_.]*\.[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*$`)

var systems = []string{
	"hs",
	"sn",
	"sockshop",
	"media",
	"ob",
	"otel-demo",
	"teastore",
	"ts",
}

var (
	httpReqMutations = []string{
		"http_request_replace_method",
		"http_request_replace_path",
	}
	httpRespActions = []string{
		"http_response_abort",
		"http_response_delay",
		"http_response_replace_code",
		"http_response_patch_body",
		"http_response_replace_body",
	}
	dnsCaps         = []string{"dns_error", "dns_random"}
	networkCaps     = []string{"network_delay", "network_loss", "network_duplicate", "network_corrupt", "network_bandwidth", "network_partition"}
	jvmMethodCaps   = []string{"jvm_method_return", "jvm_method_exception", "jvm_cpu_stress", "jvm_memory_stress"}
	jvmMysqlCaps    = []string{"jvm_mysql_latency", "jvm_mysql_exception"}
	validMySQLTypes = map[string]bool{"select": true, "insert": true, "update": true, "delete": true, "replace": true, "all": true}
	// chaos-mesh HTTPChaos accepts only this set per seeds_http schema.
	validHTTPMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true}
)

type point struct {
	Capability string
	Target     map[string]interface{}
}

type pointManifest struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Metadata   map[string]string      `json:"metadata"`
	Spec       map[string]interface{} `json:"spec"`
}

func main() {
	outDir := flag.String("out", "aegislab/manifests/aegis-chaos", "output dir")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	total := 0
	for _, sys := range systems {
		n, err := dumpSystem(sys, *outDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dump %s: %v\n", sys, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s: %d files\n", sys, n)
		total += n
	}
	fmt.Fprintf(os.Stdout, "total: %d files\n", total)
}

func dumpSystem(sys, outRoot string) (int, error) {
	snap, err := metadatasnapshot.DumpForSystem(sys)
	if err != nil {
		return 0, err
	}

	byService := map[string]map[string][]point{}
	add := func(service, category string, p point) {
		if byService[service] == nil {
			byService[service] = map[string][]point{}
		}
		byService[service][category] = append(byService[service][category], p)
	}

	ns := sys

	// HTTP — emit 7 caps per (app, port, method, path), dedup on that key.
	httpSeen := map[string]bool{}
	for _, ep := range snap.HTTPEndpoints {
		if ep.Method == "" || ep.Route == "" {
			continue
		}
		if grpcRoutePattern.MatchString(ep.Route) {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(ep.Method))
		if !validHTTPMethods[method] {
			continue
		}
		port := parsePort(ep.ServerPort)
		if port <= 0 || port > 65535 {
			continue
		}
		key := fmt.Sprintf("%s|%d|%s|%s", ep.AppName, port, method, ep.Route)
		if httpSeen[key] {
			continue
		}
		httpSeen[key] = true

		target := map[string]interface{}{
			"namespace": ns,
			"app":       ep.AppName,
			"port":      port,
			"method":    method,
			"path":      ep.Route,
		}
		// server_address + span_name are metadata-only fields used by
		// groundtruth; renderer_http ignores them. Chart-author manifests
		// can omit them — the DB-backed reader treats them as optional.
		if ep.ServerAddress != "" {
			target["server_address"] = ep.ServerAddress
		}
		if ep.SpanName != "" {
			target["span_name"] = ep.SpanName
		}
		for _, cap := range httpRespActions {
			add(ep.AppName, "http", point{Capability: cap, Target: cloneMap(target)})
		}
		for _, cap := range httpReqMutations {
			add(ep.AppName, "http", point{Capability: cap, Target: cloneMap(target)})
		}
	}

	// DNS — group by (app, domain), emit 2 caps per pair.
	for _, d := range snap.DNSEndpoints {
		if d.AppName == "" || d.Domain == "" {
			continue
		}
		target := map[string]interface{}{
			"namespace":       ns,
			"app":             d.AppName,
			"domain_patterns": []interface{}{d.Domain},
		}
		for _, cap := range dnsCaps {
			add(d.AppName, "dns", point{Capability: cap, Target: cloneMap(target)})
		}
	}

	// Network — emit 6 caps per (source, target) pair.
	for _, np := range snap.NetworkPairs {
		if np.SourceService == "" || np.TargetService == "" {
			continue
		}
		target := map[string]interface{}{
			"namespace":      ns,
			"source_app":     np.SourceService,
			"target_service": np.TargetService,
		}
		// span_names is metadata-only (renderer_network ignores it) and
		// optional for chart authors. The DB-backed reader surfaces it via
		// AppNetworkPair.SpanNames so groundtruth can label affected edges.
		if len(np.SpanNames) > 0 {
			spans := make([]interface{}, len(np.SpanNames))
			for i, s := range np.SpanNames {
				spans[i] = s
			}
			target["span_names"] = spans
		}
		for _, cap := range networkCaps {
			add(np.SourceService, "network", point{Capability: cap, Target: cloneMap(target)})
		}
	}

	// JVM methods — emit 4 caps per (app, class, method).
	for _, m := range snap.JVMMethods {
		if m.AppName == "" || m.ClassName == "" || m.MethodName == "" {
			continue
		}
		target := map[string]interface{}{
			"namespace": ns,
			"app":       m.AppName,
			"class":     m.ClassName,
			"method":    m.MethodName,
		}
		for _, cap := range jvmMethodCaps {
			add(m.AppName, "jvm-method", point{Capability: cap, Target: cloneMap(target)})
		}
	}

	// JVM mysql — emit 2 caps per (app, db, table, sql_type).
	for _, op := range snap.DBOperations {
		if op.AppName == "" || op.DBName == "" || op.TableName == "" {
			continue
		}
		sqlType := strings.ToLower(strings.TrimSpace(op.OperationType))
		if !validMySQLTypes[sqlType] {
			sqlType = "all"
		}
		target := map[string]interface{}{
			"namespace": ns,
			"app":       op.AppName,
			"db_name":   op.DBName,
			"table":     op.TableName,
			"sql_type":  sqlType,
		}
		for _, cap := range jvmMysqlCaps {
			add(op.AppName, "jvm-mysql", point{Capability: cap, Target: cloneMap(target)})
		}
	}

	sysDir := filepath.Join(outRoot, sys)
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", sysDir, err)
	}

	stale, err := filepath.Glob(filepath.Join(sysDir, "*-A1b.yaml"))
	if err != nil {
		return 0, fmt.Errorf("glob %s: %w", sysDir, err)
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return 0, fmt.Errorf("remove %s: %w", p, err)
		}
	}

	services := sortedKeys(byService)

	files := 0
	for _, svc := range services {
		categories := sortedKeys(byService[svc])
		for _, cat := range categories {
			points := byService[svc][cat]
			sortPoints(points)
			manifest := pointManifest{
				APIVersion: "aegis-chaos/v1beta",
				Kind:       "PointManifest",
				Metadata: map[string]string{
					"system":        sys,
					"service":       svc,
					"instance":      "seed",
					"chart_version": "seed-genesis",
				},
				Spec: map[string]interface{}{
					"replace_scope": "service",
					"points":        pointsToInterface(points),
				},
			}
			out, err := sigsyaml.Marshal(manifest)
			if err != nil {
				return files, fmt.Errorf("marshal %s/%s: %w", svc, cat, err)
			}
			path := filepath.Join(sysDir, fmt.Sprintf("%s-%s-A1b.yaml", svc, cat))
			if err := os.WriteFile(path, out, 0o644); err != nil {
				return files, fmt.Errorf("write %s: %w", path, err)
			}
			files++
		}
	}

	return files, nil
}

func pointsToInterface(pts []point) []interface{} {
	out := make([]interface{}, len(pts))
	for i, p := range pts {
		out[i] = map[string]interface{}{
			"capability": p.Capability,
			"target":     p.Target,
		}
	}
	return out
}

func sortPoints(pts []point) {
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].Capability != pts[j].Capability {
			return pts[i].Capability < pts[j].Capability
		}
		return canonical(pts[i].Target) < canonical(pts[j].Target)
	})
}

func canonical(m map[string]interface{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		fmt.Fprintf(&sb, "%v", m[k])
		sb.WriteByte(';')
	}
	return sb.String()
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func parsePort(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
