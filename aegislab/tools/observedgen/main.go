// Command observedgen renders the trace-mined "observed surface" JSON
// (aegislab/tools/manifestgen/observed/<system>.json, produced by
// rcabench-platform/cli/chaos_point_mining) into aegis-chaos/v1beta
// PointManifests.
//
// It emits only the families that are derivable from normal-phase traces —
// pod/cpu/mem/time, http (server-attributed), gRPC base, dns and network
// (from topology edges). JVM / db-table families need bytecode analysis and
// are intentionally not produced here; they stay sourced from manifestgen's
// existing data and are merged later.
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

func isStatic(p string) bool {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return staticExt[strings.ToLower(p[i:])]
	}
	return false
}

type observed struct {
	System    string `json:"system"`
	Services  []string
	HTTP      []httpEndpoint `json:"http_endpoints"`
	GRPC      []grpcOp       `json:"grpc_operations"`
	Edges     []edge         `json:"edges"`
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
	flag.Parse()

	entries, err := filepath.Glob(filepath.Join(*in, "*.json"))
	if err != nil || len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "no *.json under %s\n", *in)
		os.Exit(1)
	}
	for _, f := range entries {
		if err := renderSystem(f, *out); err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", f, err)
		}
	}
}

func renderSystem(path, outRoot string) error {
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
		pts := buildService(o.System, ns, svc, httpBySvc[svc], grpcBySvc[svc], edgesBySrc[svc])
		if len(pts) == 0 {
			continue
		}
		if err := writeManifest(filepath.Join(sysDir, svc+".yaml"), ns, svc, pts); err != nil {
			return err
		}
	}
	return nil
}

func buildService(system, ns, svc string, https []httpEndpoint, grpcs []grpcOp, edges []edge) []point {
	var pts []point
	add := func(cap string, t map[string]any) { pts = append(pts, point{cap, t}) }

	// workload-level (always)
	add("pod_kill", map[string]any{"namespace": ns, "app": svc})
	add("pod_failure", map[string]any{"namespace": ns, "app": svc})
	ctr := containerName(system, svc)
	for _, c := range []string{"container_kill", "cpu_stress", "time_skew"} {
		add(c, map[string]any{"namespace": ns, "app": svc, "container": ctr})
	}

	// http endpoints (server-attributed)
	seen := map[string]bool{}
	for _, e := range https {
		if e.Path == "" || isStatic(e.Path) { // a pathless http span is not targetable
			continue
		}
		k := e.Method + " " + e.Path + " " + e.Port
		if seen[k] {
			continue
		}
		seen[k] = true
		base := map[string]any{"namespace": ns, "app": svc, "method": e.Method, "path": e.Path}
		if p, err := strconv.Atoi(e.Port); err == nil && p > 0 && p < 65536 {
			base["port"] = p
		}
		base["server_address"] = svc
		if len(e.SpanNames) > 0 {
			base["span_name"] = e.SpanNames[0]
		}
		for _, c := range httpReqCaps {
			add(c, cloneTarget(base))
		}
		if !isGRPCRoute(e.Path) { // HTTPChaos response/mutation is HTTP/1.x only
			a1b := map[string]any{"namespace": ns, "app": svc, "method": e.Method, "path": e.Path}
			if v, ok := base["port"]; ok {
				a1b["port"] = v
			}
			for _, c := range append(append([]string{}, httpRespCaps...), httpReqMutationCaps...) {
				add(c, cloneTarget(a1b))
			}
		}
	}

	// gRPC server ops: base http_request_* only (pseudo-route path)
	gseen := map[string]bool{}
	for _, g := range grpcs {
		if g.RPCService == "" || g.RPCMethod == "" {
			continue
		}
		path := "/" + g.RPCService + "/" + g.RPCMethod
		if gseen[path] {
			continue
		}
		gseen[path] = true
		base := map[string]any{"namespace": ns, "app": svc, "method": "POST", "path": path, "server_address": svc}
		for _, c := range httpReqCaps {
			add(c, cloneTarget(base))
		}
	}

	// dns + network, keyed by this service as the caller (source)
	dnsDone := map[string]bool{}
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
		nt := map[string]any{"namespace": ns, "source_app": svc, "target_service": e.Target}
		if len(e.SpanNames) > 0 {
			nt["span_names"] = e.SpanNames
		}
		for _, c := range networkCaps {
			add(c, cloneTarget(nt))
		}
	}
	return pts
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
