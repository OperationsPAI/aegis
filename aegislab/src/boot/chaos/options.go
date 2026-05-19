// Package chaosapp hosts the fx composition for the standalone
// aegis-chaos microservice. Mirrors boot/blob/options.go.
//
// What's in this binary:
//   - crud/chaos      — catalog, executor, HTTP handler, migrations
//   - infra           — db (MySQL), redis (unused yet, reserved for §12.1)
//   - http server     — exposes /v1beta + /healthz
//
// What's NOT in this binary:
//   - duckdb / arrow  — aegis-chaos has no parquet path
//   - chaos-experiment — explicitly deleted in §11 step 6; this binary
//     never imports it
package chaos

import (
	"strings"

	app "aegis/boot"
	chaos "aegis/crud/chaos"
	httpapi "aegis/boot/wiring/http"
	"aegis/platform/router"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.DataOptions(),
		app.WithRemoteVerifier(),

		chaos.Module,

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
