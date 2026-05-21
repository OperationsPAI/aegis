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

func CreateHTTPChaos(cli client.Client, ctx context.Context, namespace string, appName string, stressType string, duration *string, annotations map[string]string, labels map[string]string, opts ...chaos.OptHTTPChaos) (string, error) {
	spec := chaos.GenerateHttpChaosSpec(namespace, appName, duration, opts...)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namespace, appName, stressType, rand.String(6)))
	httpChaos, err := chaos.NewHttpChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithHttpChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := httpChaos.ValidateCreate(ctx, httpChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, httpChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}
