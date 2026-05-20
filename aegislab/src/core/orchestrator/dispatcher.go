package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"aegis/cli/apiclient"
	chaoscrud "aegis/crud/chaos"
	"aegis/platform/config"

	guidedcli "aegis/platform/chaos"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

const (
	dispatcherChaosTimeoutConfigKey = "chaos.dispatch_timeout_seconds"
	dispatcherChaosTimeoutDefault   = 60 * time.Second
)

// dispatcherChaosTimeout returns the per-call timeout for chaos-service POSTs.
// Unset / non-positive values fall back to dispatcherChaosTimeoutDefault.
func dispatcherChaosTimeout() time.Duration {
	if s := config.GetInt(dispatcherChaosTimeoutConfigKey); s > 0 {
		return time.Duration(s) * time.Second
	}
	return dispatcherChaosTimeoutDefault
}

var injectionDispatchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "aegis_injection_dispatch_total",
	Help: "Fault-injection dispatches by logical system.",
}, []string{"system"})

// chaosServiceClient is the seam tests substitute. Returning the ack-id (one
// per inject, or one batch_id) is enough for the dispatcher — the terminal
// webhook arrives separately and is handled by crud/hooks/chaos.
type chaosServiceClient interface {
	CreateInjection(ctx context.Context, body apiclient.ChaosChaosCreateInjectionReq) (string, error)
	CreateInjectionBatch(ctx context.Context, body apiclient.ChaosChaosCreateInjectionBatchReq) (string, error)
	DeleteInjection(ctx context.Context, id string) error
	DeleteInjectionBatch(ctx context.Context, id string) error
}

// errChaosServiceNotFound signals an idempotent cleanup hit: the chaos service
// already considers the resource gone (HTTP 404). Callers swallow it so cancel
// stays idempotent under retries.
var errChaosServiceNotFound = errors.New("chaos service: resource not found")

var defaultChaosServiceClient = func() (chaosServiceClient, error) {
	url := strings.TrimSpace(config.GetChaosServiceURL())
	if url == "" {
		return nil, errors.New("chaos.service_url is empty; cannot dispatch via chaos service")
	}
	return &sdkChaosServiceClient{baseURL: url, bearer: resolveChaosOutboundBearer()}, nil
}

// resolveChaosOutboundBearer prefers the SA token minted at boot; if mint
// hasn't run (test binary, missing seed) it logs a one-shot ERROR and falls
// back to CHAOS_OUTBOUND_BEARER. The env path is kept for one release as a
// safety net — production must wire the SA token before next bump.
func resolveChaosOutboundBearer() string {
	if tok := currentChaosSAToken(); tok != "" {
		return tok
	}
	envTok := strings.TrimSpace(os.Getenv(OutboundBearerEnv))
	if envTok != "" {
		outboundBearerEnvDeprecationOnce.Do(func() {
			logrus.StandardLogger().WithField("env", OutboundBearerEnv).
				Error("DEPRECATED: backend→chaos auth using static CHAOS_OUTBOUND_BEARER; chaos-client SA mint not wired (missing seed?). Token will be rejected once one-release grace window closes.")
		})
	}
	return envTok
}

var outboundBearerEnvDeprecationOnce sync.Once

type sdkChaosServiceClient struct {
	baseURL string
	bearer  string
}

func (c *sdkChaosServiceClient) newAPIClient() *apiclient.APIClient {
	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(c.baseURL, "/")}}
	if c.bearer != "" {
		cfg.AddDefaultHeader("Authorization", "Bearer "+c.bearer)
	}
	return apiclient.NewAPIClient(cfg)
}

func (c *sdkChaosServiceClient) CreateInjection(ctx context.Context, body apiclient.ChaosChaosCreateInjectionReq) (string, error) {
	cli := c.newAPIClient()
	resp, _, err := cli.ChaosAPI.ChaosCreateInjection(ctx).ChaosChaosCreateInjectionReq(body).Execute()
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Data == nil || resp.Data.Id == nil {
		return "", errors.New("chaos service: empty injection_id in ACK")
	}
	return *resp.Data.Id, nil
}

