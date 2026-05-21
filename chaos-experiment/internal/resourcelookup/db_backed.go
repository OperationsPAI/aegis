// Phase A4: a DB-backed metadata reader for chaos_points.
//
// This file provides the integration seam used by aegislab/src/crud/chaos to
// route systemCache lookups against the chaos_points table instead of the
// in-memory `internal/<sys>/*` providers. Once registered via
// SetChaosPointStore, every GetSystemCache(...).GetAllX() call drains
// chaos_points; the static providers remain wired only as a fallback for
// chaos-experiment's own *_test.go (whose data is the in-memory maps).
//
// We intentionally do not import gorm here — aegislab adapts its *gorm.DB
// to the small ChaosPointStore interface below, keeping chaos-experiment
// free of database dependencies until Phase B (git mv into aegislab).
package resourcelookup

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// ChaosPointRow is one row from the chaos_points table.
//
// SystemName / CapabilityName mirror the SQL columns; Target is the JSON
// blob (e.g. {"app":"checkout","method":"POST","path":"/get-quote","port":8080}).
type ChaosPointRow struct {
	SystemName     string
	CapabilityName string
	Target         map[string]any
}

// ChaosPointStore is the minimal contract the resourcelookup package needs
// to source metadata from chaos_points. aegislab implements this against
// *gorm.DB.
type ChaosPointStore interface {
	// QueryPoints returns every active chaos_point for the given system.
	// Implementations should filter on status='active'.
	QueryPoints(ctx context.Context, system string) ([]ChaosPointRow, error)
}

var (
	chaosPointStoreMu sync.RWMutex
	chaosPointStore   ChaosPointStore
)

// SetChaosPointStore installs a process-wide ChaosPointStore. When set, every
// systemCache GetAllX() routes through it instead of the static
// internal/<sys>/* providers. Passing nil restores the static path.
func SetChaosPointStore(store ChaosPointStore) {
	chaosPointStoreMu.Lock()
	defer chaosPointStoreMu.Unlock()
	chaosPointStore = store
}

func getChaosPointStore() ChaosPointStore {
	chaosPointStoreMu.RLock()
	defer chaosPointStoreMu.RUnlock()
	return chaosPointStore
}

// capability → family mapping.
//
// http_*       → service endpoints (target: app/method/path/port)
// network_*    → network pairs     (target: source_app/target_service)
// dns_*        → dns endpoints     (target: app/domain_patterns[])
// jvm_mysql_*  → database ops      (target: app/db_name/table/sql_type)
// jvm_*        → java class/method (target: app/class/method)         [exclude jvm_mysql_*]
func familyOf(capability string) string {
	switch {
	case strings.HasPrefix(capability, "http_"):
		return "http"
	case strings.HasPrefix(capability, "network_"):
		return "network"
	case strings.HasPrefix(capability, "dns_"):
		return "dns"
	case strings.HasPrefix(capability, "jvm_mysql_"):
		return "db"
	case strings.HasPrefix(capability, "jvm_"):
		return "jvm"
	case strings.HasPrefix(capability, "grpc_"):
		return "grpc"
	default:
		return ""
	}
}

func asString(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, ok := m[k]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers come back as float64; render port-like values without decimals.
		if t == float64(int64(t)) {
			return formatInt(int64(t))
		}
	}
	return ""
}

