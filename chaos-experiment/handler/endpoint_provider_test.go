package handler

import (
	"context"
	"testing"

	k8sclient "github.com/OperationsPAI/chaos-experiment/client"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type appLabelMetadataStore struct {
	serviceNames map[string][]string
	networkPairs map[string][]systemconfig.NetworkPairData
}

func (m *appLabelMetadataStore) GetServiceEndpoints(system string, serviceName string) ([]systemconfig.ServiceEndpointData, error) {
	return nil, nil
}

func (m *appLabelMetadataStore) GetAllServiceNames(system string) ([]string, error) {
	return m.serviceNames[system], nil
}

func (m *appLabelMetadataStore) GetJavaClassMethods(system string, serviceName string) ([]systemconfig.JavaClassMethodData, error) {
	return nil, nil
}

func (m *appLabelMetadataStore) GetDatabaseOperations(system string, serviceName string) ([]systemconfig.DatabaseOperationData, error) {
	return nil, nil
}

func (m *appLabelMetadataStore) GetGRPCOperations(system string, serviceName string) ([]systemconfig.GRPCOperationData, error) {
	return nil, nil
}

func (m *appLabelMetadataStore) GetNetworkPairs(system string) ([]systemconfig.NetworkPairData, error) {
	return m.networkPairs[system], nil
}

func (m *appLabelMetadataStore) GetRuntimeMutatorTargets(system string) ([]systemconfig.RuntimeMutatorTargetData, error) {
	return nil, nil
}

func TestGetAppLabelByIndexUsesInjectableOrdering(t *testing.T) {
	const testSystem = systemconfig.SystemType("app-index-filter-test")
	const namespace = "app-index-filter-test0"

	t.Cleanup(func() {
		systemconfig.SetMetadataStore(nil)
		_ = systemconfig.UnregisterSystem(testSystem)
		_ = systemconfig.SetCurrentSystem(systemconfig.SystemTrainTicket)

		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		k8sclient.InitWithClient(fake.NewClientBuilder().WithScheme(scheme).Build())
	})

	if err := systemconfig.RegisterSystem(systemconfig.SystemRegistration{
		Name:        testSystem,
		NsPattern:   "^app-index-filter-test\\d+$",
		DisplayName: "AppIndexFilterTest",
		AppLabelKey: "app",
	}); err != nil {
		t.Fatalf("RegisterSystem() error = %v", err)
	}
	if err := systemconfig.SetCurrentSystem(testSystem); err != nil {
		t.Fatalf("SetCurrentSystem() error = %v", err)
	}

	systemconfig.SetMetadataStore(&appLabelMetadataStore{
		serviceNames: map[string][]string{
			string(testSystem): {"db", "svc-a", "svc-b"},
		},
		networkPairs: map[string][]systemconfig.NetworkPairData{
			string(testSystem): {
				{Source: "svc-a", Target: "db"},
				{Source: "svc-b", Target: "svc-a"},
			},
		},
	})

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db-0", Namespace: namespace, Labels: map[string]string{"app": "db"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "svc-a-0", Namespace: namespace, Labels: map[string]string{"app": "svc-a"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "svc-b-0", Namespace: namespace, Labels: map[string]string{"app": "svc-b"}}},
	).Build()
	k8sclient.InitWithClient(fakeClient)

	appName, err := getAppLabelByIndex(context.Background(), testSystem, namespace, 1)
	if err != nil {
		t.Fatalf("getAppLabelByIndex() error = %v", err)
	}
	if appName != "svc-b" {
		t.Fatalf("getAppLabelByIndex(..., 1) = %q, want %q", appName, "svc-b")
	}
}
