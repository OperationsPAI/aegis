package trace

import (
	"context"
	"time"

	"aegis/platform/authz"
	"aegis/platform/dto"

	"github.com/redis/go-redis/v9"
)

// HandlerService captures trace operations consumed by HTTP handlers and gateway adapters.
type HandlerService interface {
	GetTrace(context.Context, authz.CallerScope, string) (*TraceDetailResp, error)
	GetTraceSpans(context.Context, authz.CallerScope, string) (*SpansResp, error)
	GetTraceLogs(context.Context, authz.CallerScope, string, *TraceLogQueryReq) (*TraceLogsResp, error)
	ListTraces(context.Context, authz.CallerScope, *ListTraceReq) (*dto.ListResp[TraceResp], error)
	GetTraceStreamProcessor(context.Context, authz.CallerScope, string) (*StreamProcessor, error)
	ReadTraceStreamMessages(context.Context, string, string, int64, time.Duration) ([]redis.XStream, error)
	CancelTrace(context.Context, authz.CallerScope, string) (*CancelTraceResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
