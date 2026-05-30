// Reads chaos_points via ChaosPointStore.
package resourcelookup

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
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
	// LatestUpdate returns MAX(updated_at) across all chaos_points rows for
	// the given system, regardless of status. A zero time means no rows exist
	// for this system yet. The probe drives cross-process cache invalidation:
	// any import / supersede / tombstone bumps updated_at, so a strictly newer
	// value than the cached high-water mark forces the next GetAllX to
	// re-fetch instead of returning stale per-process data.
	LatestUpdate(ctx context.Context, system string) (time.Time, error)
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
// http_*       → service endpoints (target: app/method/path/port [+server_address/span_name])
// network_*    → network pairs     (target: source_app/target_service [+span_names])
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
	case capability == "jvm_runtime_mutator":
		return "runtime-mutator"
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
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return ""
	}
	logrus.Warnf("chaos: target field %q has unexpected type %T", k, v)
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

// extractHTTPEndpoints / extractDNSEndpoints / etc. translate a single
// chaos_points snapshot into the typed slices the systemCache exposes.
// Splitting these out of dbMetadataReader keeps the shared-snapshot warm-up
// in one place (systemCache.fetchDBSnapshot below).

func extractHTTPEndpoints(rows []ChaosPointRow) []AppEndpointPair {
	seen := make(map[string]struct{})
	out := make([]AppEndpointPair, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "http" {
			continue
		}
		ep := AppEndpointPair{
			AppName:       asString(row.Target, "app"),
			Route:         asString(row.Target, "path"),
			Method:        asString(row.Target, "method"),
			ServerAddress: asString(row.Target, "server_address"),
			ServerPort:    asString(row.Target, "port"),
			SpanName:      asString(row.Target, "span_name"),
		}
		if ep.AppName == "" || ep.Route == "" {
			continue
		}
		// Multiple http_* capabilities sharing one (app, path, method, port)
		// collapse to a single endpoint so HTTP enumeration matches the
		// in-memory contract (one endpoint per route).
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
	return out
}

func extractDNSEndpoints(rows []ChaosPointRow) []AppDNSPair {
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
	return out
}

func extractNetworkPairs(rows []ChaosPointRow) []AppNetworkPair {
	type key struct{ src, dst string }
	seen := make(map[key]struct{})
	pairs := make(map[key]*AppNetworkPair)
	order := make([]key, 0)
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
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			order = append(order, k)
			pairs[k] = &AppNetworkPair{SourceService: src, TargetService: dst}
		}
		// Every network_* row for the same (src, dst) carries the same
		// span_names blob (the dump tool writes it identically across
		// the 6 capabilities); first non-empty wins.
		if len(pairs[k].SpanNames) == 0 {
			if names := asStringSlice(row.Target, "span_names"); len(names) > 0 {
				pairs[k].SpanNames = names
			}
		}
	}
	out := make([]AppNetworkPair, 0, len(order))
	for _, k := range order {
		out = append(out, *pairs[k])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceService != out[j].SourceService {
			return out[i].SourceService < out[j].SourceService
		}
		return out[i].TargetService < out[j].TargetService
	})
	return out
}

func extractJVMMethods(rows []ChaosPointRow) []AppMethodPair {
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
	return out
}

func extractDatabaseOperations(rows []ChaosPointRow) []AppDatabasePair {
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
	return out
}

func extractRuntimeMutatorTargets(rows []ChaosPointRow) []AppRuntimeMutatorTarget {
	type key struct{ app, cls, mth, name, from, to, strat string }
	seen := make(map[key]struct{})
	out := make([]AppRuntimeMutatorTarget, 0)
	for _, row := range rows {
		if familyOf(row.CapabilityName) != "runtime-mutator" {
			continue
		}
		t := AppRuntimeMutatorTarget{
			AppName:          asString(row.Target, "app"),
			ClassName:        asString(row.Target, "class"),
			MethodName:       asString(row.Target, "method"),
			MutationType:     asInt(row.Target, "mutation_type"),
			MutationTypeName: asString(row.Target, "mutation_type_name"),
			MutationFrom:     asString(row.Target, "mutation_from"),
			MutationTo:       asString(row.Target, "mutation_to"),
			MutationStrategy: asString(row.Target, "mutation_strategy"),
			Description:      asString(row.Target, "description"),
		}
		if t.AppName == "" || t.ClassName == "" || t.MethodName == "" || t.MutationTypeName == "" {
			continue
		}
		k := key{t.AppName, t.ClassName, t.MethodName, t.MutationTypeName, t.MutationFrom, t.MutationTo, t.MutationStrategy}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppName != out[j].AppName {
			return out[i].AppName < out[j].AppName
		}
		if out[i].ClassName != out[j].ClassName {
			return out[i].ClassName < out[j].ClassName
		}
		if out[i].MethodName != out[j].MethodName {
			return out[i].MethodName < out[j].MethodName
		}
		if out[i].MutationType != out[j].MutationType {
			return out[i].MutationType < out[j].MutationType
		}
		if out[i].MutationStrategy != out[j].MutationStrategy {
			return out[i].MutationStrategy < out[j].MutationStrategy
		}
		if out[i].MutationFrom != out[j].MutationFrom {
			return out[i].MutationFrom < out[j].MutationFrom
		}
		return out[i].MutationTo < out[j].MutationTo
	})
	return out
}

func asInt(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	switch t := m[k].(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return 0
}
