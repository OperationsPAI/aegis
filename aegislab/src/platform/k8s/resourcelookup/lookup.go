// Package resourcelookup surfaces the chaos_points table contents through a
// typed per-system cache. The DB-backed ChaosPointStore is required: callers
// must call SetChaosPointStore before invoking any GetAllX method. Without a
// store the five GetAllX methods (HTTP / DNS / network / JVM methods / DB
// operations) return an error so misconfigurations surface immediately
// instead of silently returning empty data.
package resourcelookup

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"

	"aegis/platform/k8s/chaosclient"
	"aegis/platform/systemconfig"
)

// chaosPointsCacheTotal counts hit/miss against the per-system chaos_points
// snapshot shared by all DB-backed GetAllX methods. A miss is one whose
// chaosPointSnapshot() call triggered a fresh ChaosPointStore.QueryPoints;
// every subsequent reuse within the same warm-up is a hit.
var chaosPointsCacheTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "aegis_chaos_points_cache_total",
	Help: "chaos_points per-system snapshot cache lookups by result (hit|miss).",
}, []string{"system", "result"})

// chaosPointsInvalidateTotal counts probe-driven cross-process invalidations:
// `stale` = MAX(updated_at) advanced past the cached high-water mark and the
// per-system snapshot + derived slices were dropped; `fresh` = cached value
// still wins; `probe_error` = the probe itself failed (we keep serving stale).
var chaosPointsInvalidateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "aegis_chaos_points_invalidate_total",
	Help: "chaos_points cross-process invalidation probes by outcome (stale|fresh|probe_error).",
}, []string{"system", "result"})

// GetAllAppLabels returns app labels for the current system.
func GetAllAppLabels(namespace, key string) ([]string, error) {
	return GetSystemCache(systemconfig.GetCurrentSystem()).GetAllAppLabels(context.Background(), namespace, key)
}

// GetAllContainers returns container info for the current system.
func GetAllContainers(namespace string) ([]ContainerInfo, error) {
	return GetSystemCache(systemconfig.GetCurrentSystem()).GetAllContainers(context.Background(), namespace)
}

// AppMethodPair represents a flattened app+method combination
type AppMethodPair struct {
	AppName    string `json:"app_name"`
	ClassName  string `json:"class_name"`
	MethodName string `json:"method_name"`
}

// AppRuntimeMutatorTarget represents a flattened valid runtime mutator target.
type AppRuntimeMutatorTarget struct {
	AppName          string `json:"app_name"`
	ClassName        string `json:"class_name"`
	MethodName       string `json:"method_name"`
	MutationType     int    `json:"mutation_type"`
	MutationTypeName string `json:"mutation_type_name"`
	MutationFrom     string `json:"mutation_from,omitempty"`
	MutationTo       string `json:"mutation_to,omitempty"`
	MutationStrategy string `json:"mutation_strategy,omitempty"`
	Description      string `json:"description,omitempty"`
}

// AppEndpointPair represents a flattened app+endpoint combination
type AppEndpointPair struct {
	AppName       string `json:"app_name"`
	Route         string `json:"route"`
	Method        string `json:"method"`
	ServerAddress string `json:"server_address"`
	ServerPort    string `json:"server_port"`
	SpanName      string `json:"span_name"`
}

// AppNetworkPair represents a flattened source+target combination for network chaos
type AppNetworkPair struct {
	SourceService string   `json:"source_service"`
	TargetService string   `json:"target_service"`
	SpanNames     []string `json:"span_names"`
}

// AppDNSPair represents a flattened app+domain combination for DNS chaos
type AppDNSPair struct {
	AppName   string   `json:"app_name"`
	Domain    string   `json:"domain"`
	SpanNames []string `json:"span_names"`
}

// AppDatabasePair represents a flattened app+database+table+operation combination
type AppDatabasePair struct {
	AppName       string `json:"app_name"`
	DBName        string `json:"db_name"`
	TableName     string `json:"table_name"`
	OperationType string `json:"operation_type"`
}

// ContainerInfo represents container information with its pod and app
type ContainerInfo struct {
	PodName       string `json:"pod_name"`
	AppLabel      string `json:"app_label"`
	ContainerName string `json:"container_name"`
}

