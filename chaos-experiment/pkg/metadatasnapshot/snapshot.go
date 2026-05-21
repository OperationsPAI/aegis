// Package metadatasnapshot exposes a one-shot read-only view of the
// internal/* metadata for migration into chaos-service's chaos_points table.
//
// Created for Phase A1b; will be deleted alongside chaos-experiment in
// Phase A4. Consumers pass the system as a string (e.g. "ts", "otel-demo")
// so they need not depend on internal/systemconfig's enum.
package metadatasnapshot

import (
	"fmt"

	"github.com/OperationsPAI/chaos-experiment/internal/resourcelookup"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// HTTPEndpoint is a flattened app+endpoint pair for HTTP chaos targeting.
type HTTPEndpoint struct {
	AppName       string
	Route         string
	Method        string
	ServerAddress string
	ServerPort    string
	SpanName      string
}

// DNSEndpoint is a flattened app+domain pair for DNS chaos targeting.
type DNSEndpoint struct {
	AppName string
	Domain  string
}

// NetworkPair is a flattened source+target service pair for network chaos.
type NetworkPair struct {
	SourceService string
	TargetService string
}

// JVMMethod is a flattened app+class+method triple for JVM method chaos.
type JVMMethod struct {
	AppName    string
	ClassName  string
	MethodName string
}

// DBOperation is a flattened app+database+table+operation tuple for JVM
// mysql chaos. Only MySQL is included (resourcelookup filters at source).
type DBOperation struct {
	AppName       string
	DBName        string
	TableName     string
	OperationType string
}

// Snapshot bundles all metadata listings for one system.
type Snapshot struct {
	System        string
	HTTPEndpoints []HTTPEndpoint
	DNSEndpoints  []DNSEndpoint
	NetworkPairs  []NetworkPair
	JVMMethods    []JVMMethod
	DBOperations  []DBOperation
}

// DumpForSystem reads all in-memory metadata for the given system code
// (e.g. "ts", "otel-demo") and returns it as a Snapshot. Calls
// systemconfig.SetCurrentSystem so per-system static providers dispatch
// correctly; on return the global system pointer is left at the last
// system dumped.
func DumpForSystem(system string) (Snapshot, error) {
	t, err := systemconfig.ParseSystemType(system)
	if err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: %w", err)
	}
	if err := systemconfig.SetCurrentSystem(t); err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: set current: %w", err)
	}
	cache := resourcelookup.GetSystemCache(t)

	http, err := cache.GetAllHTTPEndpoints()
	if err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: http: %w", err)
	}
	dns, err := cache.GetAllDNSEndpoints()
	if err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: dns: %w", err)
	}
	net, err := cache.GetAllNetworkPairs()
	if err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: network: %w", err)
	}
	methods, err := cache.GetAllJVMMethods()
	if err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: jvm methods: %w", err)
	}
	dbs, err := cache.GetAllDatabaseOperations()
	if err != nil {
		return Snapshot{}, fmt.Errorf("metadatasnapshot: db ops: %w", err)
	}

	snap := Snapshot{System: system}

	snap.HTTPEndpoints = make([]HTTPEndpoint, len(http))
	for i, ep := range http {
		snap.HTTPEndpoints[i] = HTTPEndpoint{
			AppName:       ep.AppName,
			Route:         ep.Route,
			Method:        ep.Method,
			ServerAddress: ep.ServerAddress,
			ServerPort:    ep.ServerPort,
			SpanName:      ep.SpanName,
		}
	}

	snap.DNSEndpoints = make([]DNSEndpoint, len(dns))
	for i, d := range dns {
		snap.DNSEndpoints[i] = DNSEndpoint{
			AppName: d.AppName,
			Domain:  d.Domain,
		}
	}

	snap.NetworkPairs = make([]NetworkPair, len(net))
	for i, np := range net {
		snap.NetworkPairs[i] = NetworkPair{
			SourceService: np.SourceService,
			TargetService: np.TargetService,
		}
	}

	snap.JVMMethods = make([]JVMMethod, len(methods))
	for i, m := range methods {
		snap.JVMMethods[i] = JVMMethod{
			AppName:    m.AppName,
			ClassName:  m.ClassName,
			MethodName: m.MethodName,
		}
	}

	snap.DBOperations = make([]DBOperation, len(dbs))
	for i, op := range dbs {
		snap.DBOperations[i] = DBOperation{
			AppName:       op.AppName,
			DBName:        op.DBName,
			TableName:     op.TableName,
			OperationType: op.OperationType,
		}
	}

	return snap, nil
}
