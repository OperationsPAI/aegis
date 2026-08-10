package auth

import "aegis/platform/middleware"

func NewTokenVerifier(service *Service) middleware.TokenVerifier {
	return service
}
