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

// CreateStressChaosWithContainer creates a stress chaos experiment with specified container names
func CreateStressChaosWithContainer(cli client.Client, ctx context.Context, namespace string, appName string, stressors v1alpha1.Stressors, stressType string, duration *string, annotations map[string]string, labels map[string]string, containerNames []string) (string, error) {
	spec := chaos.GenerateStressChaosSpecWithContainers(namespace, appName, duration, stressors, containerNames)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namespace, appName, stressType, rand.String(6)))
	stressChaos, err := chaos.NewStressChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithStressChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := stressChaos.ValidateCreate(ctx, stressChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, stressChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}

func MakeCPUStressors(load int, worker int) v1alpha1.Stressors {
	return v1alpha1.Stressors{
		CPUStressor: &v1alpha1.CPUStressor{
			Load:     &load,
			Stressor: v1alpha1.Stressor{Workers: worker},
		},
	}
}

func MakeMemoryStressors(memorySize string, worker int) v1alpha1.Stressors {
	return v1alpha1.Stressors{
		MemoryStressor: &v1alpha1.MemoryStressor{
			Size:     memorySize,
			Stressor: v1alpha1.Stressor{Workers: worker},
		},
	}
}
