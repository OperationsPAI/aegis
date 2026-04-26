package k8s

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEvaluateNamespacePodReadiness_NoActivePods(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1ObjectMeta("done-1"),
			Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
	}
	ready, _ := evaluateNamespacePodReadiness(pods)
	if ready {
		t.Fatalf("expected no-active-pods readiness=false")
	}
}

func TestEvaluateNamespacePodReadiness_AllActiveReady(t *testing.T) {
	pods := []corev1.Pod{
		newPodWithReady("app-1", corev1.PodRunning, true),
		newPodWithReady("app-2", corev1.PodRunning, true),
	}
	ready, _ := evaluateNamespacePodReadiness(pods)
	if !ready {
		t.Fatalf("expected all-active-ready readiness=true")
	}
}

func TestEvaluateNamespacePodReadiness_ActiveNotReady(t *testing.T) {
	pods := []corev1.Pod{
		newPodWithReady("app-1", corev1.PodRunning, true),
		newPodWithReady("app-2", corev1.PodPending, false),
	}
	ready, _ := evaluateNamespacePodReadiness(pods)
	if ready {
		t.Fatalf("expected active-not-ready readiness=false")
	}
}

func newPodWithReady(name string, phase corev1.PodPhase, ready bool) corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1ObjectMeta(name),
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: status,
				},
			},
		},
	}
}

func metav1ObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}

// --- evaluateNamespaceWorkloadsReady tests --------------------------------

func TestEvaluateNamespaceWorkloadsReady_Empty(t *testing.T) {
	ready, summary := evaluateNamespaceWorkloadsReady(nil, nil, nil, nil)
	if !ready {
		t.Fatalf("expected empty namespace to be ready, got summary=%q", summary)
	}
}

func TestEvaluateNamespaceWorkloadsReady_AllReady(t *testing.T) {
	deployments := []appsv1.Deployment{
		newDeploy("api", 2, 2),
		newDeploy("worker", 1, 1),
	}
	statefulSets := []appsv1.StatefulSet{
		newSTS("db", 3, 3),
	}
	daemonSets := []appsv1.DaemonSet{
		newDS("logger", 4, 4),
	}
	jobs := []batchv1.Job{
		newJob("loadgen", false), // not failed
	}
	ready, summary := evaluateNamespaceWorkloadsReady(deployments, statefulSets, daemonSets, jobs)
	if !ready {
		t.Fatalf("expected ready=true, got summary=%q", summary)
	}
}

func TestEvaluateNamespaceWorkloadsReady_DeploymentNotReady(t *testing.T) {
	deployments := []appsv1.Deployment{
		newDeploy("api", 3, 1), // 1/3
	}
	ready, summary := evaluateNamespaceWorkloadsReady(deployments, nil, nil, nil)
	if ready {
		t.Fatalf("expected ready=false for under-replicated deployment, got summary=%q", summary)
	}
	if got := summary; got == "" {
		t.Fatalf("expected non-empty summary")
	}
}

func TestEvaluateNamespaceWorkloadsReady_StatefulSetNotReady(t *testing.T) {
	statefulSets := []appsv1.StatefulSet{
		newSTS("db", 3, 2), // sequential ordinal coming up
	}
	ready, _ := evaluateNamespaceWorkloadsReady(nil, statefulSets, nil, nil)
	if ready {
		t.Fatalf("expected ready=false for not-fully-up statefulset")
	}
}

func TestEvaluateNamespaceWorkloadsReady_FailedJobBlocks(t *testing.T) {
	jobs := []batchv1.Job{
		newJob("loadgen", true), // Failed condition
	}
	ready, summary := evaluateNamespaceWorkloadsReady(nil, nil, nil, jobs)
	if ready {
		t.Fatalf("expected ready=false when a job is in Failed state, got summary=%q", summary)
	}
}

func TestEvaluateNamespaceWorkloadsReady_CompletedJobCounted(t *testing.T) {
	// A Job that has already completed (no Failed condition) should not
	// block readiness. Combined with a healthy deployment to make the
	// "all ready" assertion meaningful.
	jobs := []batchv1.Job{
		newJob("loadgen", false),
	}
	deployments := []appsv1.Deployment{
		newDeploy("api", 1, 1),
	}
	ready, summary := evaluateNamespaceWorkloadsReady(deployments, nil, nil, jobs)
	if !ready {
		t.Fatalf("expected ready=true with healthy deploy + completed job, got summary=%q", summary)
	}
}

func TestEvaluateNamespaceWorkloadsReady_DefaultReplicasIsOne(t *testing.T) {
	// A Deployment without an explicit Spec.Replicas defaults to 1 (k8s
	// API contract). A status of AvailableReplicas==0 should NOT pass.
	deployments := []appsv1.Deployment{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "implicit"},
			Spec:       appsv1.DeploymentSpec{}, // Replicas == nil → default 1
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
	}
	ready, summary := evaluateNamespaceWorkloadsReady(deployments, nil, nil, nil)
	if ready {
		t.Fatalf("expected ready=false for nil-replicas deployment with 0 available, got summary=%q", summary)
	}
}

func newDeploy(name string, desired, available int32) appsv1.Deployment {
	return appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       appsv1.DeploymentSpec{Replicas: &desired},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available},
	}
}

func newSTS(name string, desired, ready int32) appsv1.StatefulSet {
	return appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       appsv1.StatefulSetSpec{Replicas: &desired},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: ready},
	}
}

func newDS(name string, desired, available int32) appsv1.DaemonSet {
	return appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: desired,
			NumberAvailable:        available,
		},
	}
}

func newJob(name string, failed bool) batchv1.Job {
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if failed {
		job.Status.Conditions = []batchv1.JobCondition{{
			Type:   batchv1.JobFailed,
			Status: corev1.ConditionTrue,
		}}
	}
	return job
}
