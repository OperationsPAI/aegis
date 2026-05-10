package observation

import (
	"context"

	injection "aegis/module/injection"
)

type Service struct {
	injections injection.Reader
	store      *injection.DatapackStore
}

func NewService(injections injection.Reader, store *injection.DatapackStore) *Service {
	return &Service{injections: injections, store: store}
}

// HandlerService is the surface consumed by the HTTP handler.
type HandlerService interface {
	GetMetricsCatalog(ctx context.Context, id int) (*MetricsCatalogResp, error)
	GetMetricsSeries(ctx context.Context, id int, req *MetricsSeriesReq) (*MetricsSeriesResp, error)
	ListSpans(ctx context.Context, id int, req *ListSpansReq) (*ListSpansResp, error)
	GetSpanTree(ctx context.Context, id int, traceID string) (*SpanTreeResp, error)
	GetServiceMap(ctx context.Context, id int, req *ServiceMapReq) (*ServiceMapResp, error)
}

func AsHandlerService(s *Service) HandlerService { return s }

var _ HandlerService = (*Service)(nil)
