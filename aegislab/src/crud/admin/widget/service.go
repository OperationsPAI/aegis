package widget

import "context"

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Ping(context.Context) (*PingResp, error) {
	return &PingResp{
		Module:             "widget",
		RouteRegistered:    true,
		SelfRegisteredVia:  "fx value groups",
		FrameworkFilesEdit: false,
	}, nil
}
