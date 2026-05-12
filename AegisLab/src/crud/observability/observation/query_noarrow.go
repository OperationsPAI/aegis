//go:build !duckdb_arrow

package observation

import (
	"context"
	"errors"
)

var errNoArrow = errors.New("observation queries require building with -tags duckdb_arrow")

func (s *Service) GetMetricsCatalog(_ context.Context, _ int) (*MetricsCatalogResp, error) {
	return nil, errNoArrow
}

func (s *Service) GetMetricsSeries(_ context.Context, _ int, _ *MetricsSeriesReq) (*MetricsSeriesResp, error) {
	return nil, errNoArrow
}

func (s *Service) ListSpans(_ context.Context, _ int, _ *ListSpansReq) (*ListSpansResp, error) {
	return nil, errNoArrow
}

func (s *Service) GetSpanTree(_ context.Context, _ int, _ string) (*SpanTreeResp, error) {
	return nil, errNoArrow
}

func (s *Service) GetServiceMap(_ context.Context, _ int, _ *ServiceMapReq) (*ServiceMapResp, error) {
	return nil, errNoArrow
}
