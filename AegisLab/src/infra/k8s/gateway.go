package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"aegis/consts"

	chaosCli "github.com/OperationsPAI/chaos-experiment/client"
	"github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Gateway struct {
	controller *Controller
}

var (
	k8sRestConfig    *rest.Config
	k8sClient        *kubernetes.Clientset
	k8sDynamicClient *dynamic.DynamicClient
	k8sController    *Controller

	k8sRestConfigOnce    sync.Once
	k8sClientOnce        sync.Once
	k8sDynamicClientOnce sync.Once
	controllerOnce       sync.Once
)

func NewGateway(controller *Controller) *Gateway {
	if controller == nil {
		controller = getK8sController()
	}
	return &Gateway{controller: controller}
}

func (g *Gateway) GetVolumeMountConfigMap() (map[consts.VolumeMountName]VolumeMountConfig, error) {
	return getVolumeMountConfigMap()
}

func (g *Gateway) CreateJob(ctx context.Context, jobConfig *JobConfig) error {
	return createJob(ctx, jobConfig)
}

func (g *Gateway) GetJob(ctx context.Context, namespace, jobName string) (*batchv1.Job, error) {
	return getJob(ctx, namespace, jobName)
}

func (g *Gateway) WaitForJobCompletion(ctx context.Context, namespace, jobName string) error {
	return waitForJobCompletion(ctx, namespace, jobName)
}

func (g *Gateway) GetJobPodLogs(ctx context.Context, namespace, jobName string) (map[string][]string, error) {
	return getJobPodLogs(ctx, namespace, jobName)
}

func (g *Gateway) DeleteJob(ctx context.Context, namespace, jobName string) error {
	return deleteJob(ctx, namespace, jobName)
}

// DeleteChaosCRDsByLabel scans every registered chaos CRD kind and deletes
// objects matching `labelKey=labelValue` across all namespaces. See
// DeleteChaosCRDsByLabel for semantics. Failures on individual CRDs are
// surfaced as warnings, not fatal errors.
func (g *Gateway) DeleteChaosCRDsByLabel(ctx context.Context, labelKey, labelValue string) ([]DeletedCRD, []error) {
	chaosGVRs := make([]schema.GroupVersionResource, 0, len(chaosCli.GetCRDMapping()))
	for gvr := range chaosCli.GetCRDMapping() {
		chaosGVRs = append(chaosGVRs, gvr)
	}
	return DeleteChaosCRDsByLabel(ctx, chaosGVRs, labelKey, labelValue)
}

// EnsureNamespace creates the namespace if it doesn't exist. Returns
// (created, err). Harmless on existing namespaces — AlreadyExists is treated
// as success. Breaks the submit→restart-pedestal chicken-and-egg: a first-run
// submit used to 500 with `namespaces "X" not found` because guided app
// resolution lists pods in a namespace that RestartPedestal hasn't created
// yet. See github issue #91 item 1 / #92 item 1.
func (g *Gateway) EnsureNamespace(ctx context.Context, name string) (bool, error) {
	client := getK8sClient()
	if client == nil {
		return false, fmt.Errorf("kubernetes client not available")
	}
	_, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return false, nil
	}
	if !k8serrors.IsNotFound(err) {
		return false, fmt.Errorf("check namespace %q: %w", name, err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{"app.kubernetes.io/managed-by": "aegis"},
	}}
	if _, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, fmt.Errorf("create namespace %q: %w", name, err)
	}
	return true, nil
}

func (g *Gateway) CheckHealth(ctx context.Context) error {
	if getK8sRestConfig() == nil {
		return fmt.Errorf("kubernetes config not available")
	}
	client := getK8sClient()
	if client == nil {
		return fmt.Errorf("kubernetes client not available")
	}
	if getK8sDynamicClient() == nil {
		return fmt.Errorf("kubernetes dynamic client not available")
	}

	if _, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		return fmt.Errorf("kubernetes API request failed: %w", err)
	}
	return nil
}

// WaitForNamespacePodsReady blocks until every active pod in the namespace is
// Ready. "Active" means phase Pending/Running/Unknown (Succeeded/Failed pods
// are ignored). The check requires at least one active pod to avoid a false
// positive immediately after a helm release returns.
func (g *Gateway) WaitForNamespacePodsReady(ctx context.Context, namespace string, timeout time.Duration) error {
	client := getK8sClient()
	if client == nil {
		return fmt.Errorf("kubernetes client not available")
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastSummary string
	for {
		podList, err := client.CoreV1().Pods(namespace).List(waitCtx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list pods in namespace %q: %w", namespace, err)
		}

		ready, summary := evaluateNamespacePodReadiness(podList.Items)
		lastSummary = summary
		if ready {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for pods ready in namespace %q timed out (%s): %w", namespace, lastSummary, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func evaluateNamespacePodReadiness(pods []corev1.Pod) (bool, string) {
	activeNames := make([]string, 0, len(pods))
	notReadyNames := make([]string, 0)

	for _, pod := range pods {
		switch pod.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			continue
		}

		activeNames = append(activeNames, pod.Name)
		if !isPodReady(pod.Status.Conditions) {
			notReadyNames = append(notReadyNames, pod.Name)
		}
	}

	if len(activeNames) == 0 {
		return false, "no active pods found yet"
	}
	if len(notReadyNames) > 0 {
		return false, fmt.Sprintf("%d/%d active pods not ready: %v", len(notReadyNames), len(activeNames), notReadyNames)
	}
	return true, fmt.Sprintf("all %d active pods are ready", len(activeNames))
}

func isPodReady(conditions []corev1.PodCondition) bool {
	for _, cond := range conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func getK8sClient() *kubernetes.Clientset {
	k8sClientOnce.Do(func() {
		restConfig := getK8sRestConfig()
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			logrus.Fatalf("failed to create Kubernetes clientset: %v", err)
		}

		k8sClient = clientset
	})
	return k8sClient
}

func getK8sDynamicClient() *dynamic.DynamicClient {
	k8sDynamicClientOnce.Do(func() {
		restConfig := getK8sRestConfig()
		dynamicClient, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			logrus.Fatalf("failed to create Kubernetes dynamic client: %v", err)
		}

		k8sDynamicClient = dynamicClient
	})
	return k8sDynamicClient
}

func getK8sRestConfig() *rest.Config {
	k8sRestConfigOnce.Do(func() {
		restConfig, err := rest.InClusterConfig()
		if err == nil {
			logrus.Info("Successfully loaded In-Cluster Kubernetes configuration.")
			k8sRestConfig = restConfig
			logrus.Infof("Using Kubernetes Context: %s", "In-Cluster")
			return
		}

		logrus.Warn("In-cluster config not found, trying kubeconfig file")
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			logrus.Fatalf("Failed to load Kubernetes config: %v", err)
		}
		if config == nil {
			logrus.Fatalf("Failed to establish Kubernetes REST config: Neither In-Cluster nor external Kubeconfig available.")
		}

		k8sRestConfig = config
	})
	return k8sRestConfig
}

func getK8sController() *Controller {
	controllerOnce.Do(func() {
		k8sController = newController()
	})
	return k8sController
}
