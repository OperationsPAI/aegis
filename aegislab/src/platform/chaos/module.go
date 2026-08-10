package chaos

import (
	"go.uber.org/fx"
	"k8s.io/client-go/rest"
)

var Module = fx.Module("chaos",
	fx.Invoke(Initialize),
)

func Initialize(restConfig *rest.Config) {
	InitWithConfig(restConfig)
}
