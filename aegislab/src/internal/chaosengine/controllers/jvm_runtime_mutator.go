package controllers

import (
	"context"
	"fmt"
	"strings"

	"aegis/internal/chaosengine/chaos"
	chaosmeshv1alpha1 "github.com/chaos-mesh/chaos-mesh/api/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateJVMRuntimeMutatorChaos creates a JVM runtime mutator chaos experiment
func CreateJVMRuntimeMutatorChaos(
	cli client.Client,
	ctx context.Context,
	namespace string,
	appName string,
	className string,
	methodName string,
	mutationType string,
	duration *string,
	annotations map[string]string,
	labels map[string]string,
	opts ...chaos.OptChaos,
) (string, error) {

	var action chaosmeshv1alpha1.RuntimeMutatorChaosAction
	switch mutationType {
	case "constant":
		action = chaosmeshv1alpha1.RuntimeMutatorConstantAction
	case "operator":
		action = chaosmeshv1alpha1.RuntimeMutatorOperatorAction
	case "string":
		action = chaosmeshv1alpha1.RuntimeMutatorStringAction
	default:
		return "", fmt.Errorf("invalid mutation type: %s", mutationType)
	}

	spec := chaos.GenerateRuntimeMutatorChaosSpec(namespace, appName, duration, append([]chaos.OptChaos{
		chaos.WithRuntimeMutatorAction(action),
		chaos.WithRuntimeMutatorClass(className),
		chaos.WithRuntimeMutatorMethod(methodName),
	}, opts...)...)

	name := strings.ToLower(fmt.Sprintf("%s-%s-mutator-%s-%s", namespace, appName, mutationType, rand.String(6)))

	runtimeMutatorChaos, err := chaos.NewRuntimeMutatorChaos(
		chaos.WithAnnotations(annotations),
		chaos.WithLabels(labels),
		chaos.WithName(name),
		chaos.WithNamespace(namespace),
		chaos.WithRuntimeMutatorChaosSpec(spec),
	)

	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}

	create, err := runtimeMutatorChaos.ValidateCreate(ctx, runtimeMutatorChaos)
	if err != nil {
		logrus.Errorf("Failed to validate create chaos: %v", err)
		return "", err
	}
	logrus.Infof("create warning: %v", create)

	err = cli.Create(ctx, runtimeMutatorChaos)
	if err != nil {
		logrus.Errorf("Failed to create chaos: %v", err)
		return "", err
	}

	return name, nil
}
