package k8s

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"aegis/platform/config"
	"aegis/platform/consts"
	runtimeinfra "aegis/platform/runtime"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type ActionType string

const (
	jobKind      = "Job"
	resyncPeriod = 5 * time.Second

	DeleteJob ActionType = "DeleteJob"
)

type Callback interface {
	HandleJobAdd(name string, annotations map[string]string, labels map[string]string)
	HandleJobFailed(job *batchv1.Job, annotations map[string]string, labels map[string]string)
	HandleJobSucceeded(job *batchv1.Job, annotations map[string]string, labels map[string]string)
}

type QueueItem struct {
	Type      ActionType
	Namespace string
	Name      string
}

type Controller struct {
	callback         Callback
	activeNamespaces map[string]bool
	namespaceMu      sync.RWMutex
	jobInformer      cache.SharedIndexInformer
	podInformer      cache.SharedIndexInformer
	queue            workqueue.TypedRateLimitingInterface[QueueItem]
	ctx              context.Context
	cancelFunc       context.CancelFunc
}

func newController() (*Controller, error) {
	activeNamespaces := make(map[string]bool)

	tweakListOptions := func(options *metav1.ListOptions) {
		options.LabelSelector = fmt.Sprintf("%s=%s", consts.K8sLabelAppID, runtimeinfra.AppID())
	}

	client, err := getK8sClient()
	if err != nil {
		return nil, fmt.Errorf("kubernetes client not available: %w", err)
	}

	platformFactory := informers.NewSharedInformerFactoryWithOptions(
		client,
		resyncPeriod,
		informers.WithNamespace(config.GetString("k8s.namespace")),
		informers.WithTweakListOptions(tweakListOptions),
	)

	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[QueueItem](),
	)

	jobInformer := platformFactory.Batch().V1().Jobs().Informer()
	podInformer := platformFactory.Core().V1().Pods().Informer()

	return &Controller{
		activeNamespaces: activeNamespaces,
		jobInformer:      jobInformer,
		podInformer:      podInformer,
		queue:            queue,
	}, nil
}

func (c *Controller) Initialize(ctx context.Context, cancelFunc context.CancelFunc, callback Callback) {
	logrus.Info("Initializing controller...")

	c.ctx = ctx
	c.cancelFunc = cancelFunc
	c.callback = callback

	if err := c.startJobAndPodInformers(); err != nil {
		// Bootstrap-time failure: informers are required for the consumer
		// to observe Job/Pod/CRD events at all. Without them no workflow
		// callbacks fire and the process is effectively a no-op. We use
		// an explicit Errorf+os.Exit(1) (instead of logrus.Fatalf) so the
		// fail-fast intent is obvious in source per issue #193.
		logrus.Errorf("Bootstrap failure: failed to start Job and Pod informers: %v", err)
		os.Exit(1)
	}

	go c.startQueueWorker()

	logrus.Info("Controller initialized successfully")

	<-ctx.Done()
	c.stop()
	logrus.Info("Controller shutdown complete")
}

// AddNamespaceInformers marks the given namespaces as active so callers that
// previously relied on CRD informer creation get the same observable side
// effect (active set updated). The chaos CRD watcher was removed in §11 step
// 5c; chaos-service is now the sole inject path and emits its terminal status
// over the webhook receiver (crud/hooks/chaos).
func (c *Controller) AddNamespaceInformers(namespaces []string) error {
	if len(namespaces) == 0 {
		return nil
	}
	c.namespaceMu.Lock()
	defer c.namespaceMu.Unlock()
	for _, ns := range namespaces {
		c.activeNamespaces[ns] = true
	}
	return nil
}

// EnsureNamespaceActive marks the given namespace as active. Safe to call on
// a nil receiver and with an empty namespace.
func (c *Controller) EnsureNamespaceActive(namespace string) error {
	if c == nil || namespace == "" {
		return nil
	}
	return c.AddNamespaceInformers([]string{namespace})
}

// RemoveNamespaceInformers marks namespaces as inactive
// Note: Kubernetes informers cannot be gracefully stopped, so we keep them running
// but mark the namespaces as inactive to filter events in handlers
func (c *Controller) RemoveNamespaceInformers(namespaces []string) {
	if len(namespaces) == 0 {
		logrus.Debug("No namespaces to remove")
		return
	}

	logrus.Infof("Marking %d namespace(s) as inactive: %v", len(namespaces), namespaces)

	c.namespaceMu.Lock()
	defer c.namespaceMu.Unlock()

	deactivatedCount := 0
	for _, ns := range namespaces {
		wasActive := c.activeNamespaces[ns]
		c.activeNamespaces[ns] = false
		if wasActive {
			deactivatedCount++
			logrus.Infof("Namespace %s marked as inactive (events will be ignored)", ns)
		} else {
			logrus.Debugf("Namespace %s was already inactive", ns)
		}
	}

	logrus.Infof("Namespace deactivation completed: %d deactivated, %d already inactive",
		deactivatedCount, len(namespaces)-deactivatedCount)
}

