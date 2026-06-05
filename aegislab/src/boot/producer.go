package app

import (
	"context"

	etcd "aegis/platform/etcd"
	redis "aegis/platform/redis"
	httpapi "aegis/boot/wiring/http"
	"aegis/clients/sso"
	commonservice "aegis/core/orchestrator/common"
	"aegis/boot/seed"
	"aegis/platform/utils"

	"go.uber.org/fx"
	"gorm.io/gorm"
)

func ProducerHTTPOptions(port string) fx.Option {
	return fx.Options(
		fx.Provide(newProducerInitializer),
		fx.Invoke(registerProducerInitialization),
		ProducerHTTPModules(),
		fx.Supply(httpapi.ServerConfig{Addr: httpapi.NormalizeAddr(port)}),
		ssoclient.Module,
		httpapi.Module,
	)
}

type ProducerInitializer struct {
	etcd      *etcd.Gateway
	redis     *redis.Gateway
	db        *gorm.DB
	StartFunc func(context.Context) error
}

func newProducerInitializer(etcd *etcd.Gateway, redis *redis.Gateway, db *gorm.DB) *ProducerInitializer {
	return &ProducerInitializer{etcd: etcd, redis: redis, db: db}
}

func (i *ProducerInitializer) start(ctx context.Context) error {
	if i.StartFunc != nil {
		return i.StartFunc(ctx)
	}
	if err := initialization.InitializeProducer(i.db, i.redis, i.etcd, commonservice.NewConfigUpdateListener(ctx, i.db, i.etcd)); err != nil {
		return err
	}
	utils.InitValidator()
	return nil
}

func registerProducerInitialization(lc fx.Lifecycle, initializer *ProducerInitializer) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return initializer.start(ctx)
		},
	})
}
