package chaos

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"sync"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// InboundBearerEnv is the env var read at boot by NewChaosAuthFromEnv.
// Kept separate from CHAOS_WEBHOOK_BEARER (which is OUTBOUND from chaos →
// backend): the two directions need independent rotation.
const InboundBearerEnv = "CHAOS_INBOUND_BEARER"

// RequireBearerEnv toggles fail-closed behaviour when InboundBearerEnv is
// unset. Defaults to true (fail-closed); local-dev/kind sets it to "false"
// to fall through to TrustedHeaderAuth only.
const RequireBearerEnv = "CHAOS_REQUIRE_BEARER"

var (
	inboundUnsetWarnOnce sync.Once
	requireBearerWarnOnce sync.Once
)

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
	if token == "" {
		if requireBearer {
			requireBearerWarnOnce.Do(func() {
				logger.Errorf("chaos inbound bearer: %s unset but %s=true; all /v1beta requests will be rejected with 401", InboundBearerEnv, RequireBearerEnv)
			})
			return func(c *gin.Context) {
				dto.ErrorResponse(c, http.StatusUnauthorized, "Unauthorized: inbound bearer not configured")
				c.Abort()
			}
		}
		inboundUnsetWarnOnce.Do(func() {
			logger.Warnf("chaos inbound bearer: %s unset and %s=false; /v1beta endpoints run open (TrustedHeaderAuth only)", InboundBearerEnv, RequireBearerEnv)
		})
		return fallback
	}
	expected := []byte("Bearer " + token)
	return func(c *gin.Context) {
		got := c.GetHeader("Authorization")
		if got != "" && subtle.ConstantTimeCompare([]byte(got), expected) == 1 {
			// Static service bearer accepted — populate the same context
			// markers TrustedHeaderAuth would have set for a service token
			// so downstream handlers see a coherent caller scope.
			c.Set(consts.CtxKeyIsServiceToken, true)
			c.Set(consts.CtxKeyTokenType, "service")
			c.Next()
			return
		}
		// No bearer or wrong bearer → fall through to existing auth chain.
		// TrustedHeaderAuth itself emits 401 on its own terms; we only log
		// when there was a bearer attempt that didn't match, since a bare
		// gateway-HMAC request has no Authorization header at all.
		if got != "" {
			logger.WithFields(logrus.Fields{
				"path":   c.Request.URL.Path,
				"remote": clientIP(c),
			}).Warn("chaos inbound bearer: presented bearer did not match; falling back to TrustedHeaderAuth")
		}
		fallback(c)
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
