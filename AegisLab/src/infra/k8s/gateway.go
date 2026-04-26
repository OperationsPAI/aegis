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
	appsv1 "k8s.io/api/apps/v1"
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
	k8sRestConfigErr error
	k8sClient        *kubernetes.Clientset
	k8sClientErr     error
	k8sDynamicClient *dynamic.DynamicClient
	k8sDynamicErr    error
	k8sController    *Controller

	k8sRestConfigOnce    sync.Once
	k8sClientOnce        sync.Once
	k8sDynamicClientOnce sync.Once
	controllerOnce       sync.Once
)

func NewGateway(controller *Controller) *Gateway {
	if controller == nil {
		// Best-effort lazy controller init. Errors here used to be
		// logrus.Fatalf-fatal via the underlying client construction
		// (issue #193); now they are surfaced via getK8sController's
		// error log and the Gateway can still serve methods that don't
		// touch the controller (e.g. NamespaceHasWorkload, EnsureNamespace).
		controller, _ = getK8sController()
	}
	return &Gateway{controller: controller}
}

// k8sClientNotAvailableErr formats the canonical error returned by request-path
// callers when the lazy-init Kubernetes client could not be constructed.
// Replaces the previous logrus.Fatalf-on-init behavior so a transient API
// failure does not crash the backend (issue #193).
func k8sClientNotAvailableErr(err error) error {
	if err == nil {
		return fmt.Errorf("kubernetes client not available")
	}
	return fmt.Errorf("kubernetes client not available: %w", err)
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

// CleanupNamespaceChaosResources reaps zombie chaos-mesh.org CRs in a single
// namespace before a helm restart. Best-effort; see
// CleanupNamespaceChaosResources for semantics. Callers should NOT fail the
// task on returned warnings — chaos-CR cleanup is advisory.
func (g *Gateway) CleanupNamespaceChaosResources(ctx context.Context, namespace string) (map[string]int, []error) {
	return CleanupNamespaceChaosResources(ctx, namespace)
}

// EnsureNamespace creates the namespace if it doesn't exist. Returns
// (created, err). Harmless on existing namespaces — AlreadyExists is treated
// as success. Breaks the submit→restart-pedestal chicken-and-egg: a first-run
// submit used to 500 with `namespaces "X" not found` because guided app
// resolution lists pods in a namespace that RestartPedestal hasn't created
// yet. See github issue #91 item 1 / #92 item 1.
func (g *Gateway) EnsureNamespace(ctx context.Context, name string) (bool, error) {
	client, err := getK8sClient()
	if err != nil {
		return false, k8sClientNotAvailableErr(err)
	}
	_, err = client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
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

// NamespaceHasWorkload reports whether the given namespace contains at least
// one pod (any phase). Used by the submit-time namespace allocator (#166) to
// distinguish "deployed slot, currently idle" from "registered count slot,
// no workload" — the latter can't satisfy guided BuildInjection because pod
// listing would return empty and "app X not found" would surface to the
// user. Callers treat (false, nil) as "skip this slot, try next".
func (g *Gateway) NamespaceHasWorkload(ctx context.Context, namespace string) (bool, error) {
	client, err := getK8sClient()
	if err != nil {
		return false, k8sClientNotAvailableErr(err)
	}
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("list pods in %q: %w", namespace, err)
	}
	return len(pods.Items) > 0, nil
}

func (g *Gateway) CheckHealth(ctx context.Context) error {
	if _, err := getK8sRestConfig(); err != nil {
		return fmt.Errorf("kubernetes config not available: %w", err)
	}
	client, err := getK8sClient()
	if err != nil {
		return k8sClientNotAvailableErr(err)
	}
	if _, err := getK8sDynamicClient(); err != nil {
		return fmt.Errorf("kubernetes dynamic client not available: %w", err)
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
	client, err := getK8sClient()
	if err != nil {
		return k8sClientNotAvailableErr(err)
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

// WaitNamespaceReady blocks until every workload (Deployment, StatefulSet,
// DaemonSet) in the namespace reports availableReplicas >= replicas AND no Job
// is in a Failed condition, OR until the timeout elapses. This is the
// workload-level (controller-level) readiness probe used by RestartPedestal
// after a non-blocking helm install (Wait=false) — it is more tolerant than
// `WaitForNamespacePodsReady` because:
//
//   - Pods that crash/restart while their init-container chain catches up
//     don't trip the gate as long as the parent controller eventually
//     reaches `availableReplicas == replicas`.
//   - Jobs that complete fast (loadgen Jobs that run their workload and exit
//     0) count as ready.
//   - StatefulSets coming up sequentially are tolerated because we only
//     require the final `availableReplicas` count, not "all pods ready right
//     now".
//
// A namespace with zero workloads is considered ready (no work to wait on).
// Logging is one-shot at start, one-shot on success, and one-shot on
// timeout — never per-poll.
func (g *Gateway) WaitNamespaceReady(ctx context.Context, namespace string, timeout time.Duration) error {
	client, err := getK8sClient()
	if err != nil {
		return k8sClientNotAvailableErr(err)
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}

	start := time.Now()
	logrus.Infof("waiting for namespace %s to become ready, timeout %s", namespace, timeout)

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastSummary string
	for {
		ready, summary, err := checkNamespaceWorkloadsReady(waitCtx, client, namespace)
		if err != nil {
			return fmt.Errorf("readiness probe error in namespace %q: %w", namespace, err)
		}
		lastSummary = summary
		if ready {
			logrus.Infof("namespace %s ready in %s", namespace, time.Since(start).Round(time.Second))
			return nil
		}

		select {
		case <-waitCtx.Done():
			elapsed := time.Since(start).Round(time.Second)
			logrus.Warnf("namespace %s readiness timeout after %s: %s", namespace, elapsed, lastSummary)
			return fmt.Errorf("namespace %q not ready after %s: %s", namespace, elapsed, lastSummary)
		case <-ticker.C:
		}
	}
}

// checkNamespaceWorkloadsReady performs one-shot readiness evaluation of
// every Deployment / StatefulSet / DaemonSet / Job in the namespace. Returns
// (ready, human-summary, error). A list-API failure is fatal; per-workload
// not-ready states are accumulated into the summary so the caller can log
// the stuck list on timeout.
func checkNamespaceWorkloadsReady(ctx context.Context, client kubernetes.Interface, namespace string) (bool, string, error) {
	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, "", fmt.Errorf("list deployments: %w", err)
	}
	statefulSets, err := client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, "", fmt.Errorf("list statefulsets: %w", err)
	}
	daemonSets, err := client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, "", fmt.Errorf("list daemonsets: %w", err)
	}
	jobs, err := client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, "", fmt.Errorf("list jobs: %w", err)
	}
	ready, summary := evaluateNamespaceWorkloadsReady(deployments.Items, statefulSets.Items, daemonSets.Items, jobs.Items)
	return ready, summary, nil
}

