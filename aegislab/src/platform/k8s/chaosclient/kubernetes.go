package chaosclient

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	k8sClientInstance client.Client
	k8sConfigInstance *rest.Config
	once              sync.Once
	mu                sync.RWMutex
)

// InitWithConfig initializes the Kubernetes client with an external config.
// This is the recommended way when using this library - let the caller provide the config.
// This must be called before any other functions if you want to use a custom config.
func InitWithConfig(config *rest.Config) error {
	mu.Lock()
	defer mu.Unlock()

	if k8sClientInstance != nil {
		logrus.Warn("Kubernetes client already initialized, reinitializing with new config")
	}

	k8sConfigInstance = config
	scheme := runtime.NewScheme()

	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add CoreV1 scheme: %v", err)
	}

	// Create Kubernetes client
	k8sClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	k8sClientInstance = k8sClient
	logrus.Info("Kubernetes client initialized with external config")
	return nil
}

// InitWithClient initializes with an existing Kubernetes client.
// Useful when the caller already has a configured client.
func InitWithClient(k8sClient client.Client) {
	mu.Lock()
	defer mu.Unlock()

	if k8sClientInstance != nil {
		logrus.Warn("Kubernetes client already initialized, replacing with provided client")
	}

	k8sClientInstance = k8sClient
	logrus.Info("Kubernetes client initialized with external client")
}

// GetK8sConfig returns Kubernetes configuration
// It automatically detects whether running in-cluster or out-of-cluster
// Only used internally when InitWithConfig is not called
func GetK8sConfig() *rest.Config {
	mu.RLock()
	if k8sConfigInstance != nil {
		mu.RUnlock()
		return k8sConfigInstance
	}
	mu.RUnlock()

	// Try in-cluster config first (for pods running inside K8s)
	config, err := rest.InClusterConfig()
	if err == nil {
		logrus.Info("Using in-cluster Kubernetes config (ServiceAccount)")
		mu.Lock()
		k8sConfigInstance = config
		mu.Unlock()
		return config
	}

	// Fall back to kubeconfig file (for local development)
	logrus.Warn("In-cluster config not found, trying kubeconfig file")
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		logrus.Fatalf("Failed to load Kubernetes config: %v", err)
	}

	logrus.Info("Using kubeconfig from ~/.kube/config")
	mu.Lock()
	k8sConfigInstance = config
	mu.Unlock()
	return config
}

// GetK8sClient returns the Kubernetes client.
// If InitWithConfig or InitWithClient was called, it uses that client.
// Otherwise, it initializes a new client automatically (not recommended for library usage).
func GetK8sClient() client.Client {
	mu.RLock()
	if k8sClientInstance != nil {
		mu.RUnlock()
		return k8sClientInstance
	}
	mu.RUnlock()

	// Auto-initialize (fallback for backward compatibility)
	once.Do(func() {
		logrus.Warn("Auto-initializing Kubernetes client. Consider calling InitWithConfig() explicitly.")
		cfg := GetK8sConfig()
		if err := InitWithConfig(cfg); err != nil {
			logrus.Fatalf("Failed to auto-initialize Kubernetes client: %v", err)
		}
	})

	mu.RLock()
	defer mu.RUnlock()
	return k8sClientInstance
}

func ListNamespaces() ([]string, error) {
	var namespaceList corev1.NamespaceList
	if err := GetK8sClient().List(context.TODO(), &namespaceList); err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %v", err)
	}

	namespaces := make([]string, 0, len(namespaceList.Items))
	for _, item := range namespaceList.Items {
		namespaces = append(namespaces, item.Name)
	}

	return namespaces, nil
}

func GetLabels(ctx context.Context, namespace string, key string) ([]string, error) {
	labelValues := []string{}

	// List all pods in the specified namespace
	podList := &corev1.PodList{}
	err := GetK8sClient().List(ctx, podList, &client.ListOptions{
		Namespace: namespace,
	})
	if err != nil {
		fmt.Printf("failed to list pods in namespace %s: %v\n", namespace, err)
		return nil, err
	}

	for _, pod := range podList.Items {
		if value, exists := pod.Labels[key]; exists {
			labelValues = append(labelValues, value)
		}
	}
	if len(labelValues) == 0 {
		return nil, fmt.Errorf("no labels found for key %s in namespace %s", key, namespace)
	}

	slices.Sort(labelValues)
	labelValues = slices.Compact(labelValues)
	return labelValues, nil
}

// GetContainersWithAppLabel retrieves all containers along with their pod names and app labels
// in the specified namespace. appLabelKey lets the caller pick the label that identifies
// the app on each pod (e.g. "app" for TrainTicket, "app.kubernetes.io/name" for otel-demo);
// empty string falls back to "app" to preserve legacy behavior.
func GetContainersWithAppLabel(ctx context.Context, namespace, appLabelKey string) ([]map[string]string, error) {
	if appLabelKey == "" {
		appLabelKey = "app"
	}
	result := []map[string]string{}

	// List all pods in the specified namespace
	podList := &corev1.PodList{}
	if err := GetK8sClient().List(ctx, podList, &client.ListOptions{
		Namespace: namespace,
	}); err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace %s: %v", namespace, err)
	}

	for _, pod := range podList.Items {
		appLabel := pod.Labels[appLabelKey]

		// Add each container with its pod name and app label
		for _, container := range pod.Spec.Containers {
			containerInfo := map[string]string{
				"podName":       pod.Name,
				"appLabel":      appLabel,
				"containerName": container.Name,
			}
			result = append(result, containerInfo)
		}
	}

	return result, nil
}

func GetPodsByLabel(namespace, labelKey, labelValue string) ([]string, error) {
	pods := &corev1.PodList{}
	err := GetK8sClient().List(context.Background(), pods,
		client.InNamespace(namespace),
		client.MatchingLabels{labelKey: labelValue})
	if err != nil {
		return nil, err
	}

	podNames := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		podNames = append(podNames, pod.Name)
	}

	return podNames, nil
}

