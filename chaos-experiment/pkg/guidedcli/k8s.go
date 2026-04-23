package guidedcli

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/OperationsPAI/chaos-experiment/internal/resourcelookup"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

func safeAppLabels(ctx context.Context, namespace string, systemType systemconfig.SystemType) ([]string, error) {
	return resourcelookup.GetInjectableAppLabels(ctx, systemType, namespace)
}

func safeContainers(namespace string) ([]resourcelookup.ContainerInfo, error) {
	pods, err := listPodsSafe(namespace)
	if err != nil {
		return nil, err
	}
	labelKey := systemconfig.GetCurrentAppLabelKey()
	result := make([]resourcelookup.ContainerInfo, 0)
	for _, pod := range pods {
		appLabel := pod.Labels[labelKey]
		for _, container := range pod.Spec.Containers {
			result = append(result, resourcelookup.ContainerInfo{
				PodName:       pod.Name,
				AppLabel:      appLabel,
				ContainerName: container.Name,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AppLabel != result[j].AppLabel {
			return result[i].AppLabel < result[j].AppLabel
		}
		return result[i].ContainerName < result[j].ContainerName
	})
	return result, nil
}

func listPodsSafe(namespace string) ([]corev1.Pod, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = buildKubeconfigSafe()
		if err != nil {
			return nil, err
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}
	list, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods in namespace %s: %w", namespace, err)
	}
	return list.Items, nil
}
