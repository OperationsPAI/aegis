package k8s

import (
	"testing"

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
