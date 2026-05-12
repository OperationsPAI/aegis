package common

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"aegis/platform/model"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"gorm.io/gorm"
)

// DBMetadataStore implements chaos.MetadataStore by reading from MySQL with in-memory caching.
type DBMetadataStore struct {
	db    *gorm.DB
	cache sync.Map // key: "system:type:service" -> cached data
}

type ServiceTopologyData struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace,omitempty"`
	Pods      []string `json:"pods,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

const (
	metadataTypeServiceEndpoints      = "service_endpoints"
	metadataTypeDatabaseOperations    = "database_operations"
	metadataTypeJavaClassMethods      = "java_class_methods"
	metadataTypeGRPCOperations        = "grpc_operations"
	metadataTypeNetworkDependencies   = "network_dependencies"
	metadataTypeRuntimeMutatorTargets = "runtime_mutator_targets"
	metadataTypeServiceTopology       = "service_topology"
)

var metadataTypeAliases = map[string]string{
	metadataTypeServiceEndpoints:      metadataTypeServiceEndpoints,
	"service_endpoint":                metadataTypeServiceEndpoints,
	metadataTypeDatabaseOperations:    metadataTypeDatabaseOperations,
	"database_operation":              metadataTypeDatabaseOperations,
	metadataTypeJavaClassMethods:      metadataTypeJavaClassMethods,
	"java_class_method":               metadataTypeJavaClassMethods,
	metadataTypeGRPCOperations:        metadataTypeGRPCOperations,
	"grpc_operation":                  metadataTypeGRPCOperations,
	metadataTypeNetworkDependencies:   metadataTypeNetworkDependencies,
	"network_dependency":              metadataTypeNetworkDependencies,
	metadataTypeRuntimeMutatorTargets: metadataTypeRuntimeMutatorTargets,
	"runtime_mutator_target":          metadataTypeRuntimeMutatorTargets,
	metadataTypeServiceTopology:       metadataTypeServiceTopology,
	"topology":                        metadataTypeServiceTopology,
}

var (
	globalMetadataStoreMu sync.RWMutex
	globalMetadataStore   *DBMetadataStore
)

// NewDBMetadataStore creates a new DBMetadataStore instance.
func NewDBMetadataStore(db *gorm.DB) *DBMetadataStore {
	store := &DBMetadataStore{db: db}
	globalMetadataStoreMu.Lock()
	globalMetadataStore = store
	globalMetadataStoreMu.Unlock()
	return store
}

func InvalidateGlobalMetadataStoreCache() {
	globalMetadataStoreMu.RLock()
	store := globalMetadataStore
	globalMetadataStoreMu.RUnlock()
	if store != nil {
		store.InvalidateCache()
	}
}

func (s *DBMetadataStore) cacheKey(system, metaType, service string) string {
	return system + ":" + normalizeMetadataType(metaType) + ":" + service
}

func normalizeMetadataType(metaType string) string {
	key := strings.ToLower(strings.TrimSpace(metaType))
	if normalized, ok := metadataTypeAliases[key]; ok {
		return normalized
	}
	return key
}

func NormalizeMetadataTypeForWrite(metaType string) string {
	return normalizeMetadataType(metaType)
}

func metadataTypeCandidates(metaType string) []string {
	normalized := normalizeMetadataType(metaType)
	candidates := []string{normalized}
	for alias, canonical := range metadataTypeAliases {
		if canonical == normalized && alias != normalized {
			candidates = append(candidates, alias)
		}
	}
	return candidates
}

func (s *DBMetadataStore) GetServiceEndpoints(system, serviceName string) ([]chaos.ServiceEndpointData, error) {
	key := s.cacheKey(system, metadataTypeServiceEndpoints, serviceName)
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]chaos.ServiceEndpointData), nil
	}

	meta, err := s.getSystemMetadata(system, metadataTypeServiceEndpoints, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get service endpoints: %w", err)
	}
	if meta == nil {
		return nil, nil
	}

	var result []chaos.ServiceEndpointData
	if err := json.Unmarshal([]byte(meta.Data), &result); err != nil {
		return nil, fmt.Errorf("failed to parse service endpoint data: %w", err)
	}

	s.cache.Store(key, result)
	return result, nil
}

func (s *DBMetadataStore) GetAllServiceNames(system string) ([]string, error) {
	key := s.cacheKey(system, "_all_services", "")
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]string), nil
	}

	names, err := s.listServiceNames(system, metadataTypeServiceEndpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to get service names: %w", err)
	}
	if len(names) == 0 {
		names, err = s.listServiceNames(system, metadataTypeServiceTopology)
		if err != nil {
			return nil, fmt.Errorf("failed to get service names from topology: %w", err)
		}
	}

	s.cache.Store(key, names)
	return names, nil
}

func (s *DBMetadataStore) GetJavaClassMethods(system, serviceName string) ([]chaos.JavaClassMethodData, error) {
	key := s.cacheKey(system, metadataTypeJavaClassMethods, serviceName)
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]chaos.JavaClassMethodData), nil
	}

	meta, err := s.getSystemMetadata(system, metadataTypeJavaClassMethods, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get java class methods: %w", err)
	}
	if meta == nil {
		return nil, nil
	}

	var result []chaos.JavaClassMethodData
	if err := json.Unmarshal([]byte(meta.Data), &result); err != nil {
		return nil, fmt.Errorf("failed to parse java class method data: %w", err)
	}

	s.cache.Store(key, result)
	return result, nil
}

func (s *DBMetadataStore) GetDatabaseOperations(system, serviceName string) ([]chaos.DatabaseOperationData, error) {
	key := s.cacheKey(system, metadataTypeDatabaseOperations, serviceName)
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]chaos.DatabaseOperationData), nil
	}

	meta, err := s.getSystemMetadata(system, metadataTypeDatabaseOperations, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get database operations: %w", err)
	}
	if meta == nil {
		return nil, nil
	}

	var result []chaos.DatabaseOperationData
	if err := json.Unmarshal([]byte(meta.Data), &result); err != nil {
		return nil, fmt.Errorf("failed to parse database operation data: %w", err)
	}

	s.cache.Store(key, result)
	return result, nil
}

func (s *DBMetadataStore) GetGRPCOperations(system, serviceName string) ([]chaos.GRPCOperationData, error) {
	key := s.cacheKey(system, metadataTypeGRPCOperations, serviceName)
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]chaos.GRPCOperationData), nil
	}

	meta, err := s.getSystemMetadata(system, metadataTypeGRPCOperations, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get gRPC operations: %w", err)
	}
	if meta == nil {
		return nil, nil
	}

	var result []chaos.GRPCOperationData
	if err := json.Unmarshal([]byte(meta.Data), &result); err != nil {
		return nil, fmt.Errorf("failed to parse gRPC operation data: %w", err)
	}

	s.cache.Store(key, result)
	return result, nil
}

func (s *DBMetadataStore) GetNetworkPairs(system string) ([]chaos.NetworkPairData, error) {
	key := s.cacheKey(system, metadataTypeNetworkDependencies, "_all")
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]chaos.NetworkPairData), nil
	}

	metas, err := s.listSystemMetadata(system, metadataTypeNetworkDependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to get network pairs: %w", err)
	}

	var result []chaos.NetworkPairData
	for _, meta := range metas {
		var pairs []chaos.NetworkPairData
		if err := json.Unmarshal([]byte(meta.Data), &pairs); err != nil {
			return nil, fmt.Errorf("failed to parse network pair data: %w", err)
		}
		result = append(result, pairs...)
	}

	if len(result) == 0 {
		topologyMetas, err := s.listSystemMetadata(system, metadataTypeServiceTopology)
		if err != nil {
			return nil, fmt.Errorf("failed to get topology metadata: %w", err)
		}
		for _, meta := range topologyMetas {
			var topology ServiceTopologyData
			if err := json.Unmarshal([]byte(meta.Data), &topology); err != nil {
				return nil, fmt.Errorf("failed to parse service topology data: %w", err)
			}
			source := topology.Name
			if source == "" {
				source = meta.ServiceName
			}
			for _, target := range topology.DependsOn {
				if source == "" || target == "" {
					continue
				}
				result = append(result, chaos.NetworkPairData{Source: source, Target: target})
			}
		}
	}

	s.cache.Store(key, result)
	return result, nil
}

func (s *DBMetadataStore) GetRuntimeMutatorTargets(system string) ([]chaos.RuntimeMutatorTargetData, error) {
	key := s.cacheKey(system, metadataTypeRuntimeMutatorTargets, "_all")
	if cached, ok := s.cache.Load(key); ok {
		return cached.([]chaos.RuntimeMutatorTargetData), nil
	}

	metas, err := s.listSystemMetadata(system, metadataTypeRuntimeMutatorTargets)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime mutator targets: %w", err)
	}
	result := make([]chaos.RuntimeMutatorTargetData, 0)
	for _, meta := range metas {
		var targets []chaos.RuntimeMutatorTargetData
		if err := json.Unmarshal([]byte(meta.Data), &targets); err != nil {
			return nil, fmt.Errorf("failed to parse runtime mutator target data: %w", err)
		}
		result = append(result, targets...)
	}

	s.cache.Store(key, result)
	return result, nil
}

// InvalidateCache clears the entire cache. Call after metadata updates.
func (s *DBMetadataStore) InvalidateCache() {
	s.cache.Range(func(key, _ interface{}) bool {
		s.cache.Delete(key)
		return true
	})
}

func (s *DBMetadataStore) getSystemMetadata(systemName, metadataType, serviceName string) (*model.SystemMetadata, error) {
	var meta model.SystemMetadata
	if err := s.db.Where("system_name = ? AND metadata_type IN ? AND service_name = ?", systemName, metadataTypeCandidates(metadataType), serviceName).
		Order("updated_at DESC").
		First(&meta).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get system metadata: %w", err)
	}
	return &meta, nil
}

func (s *DBMetadataStore) listSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error) {
	var metas []model.SystemMetadata
	query := s.db.Where("system_name = ?", systemName)
	if metadataType != "" {
		query = query.Where("metadata_type IN ?", metadataTypeCandidates(metadataType))
	}
	if err := query.Find(&metas).Error; err != nil {
		return nil, fmt.Errorf("failed to list system metadata: %w", err)
	}
	return metas, nil
}

func (s *DBMetadataStore) listServiceNames(systemName, metadataType string) ([]string, error) {
	var names []string
	query := s.db.Model(&model.SystemMetadata{}).Where("system_name = ?", systemName)
	if metadataType != "" {
		query = query.Where("metadata_type IN ?", metadataTypeCandidates(metadataType))
	}
	if err := query.Distinct("service_name").Pluck("service_name", &names).Error; err != nil {
		return nil, fmt.Errorf("failed to list service names: %w", err)
	}
	return names, nil
}
