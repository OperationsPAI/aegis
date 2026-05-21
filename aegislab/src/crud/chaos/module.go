package chaos

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var Module = fx.Module("chaos",
	fx.Provide(NewDynamicClient),
	fx.Provide(NewExecutor),
	fx.Provide(NewManager),
	fx.Provide(NewHandler),
	fx.Provide(NewWebhookSenderFromEnv),
	fx.Provide(NewReconcilerDefault),
	fx.Provide(
		fx.Annotate(Routes, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	fx.Invoke(seedCapabilitiesOnStart),
	fx.Invoke(runReconciler),
	fx.Invoke(registerChaosPointStoreOnStart),
)

// registerChaosPointStoreOnStart installs the DB-backed chaos_points reader
// into chaos-experiment's resourcelookup package at boot, so guided walks
// and groundtruth lookups source from chaos_points instead of the legacy
// internal/<sys>/* hardcoded maps. Phase A4.
func registerChaosPointStoreOnStart(db *gorm.DB) {
	RegisterChaosPointStore(db)
}

// Read directly from the process env rather than via config.GetString:
// the chart wires these as bare env vars (helm/charts/chaos/templates/
// deployment.yaml + chaosAuth secret refs) so operators can rotate the
// bearer with kubectl-restart alone, without re-rendering the ConfigMap.
func NewWebhookSenderFromEnv(db *gorm.DB) *WebhookSender {
	logger := logrus.StandardLogger()
	w := NewWebhookSender(&http.Client{Timeout: 60 * time.Second}, os.Getenv("CHAOS_BACKEND_URL"), db, logger)
	if tok := os.Getenv("CHAOS_SA_TOKEN"); tok != "" {
		w.SetBearer(tok)
		return w
	}
	if tok := os.Getenv("CHAOS_WEBHOOK_BEARER"); tok != "" {
		logger.Error("DEPRECATED: CHAOS_WEBHOOK_BEARER fallback active; provision CHAOS_SA_TOKEN via rcabench-chaos-sa Secret")
		w.SetBearer(tok)
	}
	return w
}

func NewReconcilerDefault(db *gorm.DB, exec Executor, webhook *WebhookSender) *Reconciler {
	return NewReconciler(db, exec, webhook, 5*time.Second, logrus.StandardLogger())
}

func runReconciler(lc fx.Lifecycle, r *Reconciler) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go r.Run(ctx)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			return nil
		},
	})
}

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