func (c *sdkChaosServiceClient) CreateInjectionBatch(ctx context.Context, body apiclient.ChaosChaosCreateInjectionBatchReq) (string, error) {
	cli := c.newAPIClient()
	resp, _, err := cli.ChaosAPI.ChaosCreateInjectionBatch(ctx).ChaosChaosCreateInjectionBatchReq(body).Execute()
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Data == nil || resp.Data.Id == nil {
		return "", errors.New("chaos service: empty batch_id in ACK")
	}
	return *resp.Data.Id, nil
}

func (c *sdkChaosServiceClient) DeleteInjection(ctx context.Context, id string) error {
	cli := c.newAPIClient()
	_, httpResp, err := cli.ChaosAPI.ChaosDeleteInjection(ctx, id).Execute()
	return classifyChaosDeleteErr(httpResp, err)
}

func (c *sdkChaosServiceClient) DeleteInjectionBatch(ctx context.Context, id string) error {
	cli := c.newAPIClient()
	_, httpResp, err := cli.ChaosAPI.ChaosDeleteInjectionBatch(ctx, id).Execute()
	return classifyChaosDeleteErr(httpResp, err)
}

func classifyChaosDeleteErr(httpResp *http.Response, err error) error {
	if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
		return errChaosServiceNotFound
	}
	return err
}

// dispatcherDeps carries the request-scoped data the chaos-service branch
// needs to build caller_metadata. Cheaper than threading it through every
// chaos-experiment SDK arg.
type dispatcherDeps struct {
	taskID           string
	traceID          string
	projectID        int
	userID           int
	groupID          string
	pedestal         string
	preDuration      int
	benchmarkID      int
	benchmarkName    string
	benchmarkImage   string
	benchmarkCommand string
	// instance + chartVersion address per-instance Point catalog rows.
	// Seed manifests carry instance=seed / chart_version=seed-genesis;
	// these are what the chaos service hashes into the Point ID.
	instance     string
	chartVersion string
}

// dispatchBatchCreate POSTs the guided configs to aegis-chaos and blocks on
// the ACK. The terminal webhook lands at crud/hooks/chaos and fires
// BuildDatapack under the shared (task_id, kind) uniqueness gate.
func dispatchBatchCreate(
	ctx context.Context,
	logEntry *logrus.Entry,
	system, namespace string,
	configs []guidedcli.GuidedConfig,
	deps dispatcherDeps,
) ([]string, error) {
	injectionDispatchTotal.WithLabelValues(system).Inc()
	return dispatchViaChaosService(ctx, logEntry, system, namespace, configs, deps)
}

