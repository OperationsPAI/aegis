package task

import (
	"context"

	"aegis/platform/dto"
	"aegis/platform/model"

	"github.com/gorilla/websocket"
)

// HandlerService captures task operations consumed by HTTP handlers and gateway adapters.
type HandlerService interface {
	BatchDelete(context.Context, []string) error
	GetDetail(context.Context, string) (*dto.TaskDetailResp, error)
	List(context.Context, *ListTaskReq) (*dto.ListResp[dto.TaskResp], error)
	GetForLogStream(context.Context, string) (*model.Task, error)
	StreamLogs(context.Context, *websocket.Conn, *model.Task)
	Expedite(context.Context, string) (*dto.TaskResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
