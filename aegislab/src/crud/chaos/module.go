package chaos

import (
	"context"
	"fmt"

	"go.uber.org/fx"
	"gorm.io/gorm"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Module wires the aegis-chaos service: DB migrations + seed, the
// Chaos-Mesh executor, the Manager facade, the HTTP handler/routes.
var Module = fx.Module("chaos",
	fx.Provide(NewDynamicClient),
	fx.Provide(NewExecutor),
	fx.Provide(NewManager),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(Routes, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	fx.Invoke(seedCapabilitiesOnStart),
)

// NewExecutor returns the Chaos-Mesh executor with the supplied dynamic
// client. The Executor interface is satisfied by *ChaosMeshExecutor.
func NewExecutor(dyn dynamic.Interface) Executor {
	return NewChaosMeshExecutor(dyn)
}

// NewDynamicClient builds a dynamic Kubernetes client from in-cluster
// config first (production) and falls back to ~/.kube/config (dev).
//
// Failure to construct a client at startup is fatal — aegis-chaos has no
// useful surface without one.
func NewDynamicClient() (dynamic.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		loader := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("chaos: build kube config: %w", err)
		}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("chaos: build dynamic client: %w", err)
	}
	return dyn, nil
}

func seedCapabilitiesOnStart(lc fx.Lifecycle, db *gorm.DB) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return SeedCapabilities(db.WithContext(ctx))
		},
	})
}
