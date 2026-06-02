package configcenter

import (
	"context"

	"aegis/platform/etcd"
	"aegis/platform/model"

	"go.uber.org/fx"
	"gorm.io/gorm"
)

// Module wires the in-process configcenter: Center + AuditWriter +
// HTTP handler + routes + migrations. Loaded only by the standalone
// `aegis-configcenter` binary (and by configcenterclient's local
// mode in dev builds).
var Module = fx.Module("configcenter",
	fx.Provide(newCenterWithLifecycle),
	fx.Provide(asCenter),
	fx.Provide(asPubSub),
	fx.Provide(fx.Annotate(NewDBAuditWriter, fx.As(new(AuditWriter)))),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesAdmin, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)

// centerParams carries the configcenter dependencies. DB is optional so the
// module still builds in a DB-less deployment; when present it backs the
// dynamic_configs.default_value resolver layer.
type centerParams struct {
	fx.In

	LC fx.Lifecycle
	GW *etcd.Gateway
	DB *gorm.DB `optional:"true"`
}

func newCenterWithLifecycle(p centerParams) (*defaultCenter, error) {
	c, err := New(p.GW, dbDefaultProvider(p.DB))
	if err != nil {
		return nil, err
	}
	globalCenter = c
	p.LC.Append(fx.Hook{
		OnStart: c.Start,
		OnStop:  c.Stop,
	})
	return c, nil
}

// dbDefaultProvider returns a DefaultProvider backed by the dynamic_configs
// table, or nil when no DB is wired. The dotted config_key is namespace+"."+key.
func dbDefaultProvider(db *gorm.DB) DefaultProvider {
	if db == nil {
		return nil
	}
	return func(ctx context.Context, namespace, key string) (string, bool) {
		var cfg model.DynamicConfig
		err := db.WithContext(ctx).
			Select("default_value").
			Where("config_key = ?", namespace+"."+key).
			First(&cfg).Error
		if err != nil {
			return "", false
		}
		return cfg.DefaultValue, true
	}
}

func asCenter(c *defaultCenter) Center { return c }
func asPubSub(c *defaultCenter) PubSub { return c }
