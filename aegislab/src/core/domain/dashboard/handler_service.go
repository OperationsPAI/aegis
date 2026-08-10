package dashboard

import (
	"context"

	"aegis/platform/authz"
)

// HandlerService captures the dashboard aggregator operation consumed by the
// HTTP handler.
type HandlerService interface {
	GetProjectDashboard(context.Context, authz.CallerScope, int) (*DashboardResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
