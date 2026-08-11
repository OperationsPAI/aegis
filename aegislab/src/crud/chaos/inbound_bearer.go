package chaos

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const InboundBearerEnv = "CHAOS_INBOUND_BEARER"

// RequireBearerEnv toggles fail-closed behaviour when InboundBearerEnv is
// unset. Defaults to true (fail-closed); local-dev/kind sets it to "false".
const RequireBearerEnv = "CHAOS_REQUIRE_BEARER"

// staticBearerScopes is the scope set granted to requests that authenticate
// via the cluster-internal static bearer. Until SA tokens carry per-token
// scopes (C3-C5), this is what RequireScope actually checks against.
var staticBearerScopes = []string{
	"chaos.inject.write",
	"chaos.inject.read",
	"chaos.webhook.write",
}

// NewChaosAuthFromEnv composes the §11 step-5b R1 inbound auth chain:
//
//   - if CHAOS_INBOUND_BEARER is set and the request matches it, accept
//     immediately as a service token (short-circuit TrustedHeaderAuth);
//   - otherwise delegate to TrustedHeaderAuth for the existing
//     gateway-HMAC + JWT-fallthrough paths;
//   - if the env is unset, behave exactly like TrustedHeaderAuth alone
//     (kind dev path) and emit a single startup WARN.
func NewChaosAuthFromEnv() gin.HandlerFunc {
	return newChaosAuth(
		os.Getenv(InboundBearerEnv),
		requireBearerFromEnv(os.Getenv(RequireBearerEnv)),
		middleware.TrustedHeaderAuth(),
		logrus.StandardLogger(),
	)
}

// requireBearerFromEnv interprets the CHAOS_REQUIRE_BEARER env. Unset or any
// value other than a recognised falsy string is treated as true (fail-closed
// is the default).
func requireBearerFromEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// newChaosAuth is the testable seam. `fallback` is the middleware run when
// the static bearer is absent or doesn't match (production: TrustedHeaderAuth).
func newChaosAuth(token string, requireBearer bool, fallback gin.HandlerFunc, logger *logrus.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	var bootWarnOnce sync.Once
	if token == "" {
		if requireBearer {
			bootWarnOnce.Do(func() {
				logger.Errorf("chaos inbound bearer: %s unset but %s=true; all /v1beta requests will be rejected with 401", InboundBearerEnv, RequireBearerEnv)
			})
			return func(c *gin.Context) {
				dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: inbound bearer not configured")
				c.Abort()
			}
		}
		bootWarnOnce.Do(func() {
			logger.Warnf("chaos inbound bearer: %s unset and %s=false; /v1beta endpoints run open (TrustedHeaderAuth only)", InboundBearerEnv, RequireBearerEnv)
		})
		return fallback
	}
	expected := []byte("Bearer " + token)
	mismatchLog := newRateLimitedWarn(time.Minute)
	return func(c *gin.Context) {
		got := c.GetHeader("Authorization")
		if got != "" && subtle.ConstantTimeCompare([]byte(got), expected) == 1 {
			c.Set(consts.CtxKeyIsServiceToken, true)
			c.Set(consts.CtxKeyTokenType, "service")
			c.Set(consts.CtxKeyScopes, staticBearerScopes)
			c.Next()
			return
		}
		if got != "" {
			mismatchLog(logger, clientIP(c), c.Request.URL.Path)
		}
		fallback(c)
	}
}

// newRateLimitedWarn returns a closure that logs at most one bearer-mismatch
// WARN per (remote, path) tuple per `window`. A curl loop with a wrong token
// otherwise floods logs and drowns out real signal.
func newRateLimitedWarn(window time.Duration) func(*logrus.Logger, string, string) {
	var mu sync.Mutex
	last := make(map[string]time.Time)
	return func(logger *logrus.Logger, remote, path string) {
		key := remote + "|" + path
		mu.Lock()
		now := time.Now()
		if prev, ok := last[key]; ok && now.Sub(prev) < window {
			mu.Unlock()
			return
		}
		last[key] = now
		mu.Unlock()
		logger.WithFields(logrus.Fields{
			"path":   path,
			"remote": remote,
		}).Warn("chaos inbound bearer: presented bearer did not match; falling back to TrustedHeaderAuth")
	}
}

// rejectingFallback is used in tests where we want to assert the chain
// rejects rather than delegate to a real TrustedHeaderAuth.
func rejectingFallback(c *gin.Context) {
	dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized")
	c.Abort()
}

func clientIP(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	addr := c.Request.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}
