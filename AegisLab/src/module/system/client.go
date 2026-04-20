package system

import "aegis/model"

// Reader is the forward-only cross-module contract for system-owned data.
// Post-issue-75 the "System" aggregate lives in etcd, so only the
// system_metadata helpers remain.
type Reader interface {
	GetSystemMetadata(systemName, metadataType, serviceName string) (*model.SystemMetadata, error)
	ListSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error)
	ListSystemMetadataServiceNames(systemName, metadataType string) ([]string, error)
}
