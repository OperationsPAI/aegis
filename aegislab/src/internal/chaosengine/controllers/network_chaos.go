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

// CreateNetworkChaos creates a NetworkChaos resource
func CreateNetworkChaos(cli client.Client, ctx context.Context, namespace string, appName string, action v1alpha1.NetworkChaosAction, duration *string, annotations map[string]string, labels map[string]string, opts ...chaos.OptNetworkChaos) (string, error) {
	spec := chaos.GenerateNetworkChaosSpec(namespace, appName, duration, action, opts...)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namespace, appName, string(action), rand.String(6)))
	networkChaos, err := chaos.NewNetworkChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithNetworkChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := networkChaos.ValidateCreate(ctx, networkChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, networkChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}
