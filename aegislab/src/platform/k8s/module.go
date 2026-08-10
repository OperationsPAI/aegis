package k8s

import (
	"k8s.io/client-go/rest"

	"go.uber.org/fx"
)

var Module = fx.Module("k8s",
	fx.Provide(ProvideController),
	fx.Provide(NewGateway),
	fx.Provide(ProvideRestConfig),
)

// ProvideController is a fx provider for the lazy-initialized k8s controller.
// Returns an error so a transient API-server blip during startup surfaces as
// an fx wire-up failure rather than a logrus.Fatalf process kill (issue #193).
// fx will halt application startup on this error, which is the desired
// fail-fast behavior for genuinely-broken bootstrap.
func ProvideController() (*Controller, error) {
	return getK8sController()
}

// ProvideRestConfig is a fx provider for the lazy-initialized rest.Config.
// Returns an error for the same reason as ProvideController (issue #193).
func ProvideRestConfig() (*rest.Config, error) {
	return getK8sRestConfig()
}
