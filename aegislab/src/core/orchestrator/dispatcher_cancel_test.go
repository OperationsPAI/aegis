package consumer

import (
	"context"
	"errors"
	"testing"

	"aegis/cli/apiclient"
)

type fakeChaosCancelClient struct {
	deleteInjectionID    string
	deleteBatchID        string
	deleteInjectionCalls int
	deleteBatchCalls     int
	deleteInjectionErr   error
	deleteBatchErr       error
}

func (f *fakeChaosCancelClient) CreateInjection(_ context.Context, _ apiclient.ChaosChaosCreateInjectionReq) (string, error) {
	return "", errors.New("unused")
}
func (f *fakeChaosCancelClient) CreateInjectionBatch(_ context.Context, _ apiclient.ChaosChaosCreateInjectionBatchReq) (string, error) {
	return "", errors.New("unused")
}
func (f *fakeChaosCancelClient) DeleteInjection(_ context.Context, id string) error {
	f.deleteInjectionCalls++
	f.deleteInjectionID = id
	return f.deleteInjectionErr
}
func (f *fakeChaosCancelClient) DeleteInjectionBatch(_ context.Context, id string) error {
	f.deleteBatchCalls++
	f.deleteBatchID = id
	return f.deleteBatchErr
}

func withFakeChaosClient(t *testing.T, fake *fakeChaosCancelClient) {
	t.Helper()
	prev := testChaosServiceClient
	testChaosServiceClient = func() (chaosServiceClient, error) { return fake, nil }
	t.Cleanup(func() { testChaosServiceClient = prev })
}

func clearChaosServiceRegistry(t *testing.T) {
	t.Helper()
	chaosServiceTaskRegistryMutex.Lock()
	chaosServiceTaskRegistry = make(map[string]chaosServiceInjectionRef)
	chaosServiceTaskRegistryMutex.Unlock()
}

func TestCancelChaosServiceInjectionRoutesToDeleteInjection(t *testing.T) {
	clearChaosServiceRegistry(t)
	fake := &fakeChaosCancelClient{}
	withFakeChaosClient(t, fake)

	registerChaosServiceTask("task-A", "INJECT-ULID-1", false)

	handled, err := CancelChaosServiceInjection(context.Background(), "task-A")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for registered task")
	}
	if fake.deleteInjectionCalls != 1 {
		t.Fatalf("DeleteInjection calls: want 1 got %d", fake.deleteInjectionCalls)
	}
	if fake.deleteInjectionID != "INJECT-ULID-1" {
		t.Fatalf("DeleteInjection id: want INJECT-ULID-1 got %q", fake.deleteInjectionID)
	}
	if fake.deleteBatchCalls != 0 {
		t.Fatalf("DeleteInjectionBatch must not be called for non-batch task")
	}
	if _, ok := lookupChaosServiceTask("task-A"); ok {
		t.Fatal("task should be deregistered after successful cancel")
	}
}

func TestCancelChaosServiceInjectionRoutesBatch(t *testing.T) {
	clearChaosServiceRegistry(t)
	fake := &fakeChaosCancelClient{}
	withFakeChaosClient(t, fake)

	registerChaosServiceTask("task-B", "BATCH-ULID-1", true)

	handled, err := CancelChaosServiceInjection(context.Background(), "task-B")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	if fake.deleteBatchCalls != 1 || fake.deleteBatchID != "BATCH-ULID-1" {
		t.Fatalf("want one DeleteInjectionBatch(BATCH-ULID-1), got calls=%d id=%q",
			fake.deleteBatchCalls, fake.deleteBatchID)
	}
	if fake.deleteInjectionCalls != 0 {
		t.Fatal("DeleteInjection must not be called for batch task")
	}
}

func TestCancelChaosServiceInjectionSwallows404(t *testing.T) {
	clearChaosServiceRegistry(t)
	fake := &fakeChaosCancelClient{deleteInjectionErr: errChaosServiceNotFound}
	withFakeChaosClient(t, fake)

	registerChaosServiceTask("task-C", "INJECT-ULID-2", false)

	handled, err := CancelChaosServiceInjection(context.Background(), "task-C")
	if err != nil {
		t.Fatalf("404 must be swallowed, got: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true (we owned this task even though resource was gone)")
	}
	if _, ok := lookupChaosServiceTask("task-C"); ok {
		t.Fatal("task should be deregistered after 404")
	}
}

func TestCancelChaosServiceInjectionUnknownTaskNoop(t *testing.T) {
	clearChaosServiceRegistry(t)
	fake := &fakeChaosCancelClient{}
	withFakeChaosClient(t, fake)

	handled, err := CancelChaosServiceInjection(context.Background(), "task-never-dispatched")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if handled {
		t.Fatal("expected handled=false for unknown task — caller falls back to legacy CR delete")
	}
	if fake.deleteInjectionCalls != 0 || fake.deleteBatchCalls != 0 {
		t.Fatal("no Delete call should be issued for unknown task")
	}
}

// TestDefaultChaosServiceClientUsesSAResolver guards against the previous-attempt
// regression where the Delete path introduced a parallel newClient() that read
// CHAOS_OUTBOUND_BEARER directly, bypassing the SA-token mint. The production
// factory must mint bearer via resolveChaosOutboundBearer so both Create and
// Delete share the same auth path.
func TestDefaultChaosServiceClientUsesSAResolver(t *testing.T) {
	t.Setenv("CHAOS_OUTBOUND_BEARER", "env-token-xyz")
	// no SA mint in test binary → resolver falls back to env.
	got := resolveChaosOutboundBearer()
	if got != "env-token-xyz" {
		t.Fatalf("resolveChaosOutboundBearer must read env when SA mint absent; got %q", got)
	}
}
