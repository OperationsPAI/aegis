package dashboard

import "context"

// HandlerService captures the dashboard aggregator operation consumed by the
// HTTP handler.
type HandlerService interface {
	GetProjectDashboard(context.Context, int) (*DashboardResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
