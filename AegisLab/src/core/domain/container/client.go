package container

import (
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
)

type Reader interface {
	ListContainerVersionEnvVars([]dto.ParameterSpec, *model.ContainerVersion) ([]dto.ParameterItem, error)
	ListHelmConfigValues([]dto.ParameterSpec, *model.HelmConfig) ([]dto.ParameterItem, error)
	ResolveContainerVersions([]*dto.ContainerRef, consts.ContainerType, int) (map[*dto.ContainerRef]model.ContainerVersion, error)
}

type Writer interface {
	CreateContainerCore(*model.Container, int) (*model.Container, error)
	UploadHelmValueFileFromPath(string, *model.HelmConfig, string) error
}

func AsReader(repo *Repository) *Repository { return repo }

func AsWriter(repo *Repository) *Repository { return repo }

var _ Reader = (*Repository)(nil)
var _ Writer = (*Repository)(nil)
