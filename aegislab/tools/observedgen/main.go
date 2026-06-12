// Command observedgen renders the trace-mined "observed surface" JSON
// (aegislab/tools/manifestgen/observed/<system>.json, produced by
// rcabench-platform/cli/chaos_point_mining) into aegis-chaos/v1beta
// PointManifests.
//
// It emits the families derivable from normal-phase traces — pod/cpu/mem/time,
// http (server-attributed), gRPC base, dns and network (from topology edges +
// infra deps), and jvm_mysql_* (from db client spans' db_name/table/sql_type).
// jvm_method_* / jvm_runtime_mutator need bytecode analysis and are NOT produced
// here; they stay sourced from manifestgen's existing data and are merged later.
//
// Output goes to a SEPARATE tree (default manifests/aegis-chaos-observed/) so
// the current catalog is never clobbered and the two can be diffed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	apiVersion   = "aegis-chaos/v1beta"
	kind         = "PointManifest"
	instance     = "observed"
	chartVersion = "observed-genesis"
	replaceScope = "service"
)

// systemDisplay mirrors manifestgen: the value used for metadata.system, the
// output folder, and the target namespace. Keyed by the observed-JSON system
// (derived from k8s namespace), which already equals the display name for
// every stack except otel-demo.
var systemDisplay = map[string]string{
	"ts": "ts", "otel-demo": "otel-demo", "media": "media", "hs": "hs",
	"sn": "sn", "ob": "ob", "sockshop": "sockshop", "teastore": "teastore",
}

var (
	httpRespCaps        = []string{"http_response_abort", "http_response_delay", "http_response_replace_code", "http_response_patch_body", "http_response_replace_body"}
	httpReqMutationCaps = []string{"http_request_replace_method", "http_request_replace_path"}
	httpReqCaps         = []string{"http_request_delay", "http_request_abort"}
	dnsCaps             = []string{"dns_error", "dns_random"}
	networkCaps         = []string{"network_delay", "network_loss", "network_duplicate", "network_corrupt", "network_bandwidth", "network_partition"}
	jvmMysqlCaps        = []string{"jvm_mysql_latency", "jvm_mysql_exception"}
)

var staticExt = map[string]bool{".css": true, ".js": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true, ".svg": true, ".woff": true, ".woff2": true, ".ttf": true, ".eot": true, ".map": true}

// containerName mirrors manifestgen: teastore is the one stack where the
// container name differs from the app label (strip the teastore- prefix).
func containerName(system, service string) string {
	if system == "teastore" {
		return strings.TrimPrefix(service, "teastore-")
	}
	return service
}

// isGRPCRoute reports whether a path is a gRPC pseudo-route (/pkg.Svc/Method),
// which HTTPChaos response/mutation caps cannot exercise.
func isGRPCRoute(p string) bool {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	return len(segs) == 2 && strings.Contains(segs[0], ".") && segs[1] != "" && !strings.Contains(segs[1], ".")
}

func validPort(s string) int {
	if p, err := strconv.Atoi(s); err == nil && p > 0 && p < 65536 {
		return p
	}
	return 0
}

func isStatic(p string) bool {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return staticExt[strings.ToLower(p[i:])]
	}
	return false
}

type observed struct {
	System   string         `json:"system"`
	Services []string       `json:"services"`
	HTTP     []httpEndpoint `json:"http_endpoints"`
	GRPC     []grpcOp       `json:"grpc_operations"`
	Edges    []edge         `json:"edges"`
	Infra    []infraDep     `json:"infra_deps"`
	DB       []dbOp         `json:"db_operations"`
}

type dbOp struct {
	Service  string `json:"service"`
	DBSystem string `json:"db_system"`
	DBName   string `json:"db_name"`
	Table    string `json:"table"`
	SQLType  string `json:"sql_type"`
}