// stop gracefully stops the controller and cancels all informers
func (c *Controller) stop() {
	logrus.Info("Stopping controller and cancelling all informers...")
	if c.cancelFunc != nil {
		c.cancelFunc()
	}
	c.queue.ShutDown()
}

// startJobAndPodInformers starts Job and Pod informers with event handlers
// It should be called after the controller is created but can be called at any time
func (c *Controller) startJobAndPodInformers() error {
	if c.callback == nil {
		return fmt.Errorf("callback must be set before registering Job and Pod informers")
	}

	logrus.Debug("Starting Job and Pod informers...")

	// Register event handlers
	if c.jobInformer != nil {
		if _, err := c.jobInformer.AddEventHandler(c.genJobEventHandlerFuncs()); err != nil {
			return fmt.Errorf("failed to add event handler for Job informer: %w", err)
		}
		logrus.Debug("Registered event handler for Job informer")

		// Start Job informer
		go c.jobInformer.Run(c.ctx.Done())
		logrus.Info("Started Job informer")
	} else {
		logrus.Warn("Job informer is nil, skipping registration")
	}

	if c.podInformer != nil {
		if _, err := c.podInformer.AddEventHandler(c.genPodEventHandlerFuncs()); err != nil {
			return fmt.Errorf("failed to add event handler for Pod informer: %w", err)
		}
		logrus.Debug("Registered event handler for Pod informer")

		// Start Pod informer
		go c.podInformer.Run(c.ctx.Done())
		logrus.Info("Started Pod informer")
	} else {
		logrus.Warn("Pod informer is nil, skipping registration")
	}

	// Wait for cache sync
	syncFuncs := []cache.InformerSynced{}
	if c.jobInformer != nil {
		syncFuncs = append(syncFuncs, c.jobInformer.HasSynced)
	}
	if c.podInformer != nil {
		syncFuncs = append(syncFuncs, c.podInformer.HasSynced)
	}

	if len(syncFuncs) > 0 {
		logrus.Debug("Waiting for Job and Pod informer caches to sync...")
		if !cache.WaitForCacheSync(c.ctx.Done(), syncFuncs...) {
			return fmt.Errorf("timed out waiting for Job and Pod informer caches to sync")
		}
		logrus.Info("Job and Pod informer caches synced successfully")
	}

	logrus.Info("Successfully registered and started Job and Pod informers")
	return nil
}

// startQueueWorker starts the queue worker for processing delayed tasks
func (c *Controller) startQueueWorker() {
	logrus.Info("Starting queue worker...")
	wait.Until(c.runWorker, time.Second, c.ctx.Done())
}

// genCRDEventHandlerFuncs generates event handler functions for a given CRD GVR

func jobHasTerminalCondition(job *batchv1.Job, want batchv1.JobConditionType) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == want && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// genJobEventHandlerFuncs generates event handler functions for Job informer
func (c *Controller) genJobEventHandlerFuncs() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if initialTime := runtimeinfra.InitialTime(); !initialTime.IsZero() {
				creationTime := obj.(*batchv1.Job).CreationTimestamp.Time
				if creationTime.Before(initialTime) {
					return
				}
			}

			job := obj.(*batchv1.Job)
			logrus.WithFields(logrus.Fields{
				"namespace": job.Namespace,
				"job_name":  job.Name,
				"task_type": job.Labels[consts.JobLabelTaskType],
			}).Info("job created successfully")
			c.callback.HandleJobAdd(job.Name, job.Annotations, job.Labels)
		},
		UpdateFunc: func(oldObj, newObj any) {
			oldJob := oldObj.(*batchv1.Job)
			newJob := newObj.(*batchv1.Job)

			if oldJob.Name != newJob.Name {
				return
			}

			// Use terminal-state conditions instead of Failed/Succeeded counter
			// edges: the counter-based predicate only fires on the exact
			// transition tick and is missed across informer resyncs, leaving
			// Failed Jobs un-reaped (24+ leaked pods in exp ns, up to 5d3h old).
			if !jobHasTerminalCondition(oldJob, batchv1.JobFailed) && jobHasTerminalCondition(newJob, batchv1.JobFailed) {
				c.callback.HandleJobFailed(newJob, newJob.Annotations, newJob.Labels)
				if !config.GetBool("debugging.enabled") {
					c.queue.Add(QueueItem{
						Type:      DeleteJob,
						Namespace: newJob.Namespace,
						Name:      newJob.Name,
					})
				}
			}

			if !jobHasTerminalCondition(oldJob, batchv1.JobComplete) && jobHasTerminalCondition(newJob, batchv1.JobComplete) {
				c.callback.HandleJobSucceeded(newJob, newJob.Annotations, newJob.Labels)
				if !config.GetBool("debugging.enabled") {
					c.queue.Add(QueueItem{
						Type:      DeleteJob,
						Namespace: newJob.Namespace,
						Name:      newJob.Name,
					})
				}
			}
		},
		DeleteFunc: func(obj any) {
			job := obj.(*batchv1.Job)
			logrus.WithFields(logrus.Fields{
				"namespace": job.Namespace,
				"job_name":  job.Name,
				"task_type": job.Labels[consts.JobLabelTaskType],
			}).Infof("job delete successfully")
		},
	}
}