// evaluateNamespaceWorkloadsReady is the pure-function core of the
// namespace-ready check, kept separate so it can be unit-tested without a
// kubernetes API. Treats zero workloads as ready (defensive: a "supposed to
// be empty" namespace shouldn't block forever).
func evaluateNamespaceWorkloadsReady(
	deployments []appsv1.Deployment,
	statefulSets []appsv1.StatefulSet,
	daemonSets []appsv1.DaemonSet,
	jobs []batchv1.Job,
) (bool, string) {
	stuck := make([]string, 0)

	for _, d := range deployments {
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		if d.Status.AvailableReplicas < desired {
			stuck = append(stuck, fmt.Sprintf("deployment/%s (%d/%d available)", d.Name, d.Status.AvailableReplicas, desired))
		}
	}
	for _, s := range statefulSets {
		desired := int32(1)
		if s.Spec.Replicas != nil {
			desired = *s.Spec.Replicas
		}
		// StatefulSet uses ReadyReplicas; AvailableReplicas exists since
		// k8s 1.22 but ReadyReplicas is the historical contract.
		ready := s.Status.ReadyReplicas
		if s.Status.AvailableReplicas > ready {
			ready = s.Status.AvailableReplicas
		}
		if ready < desired {
			stuck = append(stuck, fmt.Sprintf("statefulset/%s (%d/%d ready)", s.Name, ready, desired))
		}
	}
	for _, ds := range daemonSets {
		desired := ds.Status.DesiredNumberScheduled
		if ds.Status.NumberAvailable < desired {
			stuck = append(stuck, fmt.Sprintf("daemonset/%s (%d/%d available)", ds.Name, ds.Status.NumberAvailable, desired))
		}
	}
	for _, j := range jobs {
		// A Job is "ready" if it has completed (Succeeded) or is still
		// running. A Failed Job blocks readiness — that's a genuine error
		// the operator wants to know about.
		if isJobFailed(j) {
			stuck = append(stuck, fmt.Sprintf("job/%s (failed)", j.Name))
		}
	}

	totalWorkloads := len(deployments) + len(statefulSets) + len(daemonSets) + len(jobs)
	if totalWorkloads == 0 {
		// Defensive: an empty namespace is trivially ready. RestartPedestal's
		// flow won't hit this (helm install creates workloads), but keeping
		// the check explicit avoids a spurious timeout.
		return true, "no workloads in namespace"
	}
	if len(stuck) == 0 {
		return true, fmt.Sprintf("all %d workloads ready", totalWorkloads)
	}
	return false, fmt.Sprintf("%d/%d workloads not ready: %v", len(stuck), totalWorkloads, stuck)
}

