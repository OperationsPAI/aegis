// Package blob hosts the fx composition for the standalone blob
// microservice (aegis-blob). Mirrors `app/notify/options.go`.
//
// What's in this binary:
//
//   - module/blob   — driver registry, repository, handler, lifecycle
//   - module/auth   — JWT verifier (service & human-user tokens)
//   - infra (db, redis, jwtkeys)
//   - http server   — exposes only the blob routes + /healthz
//
// What's NOT in this binary: chaos/k8s/dataset/injection/business
// modules. Producers live elsewhere and reach this binary through
// module/blobclient with `mode = "remote"`.
package blob

import (
	"strings"

	"aegis/app"
	httpapi "aegis/boot/wiring/http"
	"aegis/crud/storage/blob"
	"aegis/clients/sso"
	"aegis/platform/router"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),

		// Verify-only binary: no WithSigner. ssoclient brings
		// TokenVerifier + PermissionChecker; WithRemoteVerifier supplies
		// the JWKS-backed *Verifier ssoclient depends on.
		app.WithRemoteVerifier(),
		ssoclient.Module,
		blob.Module,

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
		return ":8085"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
