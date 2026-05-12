// Package notify hosts the fx composition for the standalone
// notification microservice. Mirrors `app/sso/options.go` in shape.
//
// What's in this binary:
//
//   - module/notification         — full 6-role implementation
//   - module/auth                  — JWT verifier (so service tokens
//                                    from aegis-backend authenticate
//                                    against /api/v2/events:publish)
//   - module/user                  — sso adapter for actor/recipient lookups
//   - infra (db, redis, jwtkeys)
//   - http server                  — exposes only the notification routes
//
// What's NOT in this binary: chaos/k8s/dataset/injection/business
// modules. This binary's job is "accept events, route them, deliver
// them, host the inbox". Producers live elsewhere.
package notify

import (
	"strings"

	"aegis/app"
	httpapi "aegis/interface/http"
	"aegis/module/notification"
	"aegis/module/ssoclient"
	"aegis/module/user"
	"aegis/router"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),

		user.Module,
		// Verify-only binary: tokens are minted by aegis-sso, not here.
		// WithRemoteVerifier wires the JWKS-backed *Verifier; ssoclient
		// adds TokenVerifier + PermissionChecker on top of it.
		app.WithRemoteVerifier(),
		ssoclient.Module,
		notification.Module,

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
		return ":8084"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
