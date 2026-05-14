package container

import (
	"aegis/platform/model"
)

func (r *Repository) CreateContainerCore(container *model.Container, userID int) (*model.Container, error) {
	service := NewService(r, NewBuildGateway(), NewFilesystemHelmFileStore(), nil, nil)
	return service.createContainerCore(r, container, userID)
}

func (r *Repository) UploadHelmValueFileFromPath(containerName string, helmConfig *model.HelmConfig, srcFilePath string) error {
	store := NewFilesystemHelmFileStore()
	targetPath, err := store.SaveValueFile(containerName, nil, srcFilePath)
	if err != nil {
		return err
	}

	helmConfig.ValueFile = targetPath
	return r.updateHelmConfig(helmConfig)
}