type systemCache struct {
	system                systemconfig.SystemType
	appLabels             map[string][]string
	appMethods            []AppMethodPair
	runtimeMutatorTargets []AppRuntimeMutatorTarget
	appEndpoints          []AppEndpointPair
	networkPairs          []AppNetworkPair
	dnsEndpoints          []AppDNSPair
	containerInfo         map[string][]ContainerInfo
	dbOperations          []AppDatabasePair

	dbSnapshotMu        sync.Mutex
	dbSnapshotRows      []ChaosPointRow
	dbSnapshotErr       error
	dbSnapshotOK        bool
	lastSeenUpdatedAt   time.Time
	lastSeenUpdatedAtOK bool
}

func GetSystemCache(system systemconfig.SystemType) *systemCache {
	return getCacheManager().getSystemCache(system)
}

// Callers must hold s.dbSnapshotMu — the cached slice it feeds into is
// guarded by the same lock so a stale snapshot cannot leak through a race
// against probeAndInvalidate.
func (s *systemCache) chaosPointSnapshot(ctx context.Context, store ChaosPointStore) ([]ChaosPointRow, error) {
	system := string(s.system)
	if s.dbSnapshotOK {
		chaosPointsCacheTotal.WithLabelValues(system, "hit").Inc()
		return s.dbSnapshotRows, s.dbSnapshotErr
	}
	chaosPointsCacheTotal.WithLabelValues(system, "miss").Inc()
	s.dbSnapshotRows, s.dbSnapshotErr = store.QueryPoints(ctx, system)
	s.dbSnapshotOK = true
	return s.dbSnapshotRows, s.dbSnapshotErr
}

// probeAndInvalidate is the cross-process freshness hook: chaos-service
// commits a new point in MySQL, any aegis-api replica that probes after the
// commit sees the bumped MAX(updated_at) and drops its stale per-system
// snapshot. Probe errors degrade to "serve cached" rather than block — the
// probe is a liveness optimisation, not a correctness gate. Callers must
// hold s.dbSnapshotMu.
func (s *systemCache) probeAndInvalidate(ctx context.Context, store ChaosPointStore) {
	system := string(s.system)
	latest, err := store.LatestUpdate(ctx, system)
	if err != nil {
		chaosPointsInvalidateTotal.WithLabelValues(system, "probe_error").Inc()
		logrus.Warnf("resourcelookup: LatestUpdate probe for system %q failed, serving cached snapshot: %v", system, err)
		return
	}
	if !s.lastSeenUpdatedAtOK {
		s.lastSeenUpdatedAt = latest
		s.lastSeenUpdatedAtOK = true
		chaosPointsInvalidateTotal.WithLabelValues(system, "fresh").Inc()
		return
	}
	if latest.After(s.lastSeenUpdatedAt) {
		s.lastSeenUpdatedAt = latest
		s.dropChaosPointDerivedLocked()
		chaosPointsInvalidateTotal.WithLabelValues(system, "stale").Inc()
		return
	}
	chaosPointsInvalidateTotal.WithLabelValues(system, "fresh").Inc()
}

// dropChaosPointDerivedLocked clears the per-system snapshot and every
// chaos_points-derived slice. k8s-derived appLabels / containerInfo are not
// chaos_points-backed, so the probe deliberately does not touch them.
// Callers must hold s.dbSnapshotMu.
func (s *systemCache) dropChaosPointDerivedLocked() {
	s.dbSnapshotRows = nil
	s.dbSnapshotErr = nil
	s.dbSnapshotOK = false
	s.appEndpoints = []AppEndpointPair{}
	s.networkPairs = []AppNetworkPair{}
	s.dnsEndpoints = []AppDNSPair{}
	s.dbOperations = []AppDatabasePair{}
	s.appMethods = []AppMethodPair{}
	s.runtimeMutatorTargets = []AppRuntimeMutatorTarget{}
}

// chaosPointDerived is the shared probe-then-extract path used by every
// chaos_points-derived GetAllX. The lock spans the probe, cache check, and
// extract so the slice cannot be read concurrently with probeAndInvalidate
// clearing it.
func chaosPointDerived[T any](
	s *systemCache,
	cached *[]T,
	extract func(rows []ChaosPointRow) []T,
) ([]T, error) {
	store, err := requireStore()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	s.dbSnapshotMu.Lock()
	defer s.dbSnapshotMu.Unlock()
	s.probeAndInvalidate(ctx, store)
	if len(*cached) > 0 {
		return *cached, nil
	}
	rows, err := s.chaosPointSnapshot(ctx, store)
	if err != nil {
		return nil, err
	}
	*cached = extract(rows)
	return *cached, nil
}

