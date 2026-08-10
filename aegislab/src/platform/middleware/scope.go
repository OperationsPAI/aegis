package middleware

import (
	"net/http"
	"sync"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const emptyScopeWarnWindow = time.Minute

var (
	emptyScopeWarnMu   sync.Mutex
	emptyScopeWarnLast = map[string]time.Time{}
)

// RequireScope enforces a single OIDC-style scope on the authenticated
// principal. Behaviour:
//
//   - no authenticated principal → 401
//   - principal has the scope → pass
//   - principal has a non-empty scope set missing the required scope → 403
//   - principal has an empty scope set → WARN-log and pass (transient
//     back-compat shim until SA tokens always carry scopes; tracked by
//     the C3-C5 service-account work).
func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAuthenticated(c) {
			dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized")
			c.Abort()
			return
		}

		scopes := currentScopes(c)
		if len(scopes) == 0 {
			if shouldLogEmptyScope(principalIdent(c)) {
				logrus.WithFields(logrus.Fields{
					"path":      c.Request.URL.Path,
					"principal": principalIdent(c),
					"required":  scope,
				}).Warn("scope check: principal has no scopes; accepting (transient back-compat)")
			}
			c.Next()
			return
		}

		for _, s := range scopes {
			if s == scope {
				c.Next()
				return
			}
		}

		dto.ErrorResponse(c, http.StatusForbidden, "missing required scope: "+scope)
		c.Abort()
	}
}

func isAuthenticated(c *gin.Context) bool {
	if IsServiceToken(c) {
		return true
	}
	if uid, ok := GetCurrentUserID(c); ok && uid > 0 {
		return true
	}
	return false
}

func currentScopes(c *gin.Context) []string {
	if v, ok := c.Get(consts.CtxKeyScopes); ok {
		if scopes, ok := v.([]string); ok {
			return scopes
		}
	}
	return nil
}

func shouldLogEmptyScope(principal string) bool {
	emptyScopeWarnMu.Lock()
	defer emptyScopeWarnMu.Unlock()
	now := time.Now()
	if prev, ok := emptyScopeWarnLast[principal]; ok && now.Sub(prev) < emptyScopeWarnWindow {
		return false
	}
	emptyScopeWarnLast[principal] = now
	return true
}

func principalIdent(c *gin.Context) string {
	if taskID, ok := GetServiceTaskID(c); ok && taskID != "" {
		return "service:" + taskID
	}
	if name, ok := GetCurrentUsername(c); ok && name != "" {
		return "user:" + name
	}
	return "unknown"
}
