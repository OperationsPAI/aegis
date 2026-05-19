// Package chaos hosts the fx composition for the standalone aegis-chaos
// microservice. Mirrors boot/blob/options.go.
package chaos

import (
	"strings"

	app "aegis/boot"
	httpapi "aegis/boot/wiring/http"
	ssoclient "aegis/clients/sso"
	chaos "aegis/crud/chaos"
	"aegis/platform/middleware"
	"aegis/platform/router"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),

		app.WithRemoteVerifier(),
		ssoclient.Module,

		chaos.Module,

		fx.Invoke(func() { middleware.AssertTrustedHeaderKeyConfigured() }),

		fx.Supply(&router.Handlers{}),
		fx.Supply(httpapi.ServerConfig{Addr: normalizeAddr(port)}),
		httpapi.Module,

		fx.Decorate(decorateEngineWithHealthz),
	)
}

func decorateEngineWithHealthz(engine *gin.Engine) *gin.Engine {
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	return engine
}

func normalizeAddr(port string) string {
	if port == "" {
		return ":8086"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
