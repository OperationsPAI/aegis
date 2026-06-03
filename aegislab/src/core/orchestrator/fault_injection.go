package consumer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"aegis/core/orchestrator/common"
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	redis "aegis/platform/redis"
	"aegis/platform/tracing"
	"aegis/platform/utils"

	chaos "aegis/platform/chaos"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
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
		resolvedPointIDs := runCatalogPreflight(childCtx, payload.system, payload.guidedConfigs, deps.DB, logEntry, nil)

		// Default: release the lock on exit. Ownership transfers to the
		// CRD-success/CRD-failed path only after both BatchCreate and
		// CreateInjection succeed; until then every early-return must free
		// the namespace lock or the next inject into this ns will loop on
		// `failed to acquire lock for namespace, retrying`.
		toReleased := false
		if err := monitor.CheckNamespaceToInject(payload.namespace, time.Now(), task.TraceID); err != nil {
			// A sibling trace holds the ns lock (the RestartPedestal→FaultInjection
			// release/reacquire window, issue #533). This is transient
			// backpressure, not a permanent failure — reschedule with backoff
			// instead of hard-failing the trace and burning its dedup slot. No
			// lock to release here: the acquire itself failed.
			if errors.Is(err, ErrNamespaceLockContended) {
				return rescheduleFaultInjectionTask(childCtx, deps.DB, deps.RedisGateway, monitor, task, fmt.Sprintf("namespace lock contended: %v, retrying", err))
			}
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
			resolvedPointIDs: resolvedPointIDs,
		}
		var names []string
		err = tracing.WithSpanNamed(childCtx, "chaos.batch_create", func(c context.Context) error {
			var werr error
			names, werr = dispatchBatchCreate(c, logEntry, payload.system, payload.namespace, payload.guidedConfigs, dispDeps)
			return werr
		})
		if err != nil {
			// 429 system-at-capacity (per-system max_concurrent_injections cap,
			// issue #533) is backpressure, not a permanent failure: no chaos CR
			// was created. Release the ns lock so a sibling can make progress,
			// then reschedule with backoff instead of hard-failing the trace.
			// All other dispatch errors (catalog 404, schema 400, system /
			// capability not found) stay terminal.
			if errors.Is(err, errChaosServiceAtCapacity) {
				if relErr := monitor.ReleaseLock(childCtx, payload.namespace, task.TraceID); relErr != nil {
					logEntry.WithError(relErr).Warn("failed to release namespace lock before fault-injection reschedule (continuing)")
				}
				toReleased = false
				return rescheduleFaultInjectionTask(childCtx, deps.DB, deps.RedisGateway, monitor, task, fmt.Sprintf("chaos service at capacity (429): %v, retrying", err))
			}
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
				Name: name,
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
	if !chaos.SystemType(pedestalStr).IsValid() {
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
	// Seeded catalog rows hash with chart_version="seed-genesis" (see
	// manifests/aegis-chaos/<sys>/*.yaml). The RestartPedestal path sets this
	// explicitly, but the --skip-restart-pedestal path bypasses that block,
	// leaving chartVersion="" → GuidedChaosPointID derives a different point_id
	// than the catalog → POST /v1beta/injections 404s. Default to seed-genesis
	// here so every path (RP, skip-RP, direct) addresses the seeded points.
	if chartVersion == "" {
		chartVersion = "seed-genesis"
	}

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

// faultInjectionMaxReschedule resolves the abandon cap for FaultInjection
// reschedules from dynamic config, falling back to the consts default. Read on
// every reschedule so an etcd push applies without a rebuild.
func faultInjectionMaxReschedule() int {
	v := config.GetInt(consts.FaultInjectionMaxRescheduleKey)
	if v <= 0 {
		return consts.DefaultFaultInjectionMaxReschedule
	}
	return v
}

// rescheduleFaultInjectionTask re-enqueues a FaultInjection task that hit
// transient pre-dispatch backpressure (429 system-at-capacity or ns-lock
// contention, issue #533) with exponential backoff and jitter. Once
// task.ReStartNum has reached the abandon cap it stops rescheduling and fails
// the trace so a genuinely unsatisfiable inject fails closed instead of looping
// forever. The namespace lock must already be released by the caller — this
// task re-acquires it on the next attempt.
func rescheduleFaultInjectionTask(ctx context.Context, db *gorm.DB, redisGateway *redis.Gateway, monitor NamespaceMonitor, task *dto.UnifiedTask, reason string) error {
	if task.ReStartNum >= faultInjectionMaxReschedule() {
		return abandonFaultInjectionTask(ctx, db, redisGateway, task, reason)
	}
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(ctx)

		randomFactor := 0.3 + rand.Float64()*0.7
		deltaTime := time.Duration(math.Min(math.Pow(2, float64(task.ReStartNum)), 5.0)*randomFactor) * consts.DefaultTimeUnit
		executeTime := time.Now().Add(deltaTime)

		span.AddEvent(fmt.Sprintf("rescheduling fault injection: %s", reason))
		logrus.WithFields(logrus.Fields{
			"task_id":     task.TaskID,
			"trace_id":    task.TraceID,
			"delay_mins":  deltaTime.Minutes(),
			"retry_count": task.ReStartNum + 1,
		}).Warnf("%s: %s", reason, executeTime)

		tracing.SetSpanAttribute(ctx, consts.TaskStateKey, consts.GetTaskStateName(consts.TaskPending))

		updateTaskState(ctx,
			newTaskStateUpdate(
				task.TraceID,
				task.TaskID,
				consts.TaskTypeFaultInjection,
				consts.TaskRescheduled,
				reason,
			).withEvent(consts.EventNoNamespaceAvailable, executeTime.String()).withDB(db).withRedis(redisGateway),
		)

		task.Reschedule(executeTime)
		if err := common.SubmitTaskWithDB(ctx, db, redisGateway, task); err != nil {
			span.RecordError(err)
			span.AddEvent("failed to submit rescheduled fault injection task")
			return fmt.Errorf("failed to submit rescheduled fault injection task: %w", err)
		}

		return nil
	})
}

// abandonFaultInjectionTask finalizes a FaultInjection task that has exhausted
// its reschedule budget (issue #533): it marks the task errored and the trace
// failed and emits fault.injection.failed. The namespace lock is already
// released by the caller before each reschedule, so there is nothing to release
// here.
func abandonFaultInjectionTask(ctx context.Context, db *gorm.DB, redisGateway *redis.Gateway, task *dto.UnifiedTask, reason string) error {
	logEntry := logrus.WithFields(logrus.Fields{
		"task_id":     task.TaskID,
		"trace_id":    task.TraceID,
		"retry_count": task.ReStartNum,
	})
	message := fmt.Sprintf("fault injection abandoned after %d reschedules: %s", task.ReStartNum, reason)
	logEntry.Warn(message)

	updateTaskState(ctx,
		newTaskStateUpdate(
			task.TraceID,
			task.TaskID,
			consts.TaskTypeFaultInjection,
			consts.TaskError,
			message,
		).withEvent(consts.EventFaultInjectionFailed, message).withDB(db).withRedis(redisGateway),
	)

	if err := db.WithContext(ctx).Model(&model.Trace{}).
		Where("id = ? AND state = ?", task.TraceID, consts.TraceRunning).
		Updates(map[string]any{
			"state":      consts.TraceFailed,
			"last_event": consts.EventFaultInjectionFailed,
			"end_time":   time.Now(),
		}).Error; err != nil {
		logEntry.WithError(err).Warn("failed to mark trace failed on fault-injection abandon (continuing)")
	}
	return nil
}