func asStringSlice(m map[string]any, k string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[k]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func formatInt(n int64) string {
	// strconv.FormatInt without importing strconv (keep imports minimal).
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// dbMetadataReader satisfies metadataReader by snapshotting chaos_points once
// per call. We don't cache here — the wrapping systemCache memoises results,
// and chaos_points imports are expected to be infrequent relative to reads.
type dbMetadataReader struct {
	store  ChaosPointStore
	system string
}

func (r *dbMetadataReader) snapshot(ctx context.Context) ([]ChaosPointRow, error) {
	rows, err := r.store.QueryPoints(ctx, r.system)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *dbMetadataReader) httpEndpoints(ctx context.Context) ([]AppEndpointPair, error) {
	rows, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	// chaos_points stores one row per (capability, target). Multiple
	// http_* capabilities for the same (app, path, method, port) collapse
	// to a single AppEndpointPair so HTTP enumeration matches the
	// in-memory contract (one endpoint per route).
	seen := make(map[string]struct{})
	out := make([]AppEndpointPair, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "http" {
			continue
		}
		ep := AppEndpointPair{
			AppName:    asString(row.Target, "app"),
			Route:      asString(row.Target, "path"),
			Method:     asString(row.Target, "method"),
			ServerPort: asString(row.Target, "port"),
		}
		if ep.AppName == "" || ep.Route == "" {
			continue
		}
		key := ep.AppName + "|" + ep.Method + "|" + ep.Route + "|" + ep.ServerPort
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppName != out[j].AppName {
			return out[i].AppName < out[j].AppName
		}
		return out[i].Route < out[j].Route
	})
	return out, nil
}

func (r *dbMetadataReader) dnsEndpoints(ctx context.Context) ([]AppDNSPair, error) {
	rows, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	type key struct{ app, domain string }
	seen := make(map[key]struct{})
	out := make([]AppDNSPair, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "dns" {
			continue
		}
		app := asString(row.Target, "app")
		for _, d := range asStringSlice(row.Target, "domain_patterns") {
			k := key{app, d}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, AppDNSPair{AppName: app, Domain: d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppName != out[j].AppName {
			return out[i].AppName < out[j].AppName
		}
		return out[i].Domain < out[j].Domain
	})
	return out, nil
}

func (r *dbMetadataReader) networkPairs(ctx context.Context) ([]AppNetworkPair, error) {
	rows, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	type key struct{ src, dst string }
	seen := make(map[key]struct{})
	out := make([]AppNetworkPair, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "network" {
			continue
		}
		src := asString(row.Target, "source_app")
		dst := asString(row.Target, "target_service")
		if src == "" || dst == "" {
			continue
		}
		k := key{src, dst}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, AppNetworkPair{SourceService: src, TargetService: dst})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceService != out[j].SourceService {
			return out[i].SourceService < out[j].SourceService
		}
		return out[i].TargetService < out[j].TargetService
	})
	return out, nil
}

func (r *dbMetadataReader) jvmMethods(ctx context.Context) ([]AppMethodPair, error) {
	rows, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	type key struct{ app, cls, mth string }
	seen := make(map[key]struct{})
	out := make([]AppMethodPair, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "jvm" {
			continue
		}
		app := asString(row.Target, "app")
		cls := asString(row.Target, "class")
		mth := asString(row.Target, "method")
		if app == "" || cls == "" || mth == "" {
			continue
		}
		k := key{app, cls, mth}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, AppMethodPair{AppName: app, ClassName: cls, MethodName: mth})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppName != out[j].AppName {
			return out[i].AppName < out[j].AppName
		}
		if out[i].ClassName != out[j].ClassName {
			return out[i].ClassName < out[j].ClassName
		}
		return out[i].MethodName < out[j].MethodName
	})
	return out, nil
}

func (r *dbMetadataReader) databaseOperations(ctx context.Context) ([]AppDatabasePair, error) {
	rows, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	type key struct{ app, db, tbl, op string }
	seen := make(map[key]struct{})
	out := make([]AppDatabasePair, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "db" {
			continue
		}
		pair := AppDatabasePair{
			AppName:       asString(row.Target, "app"),
			DBName:        asString(row.Target, "db_name"),
			TableName:     asString(row.Target, "table"),
			OperationType: asString(row.Target, "sql_type"),
		}
		if pair.AppName == "" {
			continue
		}
		k := key{pair.AppName, pair.DBName, pair.TableName, pair.OperationType}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppName != out[j].AppName {
			return out[i].AppName < out[j].AppName
		}
		if out[i].DBName != out[j].DBName {
			return out[i].DBName < out[j].DBName
		}
		if out[i].TableName != out[j].TableName {
			return out[i].TableName < out[j].TableName
		}
		return out[i].OperationType < out[j].OperationType
	})
	return out, nil
}
