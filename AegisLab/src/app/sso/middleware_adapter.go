package sso

import (
	"context"

	"aegis/platform/middleware"
	"aegis/module/rbac"
)

// ssoLocalPermissionChecker plugs rbac.Repository into the request-time
// middleware in the SSO process itself, mirroring the same surface that
// AegisLab's ssoclient adapter implements over HTTP.
func ssoLocalPermissionChecker(repo *rbac.Repository) middleware.PermissionChecker {
	return localChecker{repo: repo}
}

type localChecker struct{ repo *rbac.Repository }

func (l localChecker) Check(_ context.Context, userID int, permission, scopeType, scopeID string) (bool, error) {
	allowed, _, err := l.repo.CheckPermission(userID, permission, scopeType, scopeID)
	return allowed, err
}
