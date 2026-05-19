package chaosprune

import "context"

// HandlerService captures the chaos-prune operations consumed by the HTTP handler.
type HandlerService interface {
	Prune(ctx context.Context, req *PruneReq) (*PruneResp, error)
}

func AsHandlerService(svc *Service) HandlerService {
	return svc
}
