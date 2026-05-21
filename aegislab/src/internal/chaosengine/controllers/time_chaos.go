package controllers

import (
	"context"
	"fmt"
	"strings"

	"aegis/internal/chaosengine/chaos"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateTimeChaosWithContainer creates a time chaos experiment with specified container names
func CreateTimeChaosWithContainer(cli client.Client, ctx context.Context, namespace string, appName string, timeOffset string, duration *string, annotations map[string]string, labels map[string]string, containerNames []string) (string, error) {
	spec := chaos.GenerateTimeChaosSpecWithContainers(namespace, appName, duration, timeOffset, containerNames)
	name := strings.ToLower(fmt.Sprintf("%s-%s-time-%s", namespace, appName, rand.String(6)))
	timeChaos, err := chaos.NewTimeChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithTimeChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := timeChaos.ValidateCreate(ctx, timeChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, timeChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}