// requireStore returns the installed ChaosPointStore or an error explaining
// that one must be wired before resource lookups can run.
func requireStore() (ChaosPointStore, error) {
	store := getChaosPointStore()
	if store == nil {
		return nil, fmt.Errorf("resourcelookup: ChaosPointStore not installed; call resourcelookup.SetChaosPointStore from the host process before invoking GetAll*")
	}
	return store, nil
}

// ResetSystemCache clears and removes cached lookup data for a system.
func ResetSystemCache(system systemconfig.SystemType) {
	cm := getCacheManager()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.caches, system)
}

func newSystemCache(system systemconfig.SystemType) *systemCache {
	return &systemCache{
		system:                system,
		appLabels:             make(map[string][]string),
		appMethods:            []AppMethodPair{},
		runtimeMutatorTargets: []AppRuntimeMutatorTarget{},
		appEndpoints:          []AppEndpointPair{},
		networkPairs:          []AppNetworkPair{},
		dnsEndpoints:          []AppDNSPair{},
		dbOperations:          []AppDatabasePair{},
		containerInfo:         make(map[string][]ContainerInfo),
	}
}

type cacheManager struct {
	caches map[systemconfig.SystemType]*systemCache
	mu     sync.RWMutex
}

var (
	managerInstance *cacheManager
	managerOnce     sync.Once
)

func getCacheManager() *cacheManager {
	managerOnce.Do(func() {
		allSystemTypes := systemconfig.GetAllSystemTypes()
		managerInstance = &cacheManager{
			caches: make(map[systemconfig.SystemType]*systemCache, len(allSystemTypes)),
		}
	})
	return managerInstance
}

func (cm *cacheManager) getSystemCache(system systemconfig.SystemType) *systemCache {
	cm.mu.RLock()
	cache, exists := cm.caches[system]
	cm.mu.RUnlock()

	if !exists {
		cm.mu.Lock()
		defer cm.mu.Unlock()
		cache, exists = cm.caches[system]
		if !exists {
			cache = newSystemCache(system)
			cm.caches[system] = cache
		}
	}

	return cache
}

// GetAllAppLabels reads pod labels from the live cluster. The static service
// fallback is gone — without cluster access this returns an empty list.
func (s *systemCache) GetAllAppLabels(ctx context.Context, namespace string, key string) ([]string, error) {
	if labels, exists := s.appLabels[key]; exists && len(labels) > 0 {
		return labels, nil
	}

	labels, err := chaosclient.GetLabels(ctx, namespace, key)
	if err != nil {
		return nil, err
	}
	sort.Strings(labels)
	s.appLabels[key] = labels
	return labels, nil
}

// GetAllJVMMethods returns all app+method pairs sourced from chaos_points.
func (s *systemCache) GetAllJVMMethods() ([]AppMethodPair, error) {
	return chaosPointDerived(s, &s.appMethods, extractJVMMethods)
}

// GetAllJVMRuntimeMutatorTargets returns all valid runtime mutator targets
// sourced from chaos_points (jvm_runtime_mutator capability).
func (s *systemCache) GetAllJVMRuntimeMutatorTargets() ([]AppRuntimeMutatorTarget, error) {
	return chaosPointDerived(s, &s.runtimeMutatorTargets, extractRuntimeMutatorTargets)
}

// GetAllHTTPEndpoints returns all app+endpoint pairs sourced from chaos_points.
func (s *systemCache) GetAllHTTPEndpoints() ([]AppEndpointPair, error) {
	return chaosPointDerived(s, &s.appEndpoints, extractHTTPEndpoints)
}

// GetAllNetworkPairs returns all network pairs sourced from chaos_points.
func (s *systemCache) GetAllNetworkPairs() ([]AppNetworkPair, error) {
	return chaosPointDerived(s, &s.networkPairs, extractNetworkPairs)
}

// GetAllDNSEndpoints returns all app+domain pairs sourced from chaos_points.
func (s *systemCache) GetAllDNSEndpoints() ([]AppDNSPair, error) {
	return chaosPointDerived(s, &s.dnsEndpoints, extractDNSEndpoints)
}

