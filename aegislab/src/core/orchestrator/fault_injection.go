package consumer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/tracing"
	"aegis/platform/utils"

	chaos "aegis/platform/chaos"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

// injectionPayload contains all necessary data for executing a fault injection batch
type injectionPayload struct {
	benchmark     dto.ContainerVersionItem
	preDuration   int
	guidedConfigs []chaos.GuidedConfig
	namespace     string
	pedestal      string
	pedestalID    int
	labels        []dto.LabelItem
	system        string
	chaosInstance string
	chartVersion  string
}

type FaultBatchManager struct {
	mu              sync.RWMutex
	batchCounts     map[string]int
	batchInjections map[string][]string
}

func NewFaultBatchManager() *FaultBatchManager {
	return &FaultBatchManager{
		batchCounts:     make(map[string]int),
		batchInjections: make(map[string][]string),
	}
}

func (bm *FaultBatchManager) deleteBatch(batchID string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	delete(bm.batchCounts, batchID)
	delete(bm.batchInjections, batchID)
}

func (bm *FaultBatchManager) incrementBatchCount(batchID string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.batchCounts[batchID]++
}

func (bm *FaultBatchManager) isFinished(batchID string) bool {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	count, exists := bm.batchCounts[batchID]
	if !exists {
		return true
	}
	injectionNames, exists := bm.batchInjections[batchID]
	if !exists {
		return true
	}

	return count >= len(injectionNames)
}

// snapshotBatchProgress returns (count, expected) for batchID under read
// lock. Originally hooked into the legacy CRD watcher per-leaf log emit
// for hybrid batches (issue #305); now retained because chaos-service
// batch progress could grow the same observability need.
func (bm *FaultBatchManager) snapshotBatchProgress(batchID string) (int, int) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	count := bm.batchCounts[batchID]
	expected := len(bm.batchInjections[batchID])
	return count, expected
}

func (bm *FaultBatchManager) setBatchInjections(batchID string, injectionNames []string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.batchCounts[batchID] = 0
	bm.batchInjections[batchID] = injectionNames
}

// executeFaultInjection drives the guided fault-injection batch through
// the chaos-service dispatcher. The chaos-service catalog-resolves each
// guided spec server-side and returns a structured 400 on bad input, so
// no local BuildInjection pre-flight is run here — a chaos-service
// rejection is treated as the authoritative validation error.
func executeFaultInjection(ctx context.Context, task *dto.UnifiedTask, deps RuntimeDeps) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		batchManager := deps.FaultBatchManager
		if batchManager == nil {
			return fmt.Errorf("fault batch manager is nil")
		}

		span := trace.SpanFromContext(childCtx)
		logEntry := logrus.WithFields(logrus.Fields{
			"task_id":  task.TaskID,
			"trace_id": task.TraceID,
		})

		payload, err := parseInjectionPayload(task.Payload)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to parse injection payload", err)
		}

		monitor := deps.Monitor
		if monitor == nil {
			return handleExecutionError(span, logEntry, "monitor not initialized", fmt.Errorf("monitor not initialized"))
		}

		// §11 step 4.5 — observable cutover: when etcd flag
		// aegis.injection.catalog_source=chaos_service, validate each Point
		// against the chaos service catalog before in-process resolution.
		// Failures fall back silently — the real source of truth remains
		// in-process until step 5b moves the executor.
		//
		// Hoisted above CheckNamespaceToInject (M4): the preflight only reads
		// the catalog over HTTP and does not touch the ns lock store, so
		// running it while holding the lock pins the lock for up to
		// catalogPreflightTimeout per config (≈ 5s × N) and serialises
		// concurrent injects against the same ns for no reason.
		//
		// payload.system is the *logical* system name (e.g. "otel-demo"),
		// matching chaos_points.system_name in the seed manifests. It is
		// deliberately distinct from payload.namespace (e.g. "otel-demo0"),
		// which is the concrete pool-allocated ns the CR is applied into.
		// The catalog is keyed by the logical name; do not substitute the
		// concrete ns here or every preflight will fall through to WARN.
		runCatalogPreflight(childCtx, payload.system, payload.guidedConfigs, deps.DB, logEntry, nil)

		// Default: release the lock on exit. Ownership transfers to the
		// CRD-success/CRD-failed path only after both BatchCreate and
		// CreateInjection succeed; until then every early-return must free
		// the namespace lock or the next inject into this ns will loop on
		// `failed to acquire lock for namespace, retrying`.
		toReleased := false
		if err := monitor.CheckNamespaceToInject(payload.namespace, time.Now(), task.TraceID); err != nil {
			return handleExecutionError(span, logEntry, "failed to get namespace to inject fault", err)
		}
		toReleased = true

		defer func() {
			if toReleased {
				if err := monitor.ReleaseLock(childCtx, payload.namespace, task.TraceID); err != nil {
					if err := handleExecutionError(span, logEntry, fmt.Sprintf("failed to release lock for namespace %s", payload.namespace), err); err != nil {
						logEntry.Error(err)
						return
					}
				}
			}
		}()

		batchID := fmt.Sprintf("batch-%s", utils.GenerateULID(nil))

		// Dispatch via aegis-chaos /v1beta/injections; the webhook receiver
		// completes the BuildDatapack handoff.
		dispDeps := dispatcherDeps{
			taskID:           task.TaskID,
			traceID:          task.TraceID,
			projectID:        task.ProjectID,
			userID:           task.UserID,
			groupID:          task.GroupID,
			pedestal:         payload.pedestal,
			preDuration:      payload.preDuration,
			benchmarkID:      payload.benchmark.ID,
			benchmarkName:    payload.benchmark.Name,
			benchmarkImage:   payload.benchmark.ImageRef,
			benchmarkCommand: payload.benchmark.Command,
			instance:         payload.chaosInstance,
			chartVersion:     payload.chartVersion,
		}
		var names []string
		err = tracing.WithSpanNamed(childCtx, "chaos.batch_create", func(c context.Context) error {
			var werr error
			names, werr = dispatchBatchCreate(c, logEntry, payload.system, payload.namespace, payload.guidedConfigs, dispDeps)
			return werr
		})
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to inject faults", err)
		}
		logEntry.Info("dispatcher: faults submitted")

		var name string
		if len(names) > 1 {
			name = batchID
			batchManager.setBatchInjections(batchID, names)
		} else {
			name = names[0]
		}

		// Emit fault.injection.started so the regression validator's
		// required_events sequence has its bridge event between
		// restart.pedestal.* and datapack.build.started. The webhook
		// receiver (crud/hooks/chaos) is the sole writer of the
		// fault_injections row.
		updateTaskState(childCtx,
			newTaskStateUpdate(
				task.TraceID,
				task.TaskID,
				task.Type,
				consts.TaskRunning,
				fmt.Sprintf("injecting fault for task %s", task.TaskID),
			).withEvent(consts.EventFaultInjectionStarted, dto.FaultInjectionStartedPayload{
				Name:         name,
				ExecutorPath: consts.ExecutorPathChaosService,
			}).withDB(deps.DB).withRedis(deps.RedisGateway),
		)
		// Ownership of the namespace lock passes to the webhook receiver from
		// here on; it will release on terminal status.
		toReleased = false
		return nil
	})
}

