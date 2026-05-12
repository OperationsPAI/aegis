// Package gateway hosts the fx composition for the L7 application
// gateway binary (cmd/aegis-gateway). Mirrors app/notify/options.go in
// shape.
//
// What's in this binary:
//
//   - module/gateway       — route matching, proxy, middleware chain
//   - module/ssoclient     — JWT verifier for pre-auth
//   - infra (config, logger, tracing, loki, jwtkeys)
//   - http server          — root handler is the gateway dispatcher
//
// What's NOT in this binary: DB, redis, k8s, chaos, any business
// module. The gateway is a transport-layer policy point; everything
// stateful lives downstream.
package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"aegis/app"
	gatewaymod "aegis/module/gateway"
	"aegis/module/ssoclient"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// Options builds the aegis-gateway runtime.
func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),

		ssoclient.Module,
		gatewaymod.Module,

		fx.Supply(serverConfig{Addr: normalizeAddr(port)}),
		fx.Provide(newServer),
		fx.Invoke(registerServerLifecycle),
	)
}

type serverConfig struct {
	Addr string
}

func newServer(cfg serverConfig, h *gatewaymod.Handler) *http.Server {
	return &http.Server{
		Addr:    cfg.Addr,
		Handler: h,
	}
}

func registerServerLifecycle(lc fx.Lifecycle, server *http.Server) {
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go func() {
				logrus.Infof("aegis-gateway: listening on %s", server.Addr)
				if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logrus.Errorf("aegis-gateway: server error: %v", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logrus.Info("aegis-gateway: shutting down")
			return server.Shutdown(ctx)
		},
	})
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
