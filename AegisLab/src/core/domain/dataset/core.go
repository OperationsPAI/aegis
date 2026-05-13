package dataset

import (
	"aegis/platform/model"
)

func (r *Repository) CreateDatasetCore(dataset *model.Dataset, versions []model.DatasetVersion, userID int) (*model.Dataset, error) {
	service := NewService(r, NewFilesystemDatapackFileStore(), nil)
	return service.createDatasetCore(r, dataset, versions, userID)
}
