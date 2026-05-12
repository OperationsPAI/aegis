package ratelimiter

import "context"

// HandlerService captures the rate-limiter operations consumed by the HTTP handler.
type HandlerService interface {
	GC(context.Context) (int, int, error)
	List(context.Context) (*RateLimiterListResp, error)
	Reset(context.Context, string) error
}

func AsHandlerService(service *Service) HandlerService {
	return service
}
