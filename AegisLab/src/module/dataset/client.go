package dataset

import (
	"aegis/dto"
	"aegis/model"
)

type Reader interface {
	ResolveDatasetVersions(refs []*dto.DatasetRef, userID int) (map[*dto.DatasetRef]model.DatasetVersion, error)
	ListInjectionsByDatasetVersionID(versionID int, includeLabels bool) ([]model.FaultInjection, error)
}

func AsReader(repo *Repository) Reader { return repo }

var _ Reader = (*Repository)(nil)