// dispatchViaChaosService POSTs to aegis-chaos and blocks on the ACK only.
// The terminal webhook lands at crud/hooks/chaos, which fires BuildDatapack
// under its own (task_id, kind) uniqueness gate.
func dispatchViaChaosService(
	ctx context.Context,
	logEntry *logrus.Entry,
	system, namespace string,
	configs []guidedcli.GuidedConfig,
	deps dispatcherDeps,
) ([]string, error) {
	clientFactory := defaultChaosServiceClient
	if testChaosServiceClient != nil {
		clientFactory = testChaosServiceClient
	}
	cli, err := clientFactory()
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, dispatcherChaosTimeout())
	defer cancel()

	meta := buildCallerMetadata(deps, namespace)
	meta["groundtruths"] = renderGroundtruths(configs)
	// engine_config round-trips the originating guided spec list through
	// caller_metadata so the webhook receiver can write the same JSON the
	// pre-§11-5c orchestrator wrote into fault_injections.engine_config.
	// Marshal failure here would silently land an empty-object engine_config
	// downstream — fail the dispatch instead so the caller sees the bug.
	engineConfigBytes, err := json.Marshal(configs)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: marshal engine_config: %w", err)
	}
	meta["engine_config"] = string(engineConfigBytes)

	if len(configs) == 1 {
		pid, _, _, err := chaoscrud.GuidedChaosPointID(configs[0], deps.instance, deps.chartVersion)
		if err != nil {
			return nil, fmt.Errorf("dispatcher: derive point_id: %w", err)
		}
		params := chaoscrud.GuidedToChaosParams(capabilityOf(configs[0]), configs[0])
		idemKey := deps.traceID + ":" + pid
		body := *apiclient.NewChaosChaosCreateInjectionReq()
		body.PointId = &pid
		body.IdempotencyKey = &idemKey
		body.Params = params
		body.CallerMetadata = meta
		body.AdditionalProperties = map[string]any{"namespace": namespace}
		injectionID, err := cli.CreateInjection(callCtx, body)
		if err != nil {
			return nil, fmt.Errorf("dispatcher: POST /v1beta/injections: %w", err)
		}
		registerChaosServiceTask(deps.taskID, injectionID, false)
		logEntry.WithFields(logrus.Fields{
			"injection_id": injectionID,
			"point_id":     pid,
		}).Info("dispatcher: chaos service ack received")
		return []string{injectionID}, nil
	}

	batchKey := deps.traceID + ":batch:" + uuid.NewString()
	children := make([]apiclient.ChaosChaosCreateBatchChildReq, 0, len(configs))
	for i, cfg := range configs {
		pid, _, _, err := chaoscrud.GuidedChaosPointID(cfg, deps.instance, deps.chartVersion)
		if err != nil {
			return nil, fmt.Errorf("dispatcher: spec[%d]: derive point_id: %w", i, err)
		}
		params := chaoscrud.GuidedToChaosParams(capabilityOf(cfg), cfg)
		childKey := fmt.Sprintf("%s:%d:%s", deps.traceID, i, pid)
		pidCopy := pid
		children = append(children, apiclient.ChaosChaosCreateBatchChildReq{
			PointId:              &pidCopy,
			IdempotencyKey:       &childKey,
			Params:               params,
			CallerMetadata:       meta,
			AdditionalProperties: map[string]any{"namespace": namespace},
		})
	}
	body := *apiclient.NewChaosChaosCreateInjectionBatchReq(batchKey, children)
	body.BatchCallerMetadata = meta
	batchID, err := cli.CreateInjectionBatch(callCtx, body)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: POST /v1beta/injection-batches: %w", err)
	}
	registerChaosServiceTask(deps.taskID, batchID, true)
	logEntry.WithFields(logrus.Fields{
		"batch_id": batchID,
	}).Info("dispatcher: chaos service ack received (batch)")
	return []string{batchID}, nil
}

func capabilityOf(cfg guidedcli.GuidedConfig) string {
	return chaoscrud.ChaosTypeToCapability[strings.TrimSpace(cfg.ChaosType)]
}

// renderGroundtruths derives a minimal per-spec impact ({service, container}).
// chaos service stores chaos_injections.groundtruth=NULL today; the algorithm
// container asserts non-empty ground_truth in injection.json, so the dispatcher
// computes it here from the originating GuidedConfig. Legacy
// handler.BatchCreate populates this server-side; this mirrors that contract.
func renderGroundtruths(configs []guidedcli.GuidedConfig) []map[string]any {
	gts := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		gt := map[string]any{}
		if app := strings.TrimSpace(cfg.App); app != "" {
			gt["service"] = []string{app}
		}
		if container := strings.TrimSpace(cfg.Container); container != "" {
			gt["container"] = []string{container}
		}
		if len(gt) > 0 {
			gts = append(gts, gt)
		}
	}
	return gts
}