// parseInjectionPayload extracts and validates the guided-config payload from
// the task payload used by the fault-injection consumer.
func parseInjectionPayload(payload map[string]any) (*injectionPayload, error) {
	message := "invalid or missing '%s' in task payload"

	benchmark, err := utils.ConvertToType[dto.ContainerVersionItem](payload[consts.InjectBenchmark])
	if err != nil {
		return nil, fmt.Errorf("failed to convert benchmark: %w", err)
	}

	preDurationFloat, ok := payload[consts.InjectPreDuration].(float64)
	if !ok || preDurationFloat <= 0 {
		return nil, fmt.Errorf(message, consts.InjectPreDuration)
	}
	preDuration := int(preDurationFloat)

	guidedConfigs, err := utils.ConvertToType[[]chaos.GuidedConfig](payload[consts.InjectGuidedConfigs])
	if err != nil {
		return nil, fmt.Errorf(message, consts.InjectGuidedConfigs)
	}
	if len(guidedConfigs) == 0 {
		return nil, fmt.Errorf("at least one guided config is required in %s", consts.InjectGuidedConfigs)
	}

	namespace, ok := payload[consts.InjectNamespace].(string)
	if !ok || namespace == "" {
		return nil, fmt.Errorf(message, consts.InjectNamespace)
	}

	pedestalStr, ok := payload[consts.InjectPedestal].(string)
	if !ok || pedestalStr == "" {
		return nil, fmt.Errorf(message, consts.InjectPedestal)
	}
	if !chaos.IsSystemRegistered(pedestalStr) {
		return nil, fmt.Errorf("invalid pedestal type: %s", pedestalStr)
	}

	pedestalIDFloat, ok := payload[consts.InjectPedestalID].(float64)
	if !ok || pedestalIDFloat <= 0 {
		return nil, fmt.Errorf(message, consts.InjectPedestalID)
	}
	pedestalID := int(pedestalIDFloat)

	labels, err := utils.ConvertToType[[]dto.LabelItem](payload[consts.InjectLabels])
	if err != nil {
		return nil, fmt.Errorf(message, consts.InjectLabels)
	}

	system, ok := payload[consts.InjectSystem].(string)
	if !ok || system == "" {
		return nil, fmt.Errorf(message, consts.InjectSystem)
	}

	chaosInstance, _ := payload[consts.InjectChaosInstance].(string)
	if chaosInstance == "" {
		chaosInstance = "seed"
	}
	chartVersion, _ := payload[consts.InjectChartVersion].(string)

	return &injectionPayload{
		benchmark:     benchmark,
		preDuration:   preDuration,
		guidedConfigs: guidedConfigs,
		namespace:     namespace,
		pedestal:      pedestalStr,
		pedestalID:    pedestalID,
		labels:        labels,
		system:        system,
		chaosInstance: chaosInstance,
		chartVersion:  chartVersion,
	}, nil
}
