package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// NormalizeAddr turns a port spec ("8080", ":8080", or "") into a listen
// address. Empty falls back to the supplied default (or ":8080" if none).
func NormalizeAddr(port string, defaults ...string) string {
	if port == "" {
		def := ":8080"
		if len(defaults) > 0 && defaults[0] != "" {
			def = defaults[0]
		}
		return def
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}

type healthzOptions struct {
	withReadyz bool
}

// HealthzOption tunes DecorateEngineWithHealthz.
type HealthzOption func(*healthzOptions)

// WithReadyz also installs a /readyz handler returning 200 ok. v1 stub —
// callers that need a real probe should wire their own handler instead.
func WithReadyz() HealthzOption {
	return func(o *healthzOptions) { o.withReadyz = true }
}

// DecorateEngineWithHealthz attaches /healthz (and optionally /readyz) to a
// gin engine. The verbose generic-handler signature is intentional: every
// caller historically supplied a *gin.Engine, so keep that to avoid
// re-plumbing fx.Decorate signatures.
func DecorateEngineWithHealthz(engine *gin.Engine, opts ...HealthzOption) *gin.Engine {
	cfg := healthzOptions{}
	for _, o := range opts {
		o(&cfg)
	}
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	if cfg.withReadyz {
		engine.GET("/readyz", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
	}
	return engine
}
