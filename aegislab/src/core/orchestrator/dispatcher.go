package consumer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"aegis/cli/apiclient"
	chaoscrud "aegis/crud/chaos"
	"aegis/platform/config"

	"github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

// Per-system executor flag lives at
// aegis.injection.system.<system>.executor_authoritative; empty/unknown
// values fall through to chaos-mesh-direct.
const (
	executorFlagKeyPrefix = "aegis.injection.system."
	executorFlagKeySuffix = ".executor_authoritative"

	executorPathChaosMeshDirect = "chaos-mesh-direct"
	executorPathChaosService    = "chaos-service"

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
	Help: "Fault-injection dispatches by executor path and logical system.",
}, []string{"path", "system"})

// executorAuthoritative returns the per-system executor path. Empty / unknown
// values fall through to chaos-mesh-direct.
func executorAuthoritative(system string) string {
	key := executorFlagKeyPrefix + strings.TrimSpace(system) + executorFlagKeySuffix
	switch strings.TrimSpace(config.GetString(key)) {
	case executorPathChaosService:
		return executorPathChaosService
	default:
		return executorPathChaosMeshDirect
	}
}

// chaosServiceClient is the seam tests substitute. Returning the ack-id (one
// per inject, or one batch_id) is enough for the dispatcher — the terminal
// webhook arrives separately and is handled by crud/hooks/chaos.
type chaosServiceClient interface {
	CreateInjection(ctx context.Context, body apiclient.ChaosChaosCreateInjectionReq) (string, error)
	CreateInjectionBatch(ctx context.Context, body apiclient.ChaosChaosCreateInjectionBatchReq) (string, error)
}

var defaultChaosServiceClient = func() (chaosServiceClient, error) {
	url := strings.TrimSpace(config.GetChaosServiceURL())
	if url == "" {
		return nil, errors.New("chaos.service_url is empty; cannot dispatch via chaos service")
	}
	bearer := strings.TrimSpace(os.Getenv("CHAOS_OUTBOUND_BEARER"))
	return &sdkChaosServiceClient{baseURL: url, bearer: bearer}, nil
}

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
	// Must match what aegisctl --via-chaos sends (chaos-instance /
	// chaos-chart-version) or the chaos service will hash a divergent
	// Point ID and reject the inject.
	instance     string
	chartVersion string
}

// dispatchBatchCreate is the §11 step 5b cutover seam. Reads the per-system
// flag; chaos-mesh-direct delegates to the legacy in-process SDK call,
// chaos-service POSTs and waits for ACK. Returns the names the FI orchestrator
// uses to write FaultInjection rows + drive batch tracking.
func dispatchBatchCreate(
	ctx context.Context,
	logEntry *logrus.Entry,
	system, namespace string,
	configs []guidedcli.GuidedConfig,
	confs []handler.InjectionConf,
	annotations, labels map[string]string,
	deps dispatcherDeps,
) ([]string, string, error) {
	path := executorAuthoritative(system)
	injectionDispatchTotal.WithLabelValues(path, system).Inc()

	switch path {
	case executorPathChaosService:
		names, err := dispatchViaChaosService(ctx, logEntry, system, namespace, configs, deps)
		return names, path, err
	default:
		names, err := handler.BatchCreate(ctx, confs, handler.SystemType(system), namespace, annotations, labels)
		return names, executorPathChaosMeshDirect, err
	}
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
		logEntry.WithFields(logrus.Fields{
			"injection_id": injectionID,
			"point_id":     pid,
			"path":         executorPathChaosService,
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
	logEntry.WithFields(logrus.Fields{
		"batch_id": batchID,
		"path":     executorPathChaosService,
	}).Info("dispatcher: chaos service ack received (batch)")
	return []string{batchID}, nil
}

func capabilityOf(cfg guidedcli.GuidedConfig) string {
	return chaoscrud.ChaosTypeToCapability[strings.TrimSpace(cfg.ChaosType)]
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
		},
		"pedestal":     d.pedestal,
		"pre_duration": d.preDuration,
		"namespace":    namespace,
	}
}

// testChaosServiceClient is the test seam. Production leaves it nil and falls
// back to defaultChaosServiceClient.
var testChaosServiceClient func() (chaosServiceClient, error)
