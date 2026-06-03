package consumer

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"aegis/cli/apiclient"

	"github.com/spf13/viper"
)

type stubCancelClient struct {
	called bool
	err    error
}

func (s *stubCancelClient) CreateInjection(_ context.Context, _ apiclient.ChaosChaosCreateInjectionReq) (string, error) {
	return "", nil
}

func (s *stubCancelClient) CreateInjectionBatch(_ context.Context, _ apiclient.ChaosChaosCreateInjectionBatchReq) (string, error) {
	return "", nil
}

func (s *stubCancelClient) DeleteInjectionByTask(_ context.Context, _ string) error {
	s.called = true
	return s.err
}

// TestCancelChaosServiceInjectionUnknownTaskNoop guards the orphan-CR fallback
// path in task/service.go: when chaos-service reports the task as unknown
// (404 → errChaosServiceNotFound) the hook MUST return handled=false so the
// caller falls through to DeleteChaosCRDsByLabel and cleans up CRs created by
// pre-migration runs / other orchestrator processes / hand edits.
func TestCancelChaosServiceInjectionUnknownTaskNoop(t *testing.T) {
	prevFactory := testChaosServiceClient
	t.Cleanup(func() { testChaosServiceClient = prevFactory })

	stub := &stubCancelClient{err: errChaosServiceNotFound}
	testChaosServiceClient = func() (chaosServiceClient, error) { return stub, nil }

	handled, err := CancelChaosServiceInjection(context.Background(), "task-unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Fatal("handled=true for unknown task; legacy CR-cleanup fallback will be skipped")
	}
	if !stub.called {
		t.Fatal("DeleteInjectionByTask was not invoked")
	}
}

func TestCancelChaosServiceInjectionSuccessHandled(t *testing.T) {
	prevFactory := testChaosServiceClient
	t.Cleanup(func() { testChaosServiceClient = prevFactory })

	stub := &stubCancelClient{}
	testChaosServiceClient = func() (chaosServiceClient, error) { return stub, nil }

	handled, err := CancelChaosServiceInjection(context.Background(), "task-known")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("handled=false after successful chaos-service cancel; legacy cleanup will double-fire")
	}
}

// TestDefaultChaosServiceClientUsesSAResolver guards against a previous-attempt
// regression where the Cancel path built a parallel chaos client that bypassed
// the SA mint. The Create and Delete paths share defaultChaosServiceClient,
// so a single env-fallback assertion against the factory covers both.
func TestDefaultChaosServiceClientUsesSAResolver(t *testing.T) {
	prev := chaosSATokenRef.Load()
	t.Cleanup(func() { chaosSATokenRef.Store(prev) })
	chaosSATokenRef.Store(nil)

	viper.Set("chaos.service_url", "http://example.invalid")
	t.Cleanup(func() { viper.Set("chaos.service_url", "") })
	t.Setenv(OutboundBearerEnv, "env-fallback-cancel")
	outboundBearerEnvDeprecationOnce = sync.Once{}

	cli, err := defaultChaosServiceClient()
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	sdkCli, ok := cli.(*sdkChaosServiceClient)
	if !ok {
		t.Fatalf("unexpected client type %T", cli)
	}
	if sdkCli.bearer != "env-fallback-cancel" {
		t.Errorf("bearer = %q; want env fallback", sdkCli.bearer)
	}
}

// TestChaosServiceErrClassifiesAtCapacity pins the issue-#533 crux: a 429 from
// the chaos service must be classified (via the HTTP status code, not error
// string matching) as the retryable errChaosServiceAtCapacity sentinel so the
// FaultInjection worker reschedules instead of terminal-failing the trace.
// Non-429 statuses (e.g. 404 catalog miss) must stay terminal — they must NOT
// match the sentinel.
func TestChaosServiceErrClassifiesAtCapacity(t *testing.T) {
	base := errors.New("429 Too Many Requests")

	at := chaosServiceErr(base, &http.Response{StatusCode: http.StatusTooManyRequests})
	if !errors.Is(at, errChaosServiceAtCapacity) {
		t.Fatalf("429 not classified as at-capacity: %v", at)
	}

	notFound := chaosServiceErr(errors.New("404 Not Found"), &http.Response{StatusCode: http.StatusNotFound})
	if errors.Is(notFound, errChaosServiceAtCapacity) {
		t.Fatalf("404 wrongly classified as at-capacity: %v", notFound)
	}

	nilResp := chaosServiceErr(base, nil)
	if errors.Is(nilResp, errChaosServiceAtCapacity) {
		t.Fatalf("nil response wrongly classified as at-capacity: %v", nilResp)
	}
}

