package widget

import "context"

type HandlerService interface {
	Ping(context.Context) (*PingResp, error)
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