func isJobFailed(j batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
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

// getK8sClient lazily constructs the kubernetes clientset. On construction
// failure it returns the error rather than calling logrus.Fatalf so request-
// path callers (e.g. NamespaceHasWorkload on the auto-allocate submit path)
// can surface a 5xx instead of crashing the backend process. See issue #193.
func getK8sClient() (*kubernetes.Clientset, error) {
	k8sClientOnce.Do(func() {
		restConfig, err := getK8sRestConfig()
		if err != nil {
			k8sClientErr = err
			return
		}
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			k8sClientErr = fmt.Errorf("failed to create Kubernetes clientset: %w", err)
			return
		}

		k8sClient = clientset
	})
	if k8sClientErr != nil {
		return nil, k8sClientErr
	}
	return k8sClient, nil
}

// getK8sDynamicClient lazily constructs the dynamic client. See getK8sClient
// for why errors are returned rather than fatal-logged.
func getK8sDynamicClient() (*dynamic.DynamicClient, error) {
	k8sDynamicClientOnce.Do(func() {
		restConfig, err := getK8sRestConfig()
		if err != nil {
			k8sDynamicErr = err
			return
		}
		dynamicClient, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			k8sDynamicErr = fmt.Errorf("failed to create Kubernetes dynamic client: %w", err)
			return
		}

		k8sDynamicClient = dynamicClient
	})
	if k8sDynamicErr != nil {
		return nil, k8sDynamicErr
	}
	return k8sDynamicClient, nil
}

// getK8sRestConfig lazily resolves the rest.Config (in-cluster preferred,
// kubeconfig fallback). Errors are returned rather than fatal-logged so
// callers can decide whether to fail the request or fail-fast at startup.
func getK8sRestConfig() (*rest.Config, error) {
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
			k8sRestConfigErr = fmt.Errorf("failed to load Kubernetes config (neither in-cluster nor kubeconfig %q available): %w", kubeconfig, err)
			return
		}
		if config == nil {
			k8sRestConfigErr = fmt.Errorf("failed to establish Kubernetes REST config: neither in-cluster nor external kubeconfig available")
			return
		}

		k8sRestConfig = config
	})
	if k8sRestConfigErr != nil {
		return nil, k8sRestConfigErr
	}
	return k8sRestConfig, nil
}

// k8sControllerErr captures a controller-init failure so repeated callers
// see the same error rather than re-running construction (issue #193).
var k8sControllerErr error

func getK8sController() (*Controller, error) {
	controllerOnce.Do(func() {
		ctrl, err := newController()
		if err != nil {
			k8sControllerErr = err
			return
		}
		k8sController = ctrl
	})
	if k8sControllerErr != nil {
		return nil, k8sControllerErr
	}
	return k8sController, nil
}
