package dataset

import (
	"aegis/platform/dto"
	"aegis/platform/model"
)

type Reader interface {
	ResolveDatasetVersions(refs []*dto.DatasetRef, userID int) (map[*dto.DatasetRef]model.DatasetVersion, error)
	ListInjectionsByDatasetVersionID(versionID int, includeLabels bool) ([]model.FaultInjection, error)
}

type Writer interface {
	CreateDatasetCore(dataset *model.Dataset, versions []model.DatasetVersion, userID int) (*model.Dataset, error)
}

func AsReader(repo *Repository) *Repository { return repo }
func AsWriter(repo *Repository) *Repository { return repo }

var _ Reader = (*Repository)(nil)
var _ Writer = (*Repository)(nil)
