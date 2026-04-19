package injection

import (
	"context"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"
)

// Reader exposes the injection-owned reads that other modules depend on.
type Reader interface {
	ResolveDatapacks(datapackName *string, datasetRef *dto.DatasetRef, userID int, taskType consts.TaskType) ([]model.FaultInjection, *int, error)
}

// Writer exposes the injection-owned runtime writes that consumer code depends on.
type Writer interface {
	CreateInjectionRecord(context.Context, *RuntimeCreateInjectionReq) (*dto.InjectionItem, error)
	UpdateInjectionState(context.Context, *RuntimeUpdateInjectionStateReq) error
	UpdateInjectionTimestamps(context.Context, *RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error)
}

func AsReader(repo *Repository) Reader { return repo }

func AsWriter(service *Service) Writer { return service }

var _ Reader = (*Repository)(nil)
var _ Writer = (*Service)(nil)
