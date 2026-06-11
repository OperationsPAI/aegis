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

var (
	uuidSegment    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	tripIDSegment  = regexp.MustCompile(`^[A-Z]{1,3}\d+$`)
	numericSegment = regexp.MustCompile(`^\d+$`)
)

// normalizeSpanName collapses high-cardinality segments of a span name's path
// into named placeholders ({uuid}/{tripid}/{id}) so distinct concrete spans
// fold to one groundtruth template. A span name is "METHOD /path[ suffix]";
// only the slash-delimited segments of the path part are rewritten — the
// method prefix and any non-path tokens are preserved verbatim.
func normalizeSpanName(s string) string {
	fields := strings.SplitN(s, " ", 2)
	if len(fields) != 2 {
		return s
	}
	method, rest := fields[0], fields[1]
	segs := strings.Split(rest, "/")
	for i, seg := range segs {
		switch {
		case uuidSegment.MatchString(seg):
			segs[i] = "{uuid}"
		case tripIDSegment.MatchString(seg):
			segs[i] = "{tripid}"
		case numericSegment.MatchString(seg):
			segs[i] = "{id}"
		}
	}
	return method + " " + strings.Join(segs, "/")
}

var staticResourceExts = map[string]struct{}{
	".css": {}, ".js": {}, ".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {},
	".ico": {}, ".svg": {}, ".woff": {}, ".woff2": {}, ".ttf": {}, ".eot": {}, ".map": {},
}

var staticResourcePrefixes = []string{"/assets/", "/css/", "/js/", "/img/", "/fonts/", "/static/"}

