package chaossystem

import "aegis/model"

// Reader is the cross-module read contract for chaos-system metadata.
// Post-issue-75 the "system" aggregate lives in etcd/Viper, but
// system_metadata rows still live in MySQL so we expose them here for other
// modules that want to consume service-level metadata.
type Reader interface {
	ListSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error)
}

// AsReader returns the repository as a Reader.
func AsReader(repo *Repository) *Repository { return repo }

var _ Reader = (*Repository)(nil)