// genPodEventHandlerFuncs generates event handler functions for Pod informer
func (c *Controller) genPodEventHandlerFuncs() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			newPod := newObj.(*corev1.Pod)

			if newPod.Status.Phase == corev1.PodPending {
				podReasons := []string{"ImagePullBackOff"}
				ownerRefs := newPod.OwnerReferences
				var jobOwnerRef *metav1.OwnerReference
				for _, ref := range ownerRefs {
					if ref.Kind == jobKind {
						jobOwnerRef = &ref
						break
					}
				}

				if jobOwnerRef == nil {
					return
				}

				for _, reason := range podReasons {
					if checkPodReason(newPod, reason) {
						job, err := getJob(c.ctx, newPod.Namespace, jobOwnerRef.Name)
						if err != nil {
							logrus.WithField("job_name", jobOwnerRef.Name).Error(err)
						}

						if job != nil {
							handlePodError(c.ctx, newPod, job, reason)
							// Trigger job failed callback to ensure proper cleanup (e.g., token release)
							c.callback.HandleJobFailed(job, job.Annotations, job.Labels)

							if !config.GetBool("debugging.enabled") {
								c.queue.Add(QueueItem{
									Type:      DeleteJob,
									Namespace: job.Namespace,
									Name:      job.Name,
								})
							}

							break
						}
					}
				}
			}
		},
		DeleteFunc: func(obj any) {
			pod := obj.(*corev1.Pod)
			logrus.WithField("namespace", pod.Namespace).WithField("pod_name", pod.Name).Infof("pod delete successfully")
		},
	}
}

// runWorker processes items from the queue
func (c *Controller) runWorker() {
	for c.processQueueItem() {
	}
}

// processQueueItem processes a single item from the queue
func (c *Controller) processQueueItem() bool {
	item, quit := c.queue.Get()
	if quit {
		return false
	}

	logrus.Infof("Processing item: %+v", item)

	defer c.queue.Done(item)

	var err error
	switch item.Type {
	case DeleteJob:
		if !config.GetBool("debugging.enabled") {
			err = deleteJob(context.Background(), item.Namespace, item.Name)
		} else {
			logrus.WithFields(logrus.Fields{
				"namespace": item.Namespace,
				"name":      item.Name,
			}).Info("Skipping job deletion due to debugging mode enabled")
		}

	default:
		logrus.Errorf("unknown resource type: %s", item.Type)
		return true
	}

	if err != nil {
		logrus.WithField("namespace", item.Namespace).WithField("name", item.Name).Error(err)
		c.queue.AddRateLimited(item)
		return true
	}

	c.queue.Forget(item)
	return true
}




// isNamespaceActive checks if a namespace is active and should process events
func (c *Controller) isNamespaceActive(namespace string) bool {
	c.namespaceMu.RLock()
	defer c.namespaceMu.RUnlock()

	active, exists := c.activeNamespaces[namespace]
	logrus.Debugf("Checking namespace %s: exists=%v, active=%v", namespace, exists, active)
	return exists && active
}




func checkPodReason(pod *corev1.Pod, reason string) bool {
	for _, containerStatus := range pod.Status.ContainerStatuses {
		// Check container Waiting status
		if containerStatus.State.Waiting != nil {
			if containerStatus.State.Waiting.Reason == reason {
				return true
			}
		}

		// In some cases, image pull failure may cause container to terminate directly (e.g., retry count exhausted)
		if containerStatus.State.Terminated != nil {
			if containerStatus.State.Terminated.Reason == reason {
				return true
			}
		}
	}

	return false
}

func handlePodError(ctx context.Context, pod *corev1.Pod, job *batchv1.Job, reason string) {
	// Get Pod events
	client, err := getK8sClient()
	if err != nil {
		logrus.WithField("pod_name", pod.Name).Errorf("kubernetes client not available, cannot fetch events: %v", err)
		return
	}
	events, err := client.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name),
	})
	if err != nil {
		logrus.WithField("pod_name", pod.Name).Errorf("failed to get events for Pod: %v", err)
		return
	}

	var messages []string
	for _, event := range events.Items {
		if event.Type == "Warning" && event.Reason == "Failed" {
			messages = append(messages, event.Message)
		}
	}

	logrus.WithFields(logrus.Fields{
		"job_name": job.Name,
		"pod_name": pod.Name,
		"reason":   reason,
	}).Error(messages)
}
