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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// newOtelHTTPClient returns an *http.Client whose transport propagates W3C
// tracecontext via otelhttp. Backend → chaos-service hops use this so the
// orchestrator trace stitches into the chaos-service spans; without it the
// two services show up as disconnected roots in Jaeger.
func newOtelHTTPClient() *http.Client {
	return &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
}

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
// webhook arrives separately and is handled by crud/hooks/chaos. Cancel is
// task_id-keyed: chaos-service maintains the index server-side, so the
// orchestrator no longer carries a parallel in-process map.
type chaosServiceClient interface {
	CreateInjection(ctx context.Context, body apiclient.ChaosChaosCreateInjectionReq) (string, error)
	CreateInjectionBatch(ctx context.Context, body apiclient.ChaosChaosCreateInjectionBatchReq) (string, error)
	DeleteInjectionByTask(ctx context.Context, taskID string) error
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
	if tok := CurrentChaosSAToken(); tok != "" {
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
	cfg.HTTPClient = newOtelHTTPClient()
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

func (c *sdkChaosServiceClient) DeleteInjectionByTask(ctx context.Context, taskID string) error {
	cli := c.newAPIClient()
	_, httpResp, err := cli.ChaosAPI.ChaosDeleteInjectionByTask(ctx, taskID).Execute()
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
// chaos-service SDK arg.
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
	// these are what the chaos service hashes into the Point ID. They are
	// only used as the fallback when the catalog preflight did not resolve a
	// point_id (in-process / kind mode); see resolvedPointIDs.
	instance     string
	chartVersion string
	// resolvedPointIDs is index-aligned to the guided configs: the point_id
	// the catalog preflight already resolved by natural key
	// (system, service, capability). When non-empty for a config the
	// dispatcher addresses the Point by this id directly instead of
	// recomputing the content hash — the preflight already round-tripped, so
	// recomputing only reintroduces cross-process hash-divergence (the
	// system/namespace/chart_version 404 class). Empty entry → fall back to
	// GuidedChaosPointID (in-process mode / catalog miss).
	resolvedPointIDs []string
}

// pointIDForConfig returns the catalog-resolved point_id for config index i
// when the preflight found one, else the locally derived hash. Centralised so
// the single and batch paths stay in lockstep.
func (d dispatcherDeps) pointIDForConfig(i int, cfg guidedcli.GuidedConfig) (string, error) {
	if i >= 0 && i < len(d.resolvedPointIDs) && d.resolvedPointIDs[i] != "" {
		return d.resolvedPointIDs[i], nil
	}
	pid, _, _, err := chaoscrud.GuidedChaosPointID(cfg, d.instance, d.chartVersion)
	return pid, err
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
		pid, err := deps.pointIDForConfig(0, configs[0])
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
		logEntry.WithFields(logrus.Fields{
			"injection_id": injectionID,
			"point_id":     pid,
		}).Info("dispatcher: chaos service ack received")
		return []string{injectionID}, nil
	}

	batchKey := deps.traceID + ":batch:" + uuid.NewString()
	children := make([]apiclient.ChaosChaosCreateBatchChildReq, 0, len(configs))
	for i, cfg := range configs {
		pid, err := deps.pointIDForConfig(i, cfg)
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

// CancelChaosServiceInjection issues the chaos-service DELETE that matches the
// task's prior dispatch. chaos-service resolves the (ackID, isBatch) tuple
// server-side from the indexed `task_id` column, so the orchestrator no
// longer carries a parallel in-process map.
//
// Returns handled=true when chaos-service owned the cancel (success or hard
// error). Returns handled=false when chaos-service reports the task is
// unknown (404) — that leaves the legacy CR-label cleanup in task/service.go
// to sweep orphaned PodChaos / NetworkChaos rows left behind by pre-migration
// injections, other orchestrator processes, or hand-edited CRs. Returns
// handled=false on empty taskID for the same reason.
func CancelChaosServiceInjection(ctx context.Context, taskID string) (bool, error) {
	if taskID == "" {
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

	err = cli.DeleteInjectionByTask(callCtx, taskID)
	if errors.Is(err, errChaosServiceNotFound) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("cancel chaos-service injection by task %s: %w", taskID, err)
	}
	return true, nil
}