// infraDep is a call to a leaf middleware (mysql/redis/rabbitmq) that emits no
// span of its own — the target is taken from server.address (db, a real
// workload name) or messaging.system (mq, the broker type, since the span only
// carries the broker IP).
type infraDep struct {
	Service string `json:"service"`
	Target  string `json:"target"`
	Kind    string `json:"kind"`
	System  string `json:"system"`
}

type httpEndpoint struct {
	Service   string   `json:"service"`
	Method    string   `json:"method"`
	Path      string   `json:"path"`
	Port      string   `json:"port"`
	SpanNames []string `json:"span_names"`
}
type grpcOp struct {
	Service    string `json:"service"`
	RPCService string `json:"rpc_service"`
	RPCMethod  string `json:"rpc_method"`
}
type edge struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	SpanNames []string `json:"span_names"`
}

type point struct {
	cap    string
	target map[string]any
}

func main() {
	in := flag.String("in", "aegislab/tools/manifestgen/observed", "dir of observed <system>.json")
	out := flag.String("out", "aegislab/manifests/aegis-chaos-observed", "output root")
	callerReq := flag.Bool("caller-request-points", false,
		"also emit caller-keyed http_request_* for each edge's downstream route "+
			"(bridge-mode / non-IPVLAN CNIs where egress HTTPChaos fires; inert on "+
			"byte-cluster's inbound-only bridgeless tproxy)")
	flag.Parse()

	entries, err := filepath.Glob(filepath.Join(*in, "*.json"))
	if err != nil || len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "no *.json under %s\n", *in)
		os.Exit(1)
	}
	for _, f := range entries {
		if err := renderSystem(f, *out, *callerReq); err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", f, err)
		}
	}
}

func renderSystem(path, outRoot string, callerReq bool) error {
	var o observed
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	ns, ok := systemDisplay[o.System]
	if !ok {
		return fmt.Errorf("unknown system %q", o.System)
	}

	// Group by the service that owns each point. http/grpc are keyed by the
	// hosting service; dns/network by the calling (source) service.
	httpBySvc := map[string][]httpEndpoint{}
	for _, e := range o.HTTP {
		httpBySvc[e.Service] = append(httpBySvc[e.Service], e)
	}
	grpcBySvc := map[string][]grpcOp{}
	for _, g := range o.GRPC {
		grpcBySvc[g.Service] = append(grpcBySvc[g.Service], g)
	}
	edgesBySrc := map[string][]edge{}
	svcSet := map[string]bool{}
	for _, s := range o.Services {
		svcSet[s] = true
	}
	for _, e := range o.Edges {
		edgesBySrc[e.Source] = append(edgesBySrc[e.Source], e)
		svcSet[e.Source] = true
		svcSet[e.Target] = true
	}
	// infra targets (mysql/rabbitmq/...) become network targets of the calling
	// service; they are leaves, so they do not own points themselves.
	infraBySrc := map[string][]infraDep{}
	for _, d := range o.Infra {
		infraBySrc[d.Service] = append(infraBySrc[d.Service], d)
		svcSet[d.Service] = true
	}
	dbBySvc := map[string][]dbOp{}
	for _, d := range o.DB {
		dbBySvc[d.Service] = append(dbBySvc[d.Service], d)
		svcSet[d.Service] = true
	}
	for s := range httpBySvc {
		svcSet[s] = true
	}
	for s := range grpcBySvc {
		svcSet[s] = true
	}

	sysDir := filepath.Join(outRoot, ns)
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		return err
	}
	services := make([]string, 0, len(svcSet))
	for s := range svcSet {
		services = append(services, s)
	}
	sort.Strings(services)

	for _, svc := range services {
		pts := buildService(o.System, ns, svc, httpBySvc[svc], grpcBySvc[svc], edgesBySrc[svc], infraBySrc[svc], dbBySvc[svc], callerReq)
		if len(pts) == 0 {
			continue
		}
		if err := writeManifest(filepath.Join(sysDir, svc+".yaml"), ns, svc, pts); err != nil {
			return err
		}
	}
	return nil
}

