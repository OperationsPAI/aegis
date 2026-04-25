package guidedcli

import (
	"context"
	"fmt"
	"sort"

	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// EnumerateAllCandidates returns every reachable (app, chaos_type, target) tuple
// for the given system+namespace, with one entry per leaf in the guided
// enumeration tree. Equivalent to walking GuidedResolve from empty state to
// ready_to_apply for every branch, but in-process — no HTTP round-trips.
//
// Numerical parameter grids (CPULoad, CPUWorker, Latency, ...) are NOT expanded.
// The returned GuidedConfig leaves these fields nil and callers fill them with
// policy-specific defaults before BuildInjection. The shape is otherwise
// identical to a guided-resolve "ready_to_apply" GuidedConfig.
//
// Cost is dominated by one pod-list per namespace plus the resourcelookup
// cache populates (single pass through static metadata). After the first call
// per system, subsequent enumerations are nearly free.
func EnumerateAllCandidates(ctx context.Context, system, namespace string) ([]GuidedConfig, error) {
	cfg := GuidedConfig{System: system, Namespace: namespace}
	if err := normalizeSystemSelection(&cfg); err != nil {
		return nil, fmt.Errorf("enumerate candidates: %w", err)
	}
	if cfg.SystemType == "" {
		return nil, fmt.Errorf("enumerate candidates: system %q is not registered", system)
	}

	systemType, err := systemconfig.ParseSystemType(cfg.SystemType)
	if err != nil {
		return nil, fmt.Errorf("enumerate candidates: %w", err)
	}
	if err := systemconfig.SetCurrentSystem(systemType); err != nil {
		return nil, fmt.Errorf("enumerate candidates: %w", err)
	}

	apps, err := enumerateAppLabelsHook(ctx, cfg.Namespace, systemType)
	if err != nil {
		return nil, fmt.Errorf("enumerate candidates: list apps in %s: %w", cfg.Namespace, err)
	}

	out := make([]GuidedConfig, 0)
	for _, app := range apps {
		appCfg := cfg
		appCfg.App = app

		// Mirror availableChaosTypeOptions gating: chaos types are gated on
		// the resources the app actually has. Walk each gate in turn and
		// generate one GuidedConfig per leaf.
		containers, _ := enumerateContainersByAppHook(ctx, systemType, cfg.Namespace, app)
		endpoints, _ := enumerateHTTPEndpointsByAppHook(systemType, app)
		networkTargets, _ := enumerateNetworkTargetsByAppHook(systemType, app)
		dnsDomains, _ := enumerateDNSDomainsByAppHook(systemType, app)
		methods, _ := enumerateJVMMethodsByAppHook(systemType, app)
		dbOps, _ := enumerateDatabaseOpsByAppHook(systemType, app)
		mutatorTargets, _ := enumerateRuntimeMutatorMethodsByAppHook(systemType, app)

		// Pod-level chaos: 1 leaf per app for PodKill/PodFailure (no
		// sub-target). ContainerKill/CPUStress/MemoryStress/TimeSkew = 1
		// leaf per container.
		if len(containers) > 0 {
			out = append(out,
				withChaosType(appCfg, "PodKill"),
				withChaosType(appCfg, "PodFailure"),
			)
			for _, c := range containers {
				cc := withChaosType(appCfg, "ContainerKill")
				cc.Container = c
				out = append(out, cc)

				cs := withChaosType(appCfg, "CPUStress")
				cs.Container = c
				out = append(out, cs)

				ms := withChaosType(appCfg, "MemoryStress")
				ms.Container = c
				out = append(out, ms)

				ts := withChaosType(appCfg, "TimeSkew")
				ts.Container = c
				out = append(out, ts)
			}
		}

		// HTTP chaos: leaves = httpEndpointsByApp.
		if len(endpoints) > 0 {
			httpTypes := []string{
				"HTTPRequestAbort",
				"HTTPResponseAbort",
				"HTTPRequestDelay",
				"HTTPResponseDelay",
				"HTTPResponseReplaceBody",
				"HTTPResponsePatchBody",
				"HTTPRequestReplacePath",
				"HTTPRequestReplaceMethod",
				"HTTPResponseReplaceCode",
			}
			for _, ct := range httpTypes {
				for _, ep := range endpoints {
					hc := withChaosType(appCfg, ct)
					hc.Route = ep.Route
					hc.HTTPMethod = ep.Method
					out = append(out, hc)
				}
			}
		}

		// Network chaos: leaves = networkTargetsByApp.
		if len(networkTargets) > 0 {
			netTypes := []string{
				"NetworkDelay",
				"NetworkPartition",
				"NetworkLoss",
				"NetworkDuplicate",
				"NetworkCorrupt",
				"NetworkBandwidth",
			}
			for _, ct := range netTypes {
				for _, t := range networkTargets {
					nc := withChaosType(appCfg, ct)
					nc.TargetService = t
					out = append(out, nc)
				}
			}
		}

		// DNS chaos: leaves = dnsDomainsByApp.
		if len(dnsDomains) > 0 {
			dnsTypes := []string{"DNSError", "DNSRandom"}
			for _, ct := range dnsTypes {
				for _, d := range dnsDomains {
					dc := withChaosType(appCfg, ct)
					dc.Domain = d.Domain
					out = append(out, dc)
				}
			}
		}

		// JVM method chaos (non-MySQL / non-mutator): leaves = jvmMethodsByApp.
		// JVMGarbageCollector: 1 leaf per app (no sub-target) but only when
		// the app has any JVM methods recorded — same gate that
		// availableChaosTypeOptions uses.
		if len(methods) > 0 {
			out = append(out, withChaosType(appCfg, "JVMGarbageCollector"))

			methodTypes := []string{
				"JVMLatency",
				"JVMReturn",
				"JVMException",
				"JVMCPUStress",
				"JVMMemoryStress",
			}
			for _, ct := range methodTypes {
				for _, m := range methods {
					mc := withChaosType(appCfg, ct)
					mc.Class = m.ClassName
					mc.Method = m.MethodName
					out = append(out, mc)
				}
			}
		}

		// JVM MySQL chaos: leaves = databaseOpsByApp.
		if len(dbOps) > 0 {
			dbTypes := []string{"JVMMySQLLatency", "JVMMySQLException"}
			for _, ct := range dbTypes {
				for _, op := range dbOps {
					dc := withChaosType(appCfg, ct)
					dc.Database = op.DBName
					dc.Table = op.TableName
					dc.Operation = op.OperationType
					out = append(out, dc)
				}
			}
		}

		// JVMRuntimeMutator: leaves = (mutator method) × (mutator config per
		// method). Match resolveJVMRuntimeMutator's two-step expansion.
		if len(mutatorTargets) > 0 {
			// Group mutators by (class, method) so each method emits one
			// candidate per mutator config — mirrors the guided two-step
			// walk: pick method, then pick mutator config.
			byMethod := map[string][]runtimeMutatorTarget{}
			methodOrder := make([]string, 0)
			for _, t := range mutatorTargets {
				key := t.ClassName + "#" + t.MethodName
				if _, seen := byMethod[key]; !seen {
					methodOrder = append(methodOrder, key)
				}
				byMethod[key] = append(byMethod[key], t)
			}
			for _, key := range methodOrder {
				for _, t := range byMethod[key] {
					mc := withChaosType(appCfg, "JVMRuntimeMutator")
					mc.Class = t.ClassName
					mc.Method = t.MethodName
					mc.MutatorConfig = runtimeMutatorKey(t)
					out = append(out, mc)
				}
			}
		}
	}

	sortCandidates(out)
	return out, nil
}

// withChaosType returns a copy of cfg with ChaosType set. Defensive copy keeps
// each candidate independent of others (no aliasing).
func withChaosType(cfg GuidedConfig, chaosType string) GuidedConfig {
	cfg.ChaosType = chaosType
	return cfg
}

// sortCandidates orders the result deterministically: by app, then chaos type,
// then the leaf-identifying fields. Lets callers (and tests) compare against
// a fixed expected slice without flakiness.
func sortCandidates(items []GuidedConfig) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.App != b.App {
			return a.App < b.App
		}
		if a.ChaosType != b.ChaosType {
			return a.ChaosType < b.ChaosType
		}
		if a.Container != b.Container {
			return a.Container < b.Container
		}
		if a.HTTPMethod != b.HTTPMethod {
			return a.HTTPMethod < b.HTTPMethod
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		if a.TargetService != b.TargetService {
			return a.TargetService < b.TargetService
		}
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.MutatorConfig != b.MutatorConfig {
			return a.MutatorConfig < b.MutatorConfig
		}
		if a.Database != b.Database {
			return a.Database < b.Database
		}
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		return a.Operation < b.Operation
	})
}

// --- testable indirection: package-level hooks default to the real helpers,
// allowing unit tests to inject fixture data without standing up a fake k8s
// API server or seeding the resourcelookup cache. Tests should restore each
// hook (defer) so they don't leak fixtures into other tests.

var (
	enumerateAppLabelsHook                  = func(ctx context.Context, namespace string, systemType systemconfig.SystemType) ([]string, error) {
		return safeAppLabels(ctx, namespace, systemType)
	}
	enumerateContainersByAppHook            = containersByApp
	enumerateHTTPEndpointsByAppHook         = httpEndpointsByApp
	enumerateNetworkTargetsByAppHook        = networkTargetsByApp
	enumerateDNSDomainsByAppHook            = dnsDomainsByApp
	enumerateJVMMethodsByAppHook            = jvmMethodsByApp
	enumerateDatabaseOpsByAppHook           = databaseOpsByApp
	enumerateRuntimeMutatorMethodsByAppHook = runtimeMutatorMethodsByApp
)

