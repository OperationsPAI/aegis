package configcenter

import (
	"aegis/platform/etcd"

	"go.uber.org/fx"
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

func newCenterWithLifecycle(lc fx.Lifecycle, gw *etcd.Gateway) (*defaultCenter, error) {
	c, err := New(gw)
	if err != nil {
		return nil, err
	}
	globalCenter = c
	lc.Append(fx.Hook{
		OnStart: c.Start,
		OnStop:  c.Stop,
	})
	return c, nil
}

func asCenter(c *defaultCenter) Center { return c }
func asPubSub(c *defaultCenter) PubSub { return c }
