// Package configcenter hosts the fx composition for the standalone
// configuration-center microservice. Mirrors `app/notify/options.go`
// in shape.
//
// What's in this binary:
//
//   - module/configcenter — Center + audit + admin HTTP surface
//   - module/auth         — JWT verifier (service tokens + human users)
//   - module/user         — actor lookups for audit rows
//   - infra/etcd          — this is the ONLY binary that holds etcd
//                           write credentials in v1
//   - http server         — admin endpoints under /api/v2/config
//
// What's NOT in this binary: chaos/k8s/dataset/injection/business
// modules. This binary's job is "be the typed face of etcd".
package configcenter

import (
	"strings"

	"aegis/app"
	"aegis/platform/etcd"
	httpapi "aegis/boot/wiring/http"
	"aegis/crud/admin/configcenter"
	"aegis/clients/sso"
	"aegis/crud/iam/user"
	"aegis/platform/router"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),

		etcd.Module,
		user.Module,
		// Verify-only binary: no WithSigner. ssoclient brings
		// TokenVerifier + PermissionChecker; WithRemoteVerifier supplies
		// the JWKS-backed *Verifier ssoclient depends on.
		app.WithRemoteVerifier(),
		ssoclient.Module,
		configcenter.Module,

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
	engine.GET("/readyz", func(c *gin.Context) {
		// TODO: probe etcd reachability + last-successful-watch
		// timestamp. v1 stub returns ok so kube-readiness can wire up.
		c.JSON(200, gin.H{"status": "ok"})
	})
	return engine
}

func normalizeAddr(port string) string {
	if port == "" {
		return ":8087"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
