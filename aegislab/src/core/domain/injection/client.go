package injection

import (
	"context"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
)

// Reader exposes the injection-owned reads that other modules depend on.
type Reader interface {
	ResolveDatapacks(datapackName *string, datasetRef *dto.DatasetRef, userID int, taskType consts.TaskType) ([]model.FaultInjection, *int, error)
	GetReadyDatapackName(ctx context.Context, id int) (string, error)
}

// Writer exposes the injection-owned runtime writes that consumer code depends on.
type Writer interface {
	CreateInjectionRecord(context.Context, *RuntimeCreateInjectionReq) (*dto.InjectionItem, error)
	UpdateInjectionState(context.Context, *RuntimeUpdateInjectionStateReq) error
	UpdateInjectionTimestamps(context.Context, *RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error)
}

func AsReader(service *Service) Reader { return service }

func AsWriter(service *Service) Writer { return service }

var _ Reader = (*Service)(nil)
var _ Writer = (*Service)(nil)