// isStaticResource reports whether a route serves a static asset rather than a
// service endpoint. Chaos on these is noise — they bypass application logic.
func isStaticResource(route string) bool {
	if i := strings.LastIndex(route, "/"); i >= 0 {
		last := route[i:]
		if dot := strings.LastIndex(last, "."); dot >= 0 {
			if _, ok := staticResourceExts[strings.ToLower(last[dot:])]; ok {
				return true
			}
		}
	}
	for _, p := range staticResourcePrefixes {
		if strings.HasPrefix(route, p) {
			return true
		}
	}
	return false
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

// clusterAppLabels holds the real pod app-label VALUES (the value under the
// system's selector key — `app` for everything but otel-demo's
// `app.kubernetes.io/name`) observed in a representative namespace per system
// at manifest-generation time. It is the resolution target for callee-only
// ServerAddresses: a service that appears only as a call target (never a
// ServiceName) gets pod/cpu/mem points only if its ServerAddress fuzzy-resolves
// to one of these real label values, so PodFailure actually selects a workload.
//
// Snapshot source (byte-cluster, 2026-06):
//
//	kubectl -n <ns> get pods -o jsonpath='{.items[*].metadata.labels.<key>}'
//
// Systems with no live namespace at snapshot time (hs, oteldemo, ts) carry no
// entry: their callee ServerAddresses are registered as-is (the chaos-data
// names are already k8s-native for these stacks) and logged as cluster-unverified.
var clusterAppLabels = map[string][]string{
	"sn": {
		"compose-post-service", "home-timeline-redis", "home-timeline-service",
		"load-generator", "media-frontend", "media-memcached", "media-mongodb",
		"media-service", "nginx-thrift", "post-storage-memcached",
		"post-storage-mongodb", "post-storage-service", "social-graph-mongodb",
		"social-graph-redis", "social-graph-service", "text-service",
		"unique-id-service", "url-shorten-memcached", "url-shorten-mongodb",
		"url-shorten-service", "user-memcached", "user-mention-service",
		"user-mongodb", "user-service", "user-timeline-mongodb",
		"user-timeline-redis", "user-timeline-service",
	},
	"media": {
		"cast-info-memcached", "cast-info-mongodb", "cast-info-service",
		"compose-review-memcached", "compose-review-service", "movie-id-memcached",
		"movie-id-mongodb", "movie-id-service", "movie-info-memcached",
		"movie-info-mongodb", "movie-info-service", "movie-review-mongodb",
		"movie-review-redis", "movie-review-service", "nginx-web-server",
		"page-service", "plot-memcached", "plot-mongodb", "plot-service",
		"rating-redis", "rating-service", "review-storage-memcached",
		"review-storage-mongodb", "review-storage-service", "text-service",
		"unique-id-service", "user-memcached", "user-mongodb",
		"user-review-mongodb", "user-review-redis", "user-review-service",
		"user-service",
	},
	"ob": {
		"adservice", "cartservice", "checkoutservice", "currencyservice",
		"emailservice", "frontend", "paymentservice", "productcatalogservice",
		"recommendationservice", "redis-cart", "shippingservice",
	},
	"sockshop": {
		"carts", "catalog", "front-end", "orders", "payment", "shipping", "users",
	},
	"teastore": {
		"teastore-auth", "teastore-db", "teastore-image", "teastore-persistence",
		"teastore-recommender", "teastore-registry", "teastore-webui",
	},
}

// httpInboundSystems marks stacks whose service-to-service calls are real
// HTTP/1.x, so a callee should also get an inbound http_request_delay/abort
// point (the HTTPChaos that actually bites is inbound on the callee). DSB
// stacks (hs/sn/media) front HTTP at nginx but the backends speak Thrift/gRPC,
// where an inbound HTTPChaos point dispatches and silently no-ops — excluded.
var httpInboundSystems = map[string]bool{
	"sockshop": true,
	"teastore": true,
}

// appLabel maps a chaos-experiment service-data key to the pod app-label
// VALUE used in chaos selectors. The data key already equals the pod app
// label for every system (teastore's pods carry app=teastore-webui under
// app_label_key=app, matching the trace-derived data key), so this is the
// identity. Do NOT strip the teastore- prefix here: the selector key is
// `app`, whose value is the full teastore-webui; the short form (webui)
// only exists under app.kubernetes.io/name, which is otel-demo's key.
func appLabel(sysKey, service string) string {
	return service
}

// containerName maps a service-data key to the pod's container[0] name.
// teastore is the one stack where app-label != container name: pods carry
// app=teastore-webui but the container is named webui, so container-scoped
// capabilities (container_kill / cpu_stress / time_skew)
// must strip the teastore- prefix. Every other system follows the
// app==container convention.
func containerName(sysKey, service string) string {
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
	jvmMethodCaps = []string{"jvm_method_return", "jvm_method_exception", "jvm_cpu_stress"}
	jvmMysqlCaps  = []string{"jvm_mysql_latency", "jvm_mysql_exception"}

	mutationTypeNames = map[int]string{0: "constant", 1: "operator", 2: "string"}

	validMySQLTypes = map[string]bool{"select": true, "insert": true, "update": true, "delete": true, "replace": true, "all": true}
)

type httpEndpoint struct {
	Method        string
	Route         string
	Port          string
	ServerAddress string
	SpanName      string
	// grpc marks an endpoint as gRPC — either folded from grpcoperations or
	// declared Grpc:true in serviceendpoints. Such endpoints are excluded from
	// every http_request_* family: chaos-mesh HTTPChaos only matches HTTP/1.x,
	// so points on them dispatch but silently no-op.
	grpc bool
}

// isGRPCRoute reports whether an endpoint is gRPC and thus unexercisable by
// HTTPChaos. Protocol must be declared explicitly — the dotted-route shape is
// ambiguous (teastore's REST servlets share it with real gRPC), so route shape
// is only a lint signal (see warnUnmarkedGRPCShape), never an exclusion.
func isGRPCRoute(e httpEndpoint) bool {
	return e.grpc
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

type mutationSpec struct {
	Type        int
	TypeName    string
	From        string
	To          string
	Strategy    string
	Description string
}

type mutatorEntry struct {
	Class     string
	Method    string
	Mutations []mutationSpec
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
	// service → runtime-mutator method entries
	mutators map[string][]mutatorEntry
	// client-side gRPC operations across all services (DNS gRPC-only filter)
	grpcClientOps []grpcOp
	// services that host a gRPC SERVER (a self-addressed SpanKind=Server row in
	// grpcoperations). Used by servesHTTP to suppress HTTPChaos points on a
	// gRPC-only server whose only http rows are client-side calls it makes.
	grpcServers map[string]struct{}
	// union of every service name observed anywhere in this system
	services map[string]struct{}
	// presentFamilies records which capability-family source files existed
	// at load time. Empty data after a present file means the source was
	// truncated or the AST var name drifted — the run should fail loudly
	// rather than emit silent zero-point manifests.
	presentFamilies map[string]bool
	// selfServedHTTP caches, per service, the set of normalized http routes the
	// service serves INBOUND (a non-gRPC row keyed under itself whose
	// ServerAddress is empty or itself — the server-side shape). Populated lazily
	// by serverServesPath, which uses it to suppress redundant caller-edge points.
	selfServedHTTP map[string]map[string]struct{}
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

	httpReqBySystem    map[string]int
	httpA1bBySystem    map[string]int
	dnsBySystem        map[string]int
	networkBySystem    map[string]int
	jvmMethodBySystem  map[string]int
	jvmMysqlBySystem   map[string]int
	jvmMutatorBySystem map[string]int
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
		servicesBySystem:   map[string]int{},
		pointsBySystem:     map[string]int{},
		httpReqBySystem:    map[string]int{},
		httpA1bBySystem:    map[string]int{},
		dnsBySystem:        map[string]int{},
		networkBySystem:    map[string]int{},
		jvmMethodBySystem:  map[string]int{},
		jvmMysqlBySystem:   map[string]int{},
		jvmMutatorBySystem: map[string]int{},
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
		warnUnmarkedGRPCShape(sysKey, data)
		registerCalleeServices(sysKey, data)

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
				"http":                buildHTTPA1bPoints(svc, data),
				"dns":                 buildDNSPoints(svc, data),
				"network":             buildNetworkPoints(svc, data),
				"jvm-method":          buildJVMMethodPoints(svc, data),
				"jvm-mysql":           buildJVMMysqlPoints(svc, data),
				"jvm-runtime-mutator": buildRuntimeMutatorPoints(svc, data),
			}
			for _, cat := range []string{"dns", "http", "jvm-method", "jvm-mysql", "jvm-runtime-mutator", "network"} {
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
				case "jvm-runtime-mutator":
					st.jvmMutatorBySystem[sysKey] += len(pts)
				}
			}
		}
		st.systems++

		// Sanity floor: a present family file must yield non-zero points.
		// Mismatch means the source was truncated, the AST var name drifted,
		// or the loader silently swallowed a parse error. An all-gRPC system
		// (every loaded route is a gRPC pseudo-route) legitimately yields zero
		// http_request_* points — HTTPChaos never matches it — so the floor
		// only fires when at least one http-eligible endpoint was loaded.
		var mismatches []string
		if data.presentFamilies["serviceendpoints"] && hasHTTPEligibleEndpoint(data) && st.httpReqBySystem[sysKey] == 0 {
			mismatches = append(mismatches, "http (serviceendpoints file present with http endpoints, 0 http_request_* points)")
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

// warnUnmarkedGRPCShape flags dotted /pkg.Service/Method routes that are
// treated as HTTP because nothing declared them gRPC. teastore's REST servlets
// legitimately share this shape, so it is a warning (catch protocol drift in
// new data), not an exclusion.
func warnUnmarkedGRPCShape(sysKey string, d *systemData) {
	seen := map[string]struct{}{}
	for svc, eps := range d.endpoints {
		for _, e := range eps {
			if e.grpc || !grpcRoutePattern.MatchString(e.Route) {
				continue
			}
			key := svc + " " + e.Route
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			fmt.Fprintf(os.Stderr, "warn: %s/%s route %q has gRPC shape but is not marked Grpc — confirm protocol (treating as HTTP)\n", sysKey, svc, e.Route)
		}
	}
}

// registerCalleeServices brings callee-only services — those that appear only
// as a call target (e.distinct ServerAddress) and never as a ServiceName key —
// into d.services so buildBasePoints emits pod/cpu/mem/time points for them.
// Without this they get network points only, and a PodFailure inject against
// them is a catalog miss (404). Each callee ServerAddress is fuzzy-resolved to
// a real pod app-label value (clusterAppLabels); if it resolves to a name that
// differs from the alias, every endpoint addressed to the alias is rewritten to
// the resolved name so its points (network/dns/http-inbound) target the real
// label too. ServerAddresses with no real-workload match are dropped and logged.
//
// For HTTP-native stacks (httpInboundSystems) the callee additionally inherits
// an inbound endpoint per non-gRPC call edge, so it gains http_request_delay/abort
// (the HTTPChaos that actually bites is inbound on the callee).
func registerCalleeServices(sysKey string, d *systemData) {
	realLabels, haveCluster := clusterAppLabels[sysKey]
	resolved := map[string]string{} // alias -> real label (or alias if no cluster data)
	var registered, remapped, dropped []string

	for _, svc := range sortedKeys(d.services) {
		for _, e := range d.endpoints[svc] {
			addr := e.ServerAddress
			if addr == "" || addr == svc {
				continue
			}
			if _, ok := d.services[addr]; ok {
				continue // already a first-class service
			}
			if _, done := resolved[addr]; done {
				continue
			}
			if !haveCluster {
				resolved[addr] = addr
				registered = append(registered, addr+" (cluster-unverified)")
				continue
			}
			real, ok := fuzzyResolveLabel(sysKey, addr, realLabels)
			if !ok {
				resolved[addr] = ""
				dropped = append(dropped, addr)
				continue
			}
			resolved[addr] = real
			if real == addr {
				registered = append(registered, addr)
			} else {
				registered = append(registered, real)
				remapped = append(remapped, fmt.Sprintf("%s->%s", addr, real))
			}
		}
	}

	// Apply resolution: register real names, rewrite endpoint ServerAddresses to
	// the resolved name, and inherit http-inbound endpoints onto HTTP callees.
	httpInbound := httpInboundSystems[sysKey]
	for _, svc := range sortedKeys(d.services) {
		for _, e := range d.endpoints[svc] {
			real, ok := resolved[e.ServerAddress]
			if !ok || real == "" {
				continue
			}
			d.services[real] = struct{}{}
			if httpInbound && !e.grpc {
				d.endpoints[real] = append(d.endpoints[real], httpEndpoint{
					Method:   e.Method,
					Route:    e.Route,
					Port:     e.Port,
					SpanName: e.SpanName,
				})
			}
		}
	}
	if httpInbound {
		dedupHTTP(d)
	}

	if len(registered) > 0 {
		fmt.Fprintf(os.Stderr, "info: %s registered %d callee-only services: %s\n",
			sysKey, len(registered), strings.Join(dedupStrings(registered), ", "))
	}
	if len(remapped) > 0 {
		fmt.Fprintf(os.Stderr, "info: %s fuzzy-remapped ServerAddresses: %s\n",
			sysKey, strings.Join(dedupStrings(remapped), ", "))
	}
	if len(dropped) > 0 {
		fmt.Fprintf(os.Stderr, "warn: %s dropped %d ServerAddresses with no real-workload match: %s\n",
			sysKey, len(dropped), strings.Join(dedupStrings(dropped), ", "))
	}
}

// fuzzyResolveLabel matches a ServerAddress alias to a real pod app-label value.
// Resolution order: exact → suffix-stripped (-service/-svc/-deployment) on both
// sides → system-prefix-stripped → case-insensitive → substring → shortest
// containing label. Returns the real label and true on a hit.
func fuzzyResolveLabel(sysKey, addr string, realLabels []string) (string, bool) {
	labelSet := map[string]struct{}{}
	for _, l := range realLabels {
		labelSet[l] = struct{}{}
	}
	if _, ok := labelSet[addr]; ok {
		return addr, true
	}

	canon := func(s string) string {
		s = strings.ToLower(s)
		for _, suf := range []string{"-service", "-svc", "-deployment"} {
			s = strings.TrimSuffix(s, suf)
		}
		s = strings.TrimPrefix(s, sysKey+"-")
		return s
	}
	target := canon(addr)
	// Prefer the shortest real label per canonical form for determinism.
	canonToReal := map[string]string{}
	for _, l := range sortByLen(realLabels) {
		if _, seen := canonToReal[canon(l)]; !seen {
			canonToReal[canon(l)] = l
		}
	}
	if real, ok := canonToReal[target]; ok {
		return real, true
	}

	// Substring: shortest real label whose canonical form contains, or is
	// contained by, the target's canonical form.
	for _, l := range sortByLen(realLabels) {
		cl := canon(l)
		if strings.Contains(cl, target) || strings.Contains(target, cl) {
			return l, true
		}
	}
	return "", false
}

// sortByLen returns the labels sorted by (length, lexicographic) so substring
// resolution deterministically prefers the closest (shortest) match.
func sortByLen(in []string) []string {
	out := append([]string(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

func dedupStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// hasHTTPEligibleEndpoint reports whether any service would emit an
// http_request_* point — a real HTTP/1.x route (not a gRPC pseudo-route) under
// a service that actually serves http. Used by the capability-family floor.
func hasHTTPEligibleEndpoint(d *systemData) bool {
	for svc, eps := range d.endpoints {
		if !d.servesHTTP(svc) {
			continue
		}
		for _, e := range eps {
			if !isGRPCRoute(e) {
				return true
			}
		}
	}
	return false
}

// servesHTTP reports whether a service's selected pod actually serves inbound
// http, so HTTPChaos (which only perturbs the pod's inbound http) can bite it.
//
// serviceendpoints rows are caller-keyed CLIENT edges with no SpanKind, so a
// pure gRPC server that merely makes http client calls (otel-demo's checkout:
// gRPC server, but its rows are all outbound http to shipping/email/...) would
// otherwise get http points on a pod that serves no http — a silent no-op that
// the detector reads as no_anomaly. clickhouseanalyzer also emits spurious
// reverse edges (shipping->checkout) that make checkout look like an http
// callee, so callee/ServerAddress evidence alone can't clear it.
//
// The reliable disambiguator is the gRPC SpanKind=Server data: a service is
// suppressed only when it hosts a gRPC server AND has no self-served http route
// (a non-grpc row keyed under itself whose ServerAddress is empty or itself —
// the server-side shape). Every non-gRPC-server (shipping/quote/email/frontend,
// all DSB Thrift backends, every REST service) is unaffected.
func (d *systemData) servesHTTP(service string) bool {
	if _, isGRPCServer := d.grpcServers[service]; !isGRPCServer {
		return true
	}
	for _, e := range d.endpoints[service] {
		if isGRPCRoute(e) {
			continue
		}
		if e.ServerAddress == "" || e.ServerAddress == service {
			return true
		}
	}
	return false
}

// serverServesPath reports whether `service` itself serves `normPath` inbound —
// i.e. it has a non-gRPC row keyed under itself whose ServerAddress is empty or
// itself (the server-side shape), whose normalized route equals normPath.
//
// This is the precise complement of a caller-edge: serviceendpoints rows are a
// mix of client edges (keyed=caller, ServerAddress=callee, e.g. shipping
// /getquote -> quote) and server edges (keyed=server, ServerAddress=upstream
// caller, e.g. shipping /get-quote <- load-generator). A caller-edge HTTPChaos
// point selects the CALLER's pod, which has no inbound route for that path, so
// the chaos CR parks Pending and is force-failed. suppressCallerEdge uses this
// to drop such a point only when the callee provably self-serves the route — so
// a real server-side point (callee==app, or callee has no self-served row) is
// never dropped.
func (d *systemData) serverServesPath(service, normPath string) bool {
	if d.selfServedHTTP == nil {
		d.selfServedHTTP = map[string]map[string]struct{}{}
		for svc, eps := range d.endpoints {
			for _, e := range eps {
				if isGRPCRoute(e) {
					continue
				}
				if e.ServerAddress != "" && e.ServerAddress != svc {
					continue
				}
				target, ok := httpTarget(d.namespace, svc, e)
				if !ok {
					continue
				}
				p, _ := target["path"].(string)
				if d.selfServedHTTP[svc] == nil {
					d.selfServedHTTP[svc] = map[string]struct{}{}
				}
				d.selfServedHTTP[svc][p] = struct{}{}
			}
		}
	}
	_, ok := d.selfServedHTTP[service][normPath]
	return ok
}

// suppressCallerEdge reports whether an http target is a redundant caller-edge:
// a cross-service edge whose selected pod (app) is the CALLER of the route
// (server_address set and != app) while the callee (server_address) itself
// serves that exact route. The callee's own server-side point already covers
// the route, and the caller's pod never receives it inbound, so the point would
// only ever park Pending and force-fail (cr_never_injected). A self-served
// point (server_address empty or == app) or a callee that does not serve the
// route is left untouched.
func (d *systemData) suppressCallerEdge(target map[string]any) bool {
	app, _ := target["app"].(string)
	callee, _ := target["server_address"].(string)
	if callee == "" || callee == app {
		return false
	}
	path, _ := target["path"].(string)
	return d.serverServesPath(callee, path)
}

// buildBasePoints emits the workload-agnostic baseline plus the canonical
// http_request_delay/abort pair and jvm_method_latency. http request points
// are emitted only for HTTP/1.x routes; gRPC pseudo-routes are skipped since
// HTTPChaos cannot exercise them. The pod/cpu/mem/time and jvm-method points
// are protocol-agnostic and emitted for every service.
func buildBasePoints(service string, d *systemData, st *stats, sysKey string) []point {
	ns := d.namespace
	// chaos-experiment data carries no container name; most charts follow
	// pod.label.app == container[0].name, but teastore deviates (app
	// teastore-webui, container webui) so the container name is derived
	// separately via containerName.
	app := appLabel(d.sysKey, service)
	appOnly := map[string]any{"namespace": ns, "app": app}
	withContainer := map[string]any{"namespace": ns, "app": app, "container": containerName(d.sysKey, service)}
	pts := []point{
		{Capability: "container_kill", Target: cloneTarget(withContainer)},
		{Capability: "cpu_stress", Target: cloneTarget(withContainer)},
		{Capability: "pod_failure", Target: cloneTarget(appOnly)},
		{Capability: "pod_kill", Target: cloneTarget(appOnly)},
		{Capability: "time_skew", Target: cloneTarget(withContainer)},
	}

	httpSeen := map[string]struct{}{}
	servesHTTP := d.servesHTTP(service)
	for _, e := range d.endpoints[service] {
		if !servesHTTP || isGRPCRoute(e) {
			continue
		}
		target, ok := httpTarget(ns, app, e)
		if !ok || d.suppressCallerEdge(target) {
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
	if !d.servesHTTP(service) {
		return nil
	}
	app := appLabel(d.sysKey, service)
	var pts []point
	seen := map[string]struct{}{}
	for _, e := range d.endpoints[service] {
		if isGRPCRoute(e) {
			continue
		}
		target, ok := httpTarget(d.namespace, app, e)
		if !ok || d.suppressCallerEdge(target) {
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
	rawPath := strings.TrimSpace(e.Route)
	if isStaticResource(rawPath) {
		return nil, false
	}
	path := normalizePath(rawPath)
	if path == "" {
		return nil, false
	}
	t := map[string]any{
		"namespace": ns,
		"app":       service,
		"port":      port,
		"method":    method,
		"path":      path,
	}
	if e.ServerAddress != "" {
		t["server_address"] = e.ServerAddress
	}
	if sn := strings.TrimSpace(e.SpanName); sn != "" {
		t["span_name"] = normalizeSpanName(sn)
	}
	return t, true
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
	spansByTarget := map[string]map[string]struct{}{}
	for _, e := range d.endpoints[service] {
		if e.ServerAddress == "" || e.ServerAddress == service {
			continue
		}
		target := appLabel(d.sysKey, e.ServerAddress)
		if spansByTarget[target] == nil {
			spansByTarget[target] = map[string]struct{}{}
		}
		if sn := strings.TrimSpace(e.SpanName); sn != "" {
			spansByTarget[target][normalizeSpanName(sn)] = struct{}{}
		}
	}
	app := appLabel(d.sysKey, service)
	var pts []point
	for _, target := range sortedKeys(spansByTarget) {
		spanNames := sortedKeys(spansByTarget[target])
		for _, cap := range networkCaps {
			tgt := map[string]any{
				"namespace":      d.namespace,
				"source_app":     app,
				"target_service": target,
			}
			if len(spanNames) > 0 {
				arr := make([]any, len(spanNames))
				for i, s := range spanNames {
					arr[i] = s
				}
				tgt["span_names"] = arr
			}
			pts = append(pts, point{Capability: cap, Target: tgt})
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

// buildRuntimeMutatorPoints emits one jvm_runtime_mutator point per
// (app, class, method, distinct mutation). The mutation fingerprint mirrors
// guided/resolver.go runtimeMutatorKey: constant mutations are identified by
// from:to, operator/string mutations by type_name:strategy.
func buildRuntimeMutatorPoints(service string, d *systemData) []point {
	app := appLabel(d.sysKey, service)
	var pts []point
	for _, me := range d.mutators[service] {
		seen := map[string]struct{}{}
		for _, m := range me.Mutations {
			typeName := m.TypeName
			if typeName == "" {
				typeName = mutationTypeNames[m.Type]
			}
			if typeName == "" {
				continue
			}
			var fp string
			if typeName == "constant" {
				fp = "constant:" + m.From + ":" + m.To
			} else {
				fp = typeName + ":" + m.Strategy
			}
			key := me.Class + "|" + me.Method + "|" + fp
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			target := map[string]any{
				"namespace":          d.namespace,
				"app":                app,
				"class":              me.Class,
				"method":             me.Method,
				"mutation_type":      m.Type,
				"mutation_type_name": typeName,
			}
			if m.From != "" {
				target["mutation_from"] = m.From
			}
			if m.To != "" {
				target["mutation_to"] = m.To
			}
			if m.Strategy != "" {
				target["mutation_strategy"] = m.Strategy
			}
			if m.Description != "" {
				target["description"] = m.Description
			}
			pts = append(pts, point{Capability: "jvm_runtime_mutator", Target: target})
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
		fmt.Fprintf(w, "system=%-10s services=%-4d points=%-6d (httpReq=%d httpA1b=%d dns=%d net=%d jvmM=%d jvmSQL=%d mut=%d)\n",
			systemDisplay[k], st.servicesBySystem[k], st.pointsBySystem[k],
			st.httpReqBySystem[k], st.httpA1bBySystem[k], st.dnsBySystem[k],
			st.networkBySystem[k], st.jvmMethodBySystem[k], st.jvmMysqlBySystem[k], st.jvmMutatorBySystem[k])
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
		mutators:        map[string][]mutatorEntry{},
		grpcServers:     map[string]struct{}{},
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
	if err := loadMutatorConfig(filepath.Join(root, "mutatorconfig", "mutatorconfig.go"), d); err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, ErrVarNotFound) {
			return nil, fmt.Errorf("mutatorconfig: %w", err)
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
		// Order so that within one normalized (method, path, port) group the
		// richest span_name sorts first — http point dedup keeps the first
		// endpoint per group, and a templated span like
		// "DELETE /adminorder/{uuid}/{tripid}" is a better groundtruth label
		// than a bare "DELETE". Group by normalized path first so collapsing
		// endpoints land adjacent regardless of their raw (pre-normalization)
		// route.
		sort.SliceStable(out, func(i, j int) bool {
			ni, nj := normalizePath(out[i].Route), normalizePath(out[j].Route)
			if ni != nj {
				return ni < nj
			}
			if out[i].Method != out[j].Method {
				return out[i].Method < out[j].Method
			}
			if out[i].Port != out[j].Port {
				return out[i].Port < out[j].Port
			}
			si, sj := normalizeSpanName(out[i].SpanName), normalizeSpanName(out[j].SpanName)
			if len(si) != len(sj) {
				return len(si) > len(sj)
			}
			return si < sj
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
				grpc:          fields["Grpc"] == "true",
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
			// A self-addressed Server span marks svc as a gRPC server. Used by
			// servesHTTP to drop HTTPChaos points on gRPC-only servers.
			if strings.EqualFold(fields["SpanKind"], "server") &&
				(fields["ServerAddress"] == "" || fields["ServerAddress"] == svc) {
				d.grpcServers[svc] = struct{}{}
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

func loadMutatorConfig(path string, d *systemData) error {
	cl, err := parseMapLiteral(path, "ServiceMutatorConfig")
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
			me := mutatorEntry{
				Class:  structFields(rec)["ClassName"],
				Method: structFields(rec)["MethodName"],
			}
			if me.Class == "" || me.Method == "" {
				continue
			}
			for _, f := range rec.Elts {
				fkv, ok := f.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				id, ok := fkv.Key.(*ast.Ident)
				if !ok || id.Name != "Mutations" {
					continue
				}
				specs, ok := fkv.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, s := range specs.Elts {
					srec, ok := s.(*ast.CompositeLit)
					if !ok {
						continue
					}
					fields := structFields(srec)
					me.Mutations = append(me.Mutations, mutationSpec{
						Type:        intField(srec, "Type"),
						TypeName:    fields["TypeName"],
						From:        fields["From"],
						To:          fields["To"],
						Strategy:    fields["Strategy"],
						Description: fields["Description"],
					})
				}
			}
			d.mutators[svc] = append(d.mutators[svc], me)
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
		} else if lit, ok := kv.Value.(*ast.Ident); ok {
			out[id.Name] = lit.Name
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

func intField(cl *ast.CompositeLit, name string) int {
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok || id.Name != name {
			continue
		}
		bl, ok := kv.Value.(*ast.BasicLit)
		if !ok || bl.Kind != token.INT {
			return 0
		}
		n, err := strconv.Atoi(bl.Value)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
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
