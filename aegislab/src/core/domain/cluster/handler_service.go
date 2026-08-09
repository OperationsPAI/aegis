package cluster

import "context"

// HandlerService captures the cluster-status operation consumed by the HTTP
// handler. Defined as an interface so tests can swap the production
// service for a fake without standing up the whole fx graph.
type HandlerService interface {
	GetClusterStatus(ctx context.Context) (*ClusterStatusResp, error)
}

func AsHandlerService(service *Service) HandlerService { return service }
