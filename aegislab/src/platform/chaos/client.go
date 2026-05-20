package chaos

import (
	"fmt"
	"sync"

	chaosmeshv1alpha1 "github.com/chaos-mesh/chaos-mesh/api/v1alpha1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	k8sClientInstance client.Client
	mu                sync.RWMutex
)

// InitWithConfig initializes the chaos-mesh controller-runtime client from an
// external rest.Config. Called once during boot from the fx Initialize hook;
// repeated calls log a warning and replace the cached client.
func InitWithConfig(config *rest.Config) error {
	mu.Lock()
	defer mu.Unlock()

	if k8sClientInstance != nil {
		logrus.Warn("Kubernetes client already initialized, reinitializing with new config")
	}

	scheme := runtime.NewScheme()
	if err := chaosmeshv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add Chaos Mesh v1alpha1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add CoreV1 scheme: %v", err)
	}

	k8sClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	k8sClientInstance = k8sClient
	logrus.Info("Kubernetes client initialized with external config")
	return nil
}

// GetCRDMapping returns the chaos-mesh.org CRD GVR → typed object map used by
// the namespace reclaimer and chaos-CR prune utilities. Keep this list in sync
// with chaos-mesh's v1alpha1 API surface — adding a new kind here is what
// makes aegisctl `ns reset` reap it.
func GetCRDMapping() map[schema.GroupVersionResource]client.Object {
	return map[schema.GroupVersionResource]client.Object{
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "dnschaos"}:            &chaosmeshv1alpha1.DNSChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "httpchaos"}:           &chaosmeshv1alpha1.HTTPChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "jvmchaos"}:            &chaosmeshv1alpha1.JVMChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "networkchaos"}:        &chaosmeshv1alpha1.NetworkChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "podchaos"}:            &chaosmeshv1alpha1.PodChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "stresschaos"}:         &chaosmeshv1alpha1.StressChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "timechaos"}:           &chaosmeshv1alpha1.TimeChaos{},
		{Group: "chaos-mesh.org", Version: "v1alpha1", Resource: "runtimemutatorchaos"}: &chaosmeshv1alpha1.RuntimeMutatorChaos{},
	}
}
