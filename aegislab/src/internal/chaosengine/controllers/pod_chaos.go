package controllers

import (
	"context"
	"fmt"
	"strings"

	"aegis/internal/chaosengine/chaos"
	"github.com/chaos-mesh/chaos-mesh/api/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreatePodChaos(cli client.Client, ctx context.Context, namespace string, appName string, action v1alpha1.PodChaosAction, duration *string, annotations map[string]string, labels map[string]string) (string, error) {
	spec := chaos.GeneratePodChaosSpec(namespace, appName, duration, action)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namespace, appName, action, rand.String(6)))
	podChaos, err := chaos.NewPodChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithPodChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := podChaos.ValidateCreate(ctx, podChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, podChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}

// CreatePodChaosWithContainer creates a pod chaos experiment with specified container names
func CreatePodChaosWithContainer(cli client.Client, ctx context.Context, namespace string, appName string, action v1alpha1.PodChaosAction, duration *string, annotations map[string]string, labels map[string]string, containerNames []string) (string, error) {
	spec := chaos.GeneratePodChaosSpecWithContainers(namespace, appName, duration, action, containerNames)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namespace, appName, action, rand.String(6)))
	podChaos, err := chaos.NewPodChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithPodChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := podChaos.ValidateCreate(ctx, podChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, podChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}
