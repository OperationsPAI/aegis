package handler

import (
	"context"
	"strings"
	"testing"

	k8sclient "aegis/internal/chaosengine/client"
	"aegis/internal/chaosengine/resourcelookup"
	"aegis/internal/chaosengine/systemconfig"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// stubChaosPointStore canned chaos_points rows for the groundtruth DB path tests.
type stubChaosPointStore struct {
	rows map[string][]resourcelookup.ChaosPointRow
}

func (s *stubChaosPointStore) QueryPoints(_ context.Context, system string) ([]resourcelookup.ChaosPointRow, error) {
	return s.rows[system], nil
}

// TestGetHTTPGroundtruth_DBBackedSurfacesServerAddressAndSpanName pins the
// regression that prompted the schema widen in this commit: with the
// DB-backed reader and the old (narrow) chaos_points target JSON, HTTP
// endpointPair.ServerAddress / .SpanName came back empty and getHTTPGroundtruth
// produced Service=[source, ""] + Span=[source, ""]. Re-dump now emits both
// fields and the reader plumbs them through.
func TestGetHTTPGroundtruth_DBBackedSurfacesServerAddressAndSpanName(t *testing.T) {
	t.Cleanup(func() {
		resourcelookup.SetChaosPointStore(nil)
		resourcelookup.ResetSystemCache(systemconfig.SystemTrainTicket)
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		k8sclient.InitWithClient(fake.NewClientBuilder().WithScheme(scheme).Build())
	})

	resourcelookup.SetChaosPointStore(&stubChaosPointStore{
		rows: map[string][]resourcelookup.ChaosPointRow{
			"ts": {{
				SystemName:     "ts",
				CapabilityName: "http_response_abort",
				Target: map[string]any{
					"app": "ts-user-service", "method": "GET", "path": "/api/users",
					"port":           float64(8080),
					"server_address": "ts-auth-service",
					"span_name":      "GET /api/users",
				},
			}},
		},
	})
	resourcelookup.ResetSystemCache(systemconfig.SystemTrainTicket)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ts-user-service-0", Namespace: "ts0", Labels: map[string]string{"app": "ts-user-service"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "ts-user-service"}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ts-auth-service-0", Namespace: "ts0", Labels: map[string]string{"app": "ts-auth-service"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "ts-auth-service"}}},
		},
	).Build()
	k8sclient.InitWithClient(fakeClient)

	gt, err := getHTTPGroundtruth(context.Background(), systemconfig.SystemTrainTicket, "ts0", 0)
	if err != nil {
		t.Fatalf("getHTTPGroundtruth: %v", err)
	}
	if len(gt.Service) != 2 {
		t.Fatalf("want 2 Service entries, got %d: %v", len(gt.Service), gt.Service)
	}
	if gt.Service[0] == "" || gt.Service[1] == "" {
		t.Errorf("both Service entries must be non-empty (regression: DB-backed produced [source, \"\"]), got %v", gt.Service)
	}
	if gt.Service[0] != "ts-user-service" || gt.Service[1] != "ts-auth-service" {
		t.Errorf("Service mismatch: got %v, want [ts-user-service ts-auth-service]", gt.Service)
	}
	if len(gt.Span) != 1 || gt.Span[0] != "GET /api/users" {
		t.Errorf("Span mismatch: got %v, want [GET /api/users]", gt.Span)
	}
}

func TestSelectContainerByIndex_EmptyListGivesActionableError(t *testing.T) {
	// Reproduces the sockshop "max: -1" failure: GetGroundtruth runs at a
	// moment when no pods carry the configured app-label key, so the
	// container snapshot is empty. The previous implementation reported
	// "container index out of range: 5 (max: -1)" which gave the user no hint
	// that the list was actually empty.
	_, err := selectContainerByIndex(
		nil, // empty list
		systemconfig.SystemSockShop,
		"sockshop1",
		5,
	)
	if err == nil {
		t.Fatal("expected error for empty container list, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"no containers found",
		`system "sockshop"`,
		`namespace "sockshop1"`,
		"index 5",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q is missing required substring %q", msg, want)
		}
	}
	// And it must NOT report the bogus "max: -1" any longer.
	if strings.Contains(msg, "max: -1") {
		t.Errorf("error %q still contains the unhelpful 'max: -1' phrasing", msg)
	}
}

func TestSelectContainerByIndex_OutOfRangeReportsCount(t *testing.T) {
	containers := []resourcelookup.ContainerInfo{
		{PodName: "p1", AppLabel: "front-end", ContainerName: "front-end"},
		{PodName: "p2", AppLabel: "catalogue", ContainerName: "catalogue"},
	}
	_, err := selectContainerByIndex(
		containers,
		systemconfig.SystemSockShop,
		"sockshop1",
		7,
	)
	if err == nil {
		t.Fatal("expected error for out-of-range index, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"out of range",
		"index 7",
		"2 containers available",
		"valid range 0..1",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q is missing required substring %q", msg, want)
		}
	}
}

func TestSelectContainerByIndex_NegativeIndexHandled(t *testing.T) {
	containers := []resourcelookup.ContainerInfo{
		{PodName: "p1", AppLabel: "front-end", ContainerName: "front-end"},
	}
	_, err := selectContainerByIndex(
		containers,
		systemconfig.SystemSockShop,
		"sockshop1",
		-1,
	)
	if err == nil {
		t.Fatal("expected error for negative index, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected out-of-range error, got %q", err.Error())
	}
}

// TestResolveSpecNamespace_PrefersSpecNamespace pins the regression where every
// *Spec.GetGroundtruth used GetNamespaceByIndex(system, defaultStartIndex)
// instead of the caller-provided namespace. Guided submits with --namespace hs28
// (or any non-zero pool slot) used to fail with "no containers found ... in
// namespace hs0" because the spec dropped the operator's namespace on the floor.
func TestResolveSpecNamespace_PrefersSpecNamespace(t *testing.T) {
	got, err := resolveSpecNamespace(systemconfig.SystemHotelReservation, "hs28")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hs28" {
		t.Errorf("got %q, want %q (the spec's per-call namespace, not the pool head)", got, "hs28")
	}
}

func TestResolveSpecNamespace_FallsBackToPoolHeadWhenUnset(t *testing.T) {
	// Legacy callers that build specs without setting Namespace must keep working.
	got, err := resolveSpecNamespace(systemconfig.SystemHotelReservation, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, err := systemconfig.GetNamespaceByIndex(systemconfig.SystemHotelReservation, defaultStartIndex)
	if err != nil {
		t.Fatalf("unexpected error from pool head: %v", err)
	}
	if got != want {
		t.Errorf("fallback got %q, want %q", got, want)
	}
}

func TestSelectContainerByIndex_ValidIndexReturnsEntry(t *testing.T) {
	containers := []resourcelookup.ContainerInfo{
		{PodName: "p1", AppLabel: "front-end", ContainerName: "front-end"},
		{PodName: "p2", AppLabel: "catalogue", ContainerName: "catalogue"},
	}
	got, err := selectContainerByIndex(
		containers,
		systemconfig.SystemSockShop,
		"sockshop1",
		1,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != containers[1] {
		t.Errorf("got %+v, want %+v", got, containers[1])
	}
}
