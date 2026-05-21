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

func CreateJVMChaos(cli client.Client, ctx context.Context, namespace string, appName string, action v1alpha1.JVMChaosAction, duration *string, annotations map[string]string, labels map[string]string, opts ...chaos.OptJVMChaos) (string, error) {
	spec := chaos.GenerateJVMChaosSpec(namespace, appName, duration, append([]chaos.OptJVMChaos{chaos.WithJVMAction(action)}, opts...)...)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namespace, appName, action, rand.String(6)))
	jvmChaos, err := chaos.NewJvmChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithJVMChaosSpec(spec),
	)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	create, err := jvmChaos.ValidateCreate(ctx, jvmChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)
	err = cli.Create(ctx, jvmChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}
	return name, nil
}
