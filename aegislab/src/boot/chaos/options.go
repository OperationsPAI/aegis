// Package chaos hosts the fx composition for the standalone aegis-chaos
// microservice. Mirrors boot/blob/options.go.
package chaos

import (
	"context"
	"strings"

	app "aegis/boot"
	httpapi "aegis/boot/wiring/http"
	ssoclient "aegis/clients/sso"
	"aegis/core/orchestrator/common"
	chaos "aegis/crud/chaos"
	"aegis/platform/consts"
	"aegis/platform/etcd"
	"aegis/platform/router"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),
		// etcd.Gateway is required by the global config-scope listener below;
		// DataOptions only wires db/redis, so pull in CoordinationOptions too.
		app.CoordinationOptions(),

		app.WithRemoteVerifier(),
		ssoclient.Module,

		chaos.Module,

		// The reconciler's completion webhook authenticates with a
		// chaos-service token. chaos has no signer (WithRemoteVerifier), so it
		// fetches + refreshes that token from SSO at runtime rather than
		// relying on the static (stale-format) CHAOS_SA_TOKEN env. Only the
		// standalone chaos service fires the webhook, so this lives here and
		// not in chaos.Module — the api/runtime boots wire chaos.Module without
		// ssoclient and would fail fx resolution on *ssoclient.Client.
		chaos.WebhookTokenModule,

		// CHAOS_INBOUND_BEARER (see crud/chaos/inbound_bearer.go) is the
		// canonical auth for /v1beta — the trusted-header path is only
		// used as a fall-through, so we don't gate boot on the gateway
		// HMAC key being configured.

		fx.Supply(&router.Handlers{}),
		fx.Supply(httpapi.ServerConfig{Addr: normalizeAddr(port)}),
		httpapi.Module,

		fx.Decorate(decorateEngineWithHealthz),

		// The guided resolver (crud/chaos/guided) reads system registrations
		// (ns_pattern / app_label_key) through systemconfig → Viper key
		// `injection.system.*`. Those values live in the Global config scope
		// and are only mirrored into Viper by ConfigUpdateListener; without
		// this the chaos service sees zero systems and every guided submit
		// fails with "system does not match any registered namespace pattern".
		// The api-gateway / runtime-worker get this via their seed boot; the
		// standalone chaos service did not until now.
		fx.Invoke(activateGlobalConfigScope),
	)
}

func activateGlobalConfigScope(lc fx.Lifecycle, db *gorm.DB, etcdGw *etcd.Gateway) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener := common.NewConfigUpdateListener(context.Background(), db, etcdGw)
			if err := listener.EnsureScope(consts.ConfigScopeGlobal); err != nil {
				logrus.WithError(err).Warn("aegis-chaos: failed to activate global config scope; guided resolver will see no systems")
			}
			// EnsureScope only watches the legacy /rcabench tree; `aegisctl etcd
			// put` writes the /aegis configcenter tree, so without the bridge a
			// runtime tune of rate_limiting.max_concurrent_injections never
			// reaches checkSystemCapacity's config.GetInt read.
			listener.EnsureConfigCenterBridge()
			return nil
		},
	})
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