// GetAllDatabaseOperations returns all app+database+table+operation pairs
// sourced from chaos_points. Only MySQL operations are emitted by the dump tool.
func (s *systemCache) GetAllDatabaseOperations() ([]AppDatabasePair, error) {
	return chaosPointDerived(s, &s.dbOperations, extractDatabaseOperations)
}

// GetAllContainers returns all containers with their info sorted by app label
func (s *systemCache) GetAllContainers(ctx context.Context, namespace string) ([]ContainerInfo, error) {
	if len(s.containerInfo) > 0 {
		if containers, exists := s.containerInfo[namespace]; exists {
			return containers, nil
		}
	}

	containers, err := chaosclient.GetContainersWithAppLabel(ctx, namespace, systemconfig.GetAppLabelKey(s.system))
	if err != nil {
		return nil, err
	}

	result := make([]ContainerInfo, 0, len(containers))
	for _, c := range containers {
		if c["appLabel"] != "" {
			result = append(result, ContainerInfo{
				PodName:       c["podName"],
				AppLabel:      c["appLabel"],
				ContainerName: c["containerName"],
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].AppLabel != result[j].AppLabel {
			return result[i].AppLabel < result[j].AppLabel
		}
		return result[i].ContainerName < result[j].ContainerName
	})

	s.containerInfo[namespace] = result
	return result, nil
}

// GetContainersByService returns all container names for a specific service
func (s *systemCache) GetContainersByService(ctx context.Context, namespace string, serviceName string) ([]string, error) {
	allContainers, err := s.GetAllContainers(ctx, namespace)
	if err != nil {
		return nil, err
	}

	containerNames := []string{}
	for _, container := range allContainers {
		if container.AppLabel == serviceName {
			containerNames = append(containerNames, container.ContainerName)
		}
	}

	sort.Strings(containerNames)
	return containerNames, nil
}

// GetPodsByService returns all pod names for a specific service
func (s *systemCache) GetPodsByService(ctx context.Context, namespace string, serviceName string) ([]string, error) {
	allContainers, err := s.GetAllContainers(ctx, namespace)
	if err != nil {
		return nil, err
	}

	podMap := make(map[string]bool)
	for _, container := range allContainers {
		if container.AppLabel == serviceName {
			podMap[container.PodName] = true
		}
	}

	pods := make([]string, 0, len(podMap))
	for pod := range podMap {
		pods = append(pods, pod)
	}

	sort.Strings(pods)
	return pods, nil
}

// GetContainersAndPodsByServices returns containers and pods for multiple services
func (s *systemCache) GetContainersAndPodsByServices(ctx context.Context, namespace string, serviceNames []string) ([]string, []string, error) {
	allContainers, err := s.GetAllContainers(ctx, namespace)
	if err != nil {
		return nil, nil, err
	}

	containerMap := make(map[string]bool)
	podMap := make(map[string]bool)

	serviceMap := make(map[string]bool)
	for _, service := range serviceNames {
		serviceMap[service] = true
	}

	for _, container := range allContainers {
		if serviceMap[container.AppLabel] {
			containerMap[container.ContainerName] = true
			podMap[container.PodName] = true
		}
	}

	containers := make([]string, 0, len(containerMap))
	for container := range containerMap {
		containers = append(containers, container)
	}

	pods := make([]string, 0, len(podMap))
	for pod := range podMap {
		pods = append(pods, pod)
	}

	sort.Strings(containers)
	sort.Strings(pods)

	return containers, pods, nil
}

// InvalidateCache clears all cached data
func (s *systemCache) InvalidateCache() {
	s.appLabels = make(map[string][]string)
	s.appMethods = []AppMethodPair{}
	s.runtimeMutatorTargets = []AppRuntimeMutatorTarget{}
	s.appEndpoints = []AppEndpointPair{}
	s.networkPairs = []AppNetworkPair{}
	s.dnsEndpoints = []AppDNSPair{}
	s.containerInfo = make(map[string][]ContainerInfo)
	s.dbOperations = []AppDatabasePair{}

	s.dbSnapshotMu.Lock()
	s.dbSnapshotRows = nil
	s.dbSnapshotErr = nil
	s.dbSnapshotOK = false
	s.lastSeenUpdatedAt = time.Time{}
	s.lastSeenUpdatedAtOK = false
	s.dbSnapshotMu.Unlock()
}
