package router

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func audienceChain(a framework.Audience, prov middleware.UserProvisioner) []gin.HandlerFunc {
	switch a {
	case framework.AudiencePortal, framework.AudienceSDK, framework.AudienceAdmin:
		return []gin.HandlerFunc{
			middleware.TrustedHeaderAuth(),
			middleware.JITProvision(prov),
		}
	case framework.AudiencePublic:
		return nil
	default:
		return nil
	}
}