func buildService(system, ns, svc string, https []httpEndpoint, grpcs []grpcOp, edges []edge, infra []infraDep, dbs []dbOp, callerReq bool) []point {
	var pts []point
	add := func(cap string, t map[string]any) { pts = append(pts, point{cap, t}) }

	// workload-level (always)
	add("pod_kill", map[string]any{"namespace": ns, "app": svc})
	add("pod_failure", map[string]any{"namespace": ns, "app": svc})
	ctr := containerName(system, svc)
	for _, c := range []string{"container_kill", "cpu_stress", "time_skew"} {
		add(c, map[string]any{"namespace": ns, "app": svc, "container": ctr})
	}

	// http endpoints (server-attributed). The chaos service requires a port on
	// http targets, but some Server spans don't record server.port; backfill from
	// the service's dominant observed port (a service listens on one port), and
	// skip endpoints for a service that never recorded any port at all.
	dominantPort := 0
	{
		cnt := map[int]int{}
		for _, e := range https {
			if p := validPort(e.Port); p != 0 {
				cnt[p]++
			}
		}
		best := 0
		for p, c := range cnt {
			if c > best || (c == best && (dominantPort == 0 || p < dominantPort)) {
				best, dominantPort = c, p
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range https {
		if e.Path == "" || isStatic(e.Path) { // a pathless http span is not targetable
			continue
		}
		port := dominantPort
		if p := validPort(e.Port); p != 0 {
			port = p
		}
		if port == 0 { // no port observed anywhere for this service — not HTTPChaos-able
			continue
		}
		k := fmt.Sprintf("%s %s %d", e.Method, e.Path, port)
		if seen[k] {
			continue
		}
		seen[k] = true
		base := map[string]any{"namespace": ns, "app": svc, "method": e.Method, "path": e.Path, "port": port, "server_address": svc}
		if len(e.SpanNames) > 0 {
			base["span_name"] = e.SpanNames[0]
		}
		for _, c := range httpReqCaps {
			add(c, cloneTarget(base))
		}
		if !isGRPCRoute(e.Path) { // HTTPChaos response/mutation is HTTP/1.x only
			a1b := map[string]any{"namespace": ns, "app": svc, "method": e.Method, "path": e.Path, "port": port}
			for _, c := range append(append([]string{}, httpRespCaps...), httpReqMutationCaps...) {
				add(c, cloneTarget(a1b))
			}
		}
	}

	// gRPC server ops get NO http_request_* points: the chaos service requires a
	// port on http targets (HTTPChaosSpec.Port is non-optional) and gRPC ops carry
	// no stable listen port, so such points fail server schema validation; and
	// HTTPChaos on a gRPC/Thrift listener is inert anyway. These services are
	// covered by pod + network points instead.
	_ = grpcs

	// dns + network, keyed by this service as the caller (source)
	dnsDone, netDone := map[string]bool{}, map[string]bool{}
	for _, e := range edges {
		if e.Target == "" || e.Target == svc {
			continue
		}
		if !dnsDone[e.Target] {
			dnsDone[e.Target] = true
			for _, c := range dnsCaps {
				add(c, map[string]any{"namespace": ns, "app": svc, "domain_patterns": []string{e.Target}})
			}
		}
		netDone[e.Target] = true
		nt := map[string]any{"namespace": ns, "source_app": svc, "target_service": e.Target}
		if len(e.SpanNames) > 0 {
			nt["span_names"] = e.SpanNames
		}
		for _, c := range networkCaps {
			add(c, cloneTarget(nt))
		}
	}

	// infra dependencies (mysql/redis/rabbitmq): network only (no dns/pod — the
	// broker/db is a leaf with no span of its own).
	for _, d := range infra {
		if d.Target == "" || d.Target == svc || netDone[d.Target] {
			continue
		}
		netDone[d.Target] = true
		nt := map[string]any{"namespace": ns, "source_app": svc, "target_service": d.Target}
		for _, c := range networkCaps {
			add(c, cloneTarget(nt))
		}
	}

	// jvm_mysql_* from db client spans (db_name/table/sql_type). Gated on mysql,
	// matching manifestgen; the JDBC-intercepting agent only bites Java stacks,
	// which are the only ones with mysql db spans here.
	sqlSeen := map[string]bool{}
	for _, d := range dbs {
		if d.DBSystem != "mysql" || d.DBName == "" || d.Table == "" || d.SQLType == "" {
			continue
		}
		k := d.DBName + "|" + d.Table + "|" + d.SQLType
		if sqlSeen[k] {
			continue
		}
		sqlSeen[k] = true
		t := map[string]any{"namespace": ns, "app": svc, "db_name": d.DBName, "table": d.Table, "sql_type": d.SQLType}
		for _, c := range jvmMysqlCaps {
			add(c, cloneTarget(t))
		}
	}

	// caller-keyed http_request_* for each edge's downstream route (bridge-mode
	// egress semantics; off by default because byte-cluster's bridgeless tproxy
	// is inbound-only). Method/path come from the edge's observed operations.
	if callerReq {
		crSeen := map[string]bool{}
		for _, e := range edges {
			if e.Target == "" || e.Target == svc {
				continue
			}
			for _, sn := range e.SpanNames {
				m, p, ok := parseSpanOp(sn)
				if !ok || isStatic(p) {
					continue
				}
				k := m + " " + p
				if crSeen[k] {
					continue
				}
				crSeen[k] = true
				base := map[string]any{"namespace": ns, "app": svc, "method": m, "path": p, "server_address": e.Target}
				for _, c := range append(append([]string{}, httpReqCaps...), httpReqMutationCaps...) {
					add(c, cloneTarget(base))
				}
			}
		}
	}
	return pts
}

// parseSpanOp extracts METHOD and path from a normalized span name of the form
// "METHOD /path". gRPC/Thrift span names (no leading HTTP verb or no /path)
// return ok=false.
func parseSpanOp(sn string) (method, path string, ok bool) {
	parts := strings.SplitN(sn, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	m, p := parts[0], parts[1]
	if m == "" || strings.ToUpper(m) != m || !strings.HasPrefix(p, "/") {
		return "", "", false
	}
	return m, p, true
}

func cloneTarget(t map[string]any) map[string]any {
	c := make(map[string]any, len(t))
	for k, v := range t {
		c[k] = v
	}
	return c
}

func writeManifest(path, system, service string, points []point) error {
	var b strings.Builder
	b.WriteString("apiVersion: " + apiVersion + "\n")
	b.WriteString("kind: " + kind + "\n")
	b.WriteString("metadata:\n")
	b.WriteString("  chart_version: " + q(chartVersion) + "\n")
	b.WriteString("  instance: " + q(instance) + "\n")
	b.WriteString("  service: " + q(service) + "\n")
	b.WriteString("  system: " + q(system) + "\n")
	b.WriteString("spec:\n")
	b.WriteString("  replace_scope: " + q(replaceScope) + "\n")
	b.WriteString("  points:\n")
	for _, p := range points {
		b.WriteString("    - capability: " + q(p.cap) + "\n")
		b.WriteString("      target:\n")
		keys := make([]string, 0, len(p.target))
		for k := range p.target {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeField(&b, k, p.target[k])
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeField(b *strings.Builder, k string, v any) {
	switch val := v.(type) {
	case int:
		b.WriteString("        " + k + ": " + strconv.Itoa(val) + "\n")
	case []string:
		b.WriteString("        " + k + ":\n")
		for _, s := range val {
			b.WriteString("          - " + q(s) + "\n")
		}
	default:
		b.WriteString("        " + k + ": " + q(fmt.Sprint(val)) + "\n")
	}
}

func q(s string) string {
	out, _ := json.Marshal(s) // JSON string == YAML double-quoted scalar
	return string(out)
}
