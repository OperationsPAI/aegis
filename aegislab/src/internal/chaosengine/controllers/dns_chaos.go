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

// CreateDnsChaos creates a DNS chaos experiment with the specified parameters
func CreateDnsChaos(cli client.Client, ctx context.Context, namespace string, appName string, action v1alpha1.DNSChaosAction, patterns []string, duration *string, annotations map[string]string, labels map[string]string) (string, error) {
	spec := chaos.GenerateDnsChaosSpec(namespace, appName, duration, action, patterns)
	name := strings.ToLower(fmt.Sprintf("%s-%s-dns-%s", namespace, appName, rand.String(6)))
	dnsChaos, err := chaos.NewDnsChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithDnsChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create DNS chaos: %v", err)
		return "", err
	}
	create, err := dnsChaos.ValidateCreate(ctx, dnsChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create DNS chaos: %v", err)
		return "", err
	}
	logrus.Infof("Create warning: %v", create)
	err = cli.Create(ctx, dnsChaos)
	if err != nil {
		logrus.Errorf("Failed to create DNS chaos: %v", err)
		return "", err
	}
	return name, nil
}
