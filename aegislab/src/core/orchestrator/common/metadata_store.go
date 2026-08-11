package common

import (
	"strings"
)

// ServiceTopologyData is the shape the chaossystem service writes into
// system_metadata.data when callers push topology updates via UpsertMetadata.
// chaos-service consumes the same column server-side on its own.
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

func normalizeMetadataType(metaType string) string {
	key := strings.ToLower(strings.TrimSpace(metaType))
	if normalized, ok := metadataTypeAliases[key]; ok {
		return normalized
	}
	return key
}

// NormalizeMetadataTypeForWrite canonicalises a user-facing metadata type
// string (e.g. "topology") to the form persisted in system_metadata
// (e.g. "service_topology"). Callers use it to keep write/read shapes aligned.
func NormalizeMetadataTypeForWrite(metaType string) string {
	return normalizeMetadataType(metaType)
}
