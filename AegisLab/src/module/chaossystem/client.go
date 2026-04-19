package chaossystem

import "aegis/model"

type Reader interface {
	GetSystemByID(int) (*model.System, error)
	ListSystemMetadata(systemName, metadataType string) ([]model.SystemMetadata, error)
}

func AsReader(repo *Repository) *Repository { return repo }

var _ Reader = (*Repository)(nil)
