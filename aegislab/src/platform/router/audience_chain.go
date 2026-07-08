package router

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// audienceChain returns the canonical middleware chain for a registrar's
// declared audience. Public routes get an empty chain (the auth module
// owns its own gating). Portal / SDK / Admin all gate on TrustedHeaderAuth
// followed by JIT user provisioning for external IdP tokens — finer-grained
// permission checks (RequireSystemAdmin, RequireRoleRead, per-resource scope
// checks, ...) stay at the route or sub-group level because they vary by
// handler.
//
// Unknown audiences return nil; the router treats nil the same as the
// public empty-chain case.
func audienceChain(a framework.Audience, db *gorm.DB) []gin.HandlerFunc {
	switch a {
	case framework.AudiencePortal, framework.AudienceSDK, framework.AudienceAdmin:
		return []gin.HandlerFunc{
			middleware.TrustedHeaderAuth(),
			middleware.JITProvision(db),
		}
	case framework.AudiencePublic:
		return nil
	default:
		return nil
	}
}