func buildCallerMetadata(d dispatcherDeps, namespace string) map[string]any {
	return map[string]any{
		"task_id":    d.taskID,
		"trace_id":   d.traceID,
		"group_id":   d.groupID,
		"project_id": d.projectID,
		"user_id":    d.userID,
		"benchmark": map[string]any{
			"id":             d.benchmarkID,
			"name":           d.benchmarkName,
			"image_ref":      d.benchmarkImage,
			"command":        d.benchmarkCommand,
			"container_name": d.benchmarkName,
		},
		"datapack": map[string]any{
			"name":         d.taskID,
			"pre_duration": d.preDuration,
			// pedestal short-code (e.g. "otel-demo") drives BENCHMARK_SYSTEM
			// in algo_execution.go; empty falls back to "ts" and breaks
			// detection on every non-train-ticket datapack.
			"pedestal": d.pedestal,
		},
		"pedestal":     d.pedestal,
		"pre_duration": d.preDuration,
		"namespace":    namespace,
		// dispatcher always runs after SubmitTaskWithDB has persisted the
		// orchestrator-side tasks row keyed by d.taskID, so the chaos hook
		// can safely link FaultInjection.TaskID + ParentTaskID back to it.
		"has_backend_task": true,
	}
}

// testChaosServiceClient is the test seam. Production leaves it nil and falls
// back to defaultChaosServiceClient.
var testChaosServiceClient func() (chaosServiceClient, error)

// chaosServiceInjectionRef tracks the chaos-service ack-id we received for a
// task so the cancel path can route DELETE to the right endpoint. In-process
// only — survives a single orchestrator lifetime, same locality as the legacy
// CR-delete path which also has no cross-process state.
type chaosServiceInjectionRef struct {
	id      string
	isBatch bool
}

var (
	chaosServiceTaskRegistry      = make(map[string]chaosServiceInjectionRef)
	chaosServiceTaskRegistryMutex sync.RWMutex
)

func registerChaosServiceTask(taskID, id string, isBatch bool) {
	if taskID == "" || id == "" {
		return
	}
	chaosServiceTaskRegistryMutex.Lock()
	defer chaosServiceTaskRegistryMutex.Unlock()
	chaosServiceTaskRegistry[taskID] = chaosServiceInjectionRef{id: id, isBatch: isBatch}
}

func lookupChaosServiceTask(taskID string) (chaosServiceInjectionRef, bool) {
	chaosServiceTaskRegistryMutex.RLock()
	defer chaosServiceTaskRegistryMutex.RUnlock()
	ref, ok := chaosServiceTaskRegistry[taskID]
	return ref, ok
}

func unregisterChaosServiceTask(taskID string) {
	chaosServiceTaskRegistryMutex.Lock()
	defer chaosServiceTaskRegistryMutex.Unlock()
	delete(chaosServiceTaskRegistry, taskID)
}

// CancelChaosServiceInjection issues the chaos-service DELETE that matches the
// task's prior dispatch. Returns (false, nil) when the task was not dispatched
// via chaos-service (caller falls back to the legacy CR-delete path). A 404
// from the chaos service is swallowed as idempotent cleanup.
func CancelChaosServiceInjection(ctx context.Context, taskID string) (bool, error) {
	ref, ok := lookupChaosServiceTask(taskID)
	if !ok {
		return false, nil
	}

	clientFactory := defaultChaosServiceClient
	if testChaosServiceClient != nil {
		clientFactory = testChaosServiceClient
	}
	cli, err := clientFactory()
	if err != nil {
		return true, fmt.Errorf("cancel chaos-service injection: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, dispatcherChaosTimeout())
	defer cancel()

	if ref.isBatch {
		err = cli.DeleteInjectionBatch(callCtx, ref.id)
	} else {
		err = cli.DeleteInjection(callCtx, ref.id)
	}
	if errors.Is(err, errChaosServiceNotFound) {
		unregisterChaosServiceTask(taskID)
		return true, nil
	}
	if err != nil {
		return true, fmt.Errorf("cancel chaos-service injection %s: %w", ref.id, err)
	}
	unregisterChaosServiceTask(taskID)
	return true, nil
}
