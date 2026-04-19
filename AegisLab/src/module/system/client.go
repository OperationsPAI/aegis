package system

import "aegis/model"

// Reader is the forward-only cross-module contract for system-owned data.
// Phase 4 consumers will switch from direct table reads to this interface in
// their own PRs.
type Reader interface {
	GetSystemByID(id int) (*model.System, error)
	GetSystemMetadata(systemName, metadataType, serviceName string) (*model.SystemMetadata, error)
	ListSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error)
	ListSystemMetadataServiceNames(systemName, metadataType string) ([]string, error)
}

func AsReader(repo *Repository) Reader {
	return repo
}
