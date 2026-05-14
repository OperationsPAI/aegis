package httpapi

import (
	"aegis/platform/middleware"
	"aegis/platform/router"

	"go.uber.org/fx"
)

var Module = fx.Module("http",
	fx.Provide(
		middleware.NewService,
		router.New,
		NewServer,
	),
	fx.Invoke(registerServerLifecycle),
)
