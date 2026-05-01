package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"
	injection "aegis/module/injection"
	"aegis/tracing"
	"aegis/utils"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

// injectionPayload contains all necessary data for executing a fault injection batch
type injectionPayload struct {
	benchmark     dto.ContainerVersionItem
	preDuration   int
	guidedConfigs []guidedcli.GuidedConfig
	namespace     string
	pedestal      string
	pedestalID    int
	labels        []dto.LabelItem
	system        string
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
// lock. Used by HandleCRDSucceeded to log per-leaf progress so a hybrid
// batch stuck waiting on a leaf whose CRD informer never fires (issue
// #305) is observable rather than silent.
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

// executeFaultInjection handles the guided fault-injection batch path:
// GuidedConfig -> guidedcli.BuildInjection -> handler.InjectionConf -> BatchCreate.
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

		batchLen := len(payload.guidedConfigs)
		injectionConfs := make([]chaos.InjectionConf, 0, batchLen)
		displayMaps := make([]map[string]any, 0, batchLen)
		groundtruths := make([]model.Groundtruth, 0, batchLen)

		for i, cfg := range payload.guidedConfigs {
			conf, _, err := guidedcli.BuildInjection(childCtx, cfg)
			if err != nil {
				return handleExecutionError(span, logEntry, fmt.Sprintf("failed to build guided injection %d", i), err)
			}
			displayMap, err := conf.GetDisplayConfig(childCtx)
			if err != nil {
				return handleExecutionError(span, logEntry, fmt.Sprintf("failed to get display config for guided config %d", i), err)
			}
			chaosGroundtruth, err := conf.GetGroundtruth(childCtx)
			if err != nil {
				return handleExecutionError(span, logEntry, fmt.Sprintf("failed to get groundtruth for guided config %d", i), err)
			}
			injectionConfs = append(injectionConfs, conf)
			displayMaps = append(displayMaps, displayMap)
			groundtruths = append(groundtruths, *model.NewDBGroundtruth(&chaosGroundtruth))
		}

		// Re-capture groundtruth right before applying CRDs. The first pass
		// above runs at the top of the FI handler — by the time annotations,
		// labels, and the batch ID are wired up, RestartPedestal's helm-upgrade
		// may have rolled the targeted pod and renamed it (the chaos-mesh CRD
		// selector is label-based so the FI still lands, but the DB-recorded
		// GT.Pod was the OLD name). Re-resolving here pins fresh pod / container
		// names without changing the action-space (service-level groundtruth is
		// expected to be stable; a service mismatch is logged but we proceed
		// with the fresh values — the CRD is what's actually about to run).
		for i, conf := range injectionConfs {
			confCopy := conf
			fresh, mismatch, err := recaptureGroundtruth(childCtx, func(ctx context.Context) (chaos.Groundtruth, error) {
				return confCopy.GetGroundtruth(ctx)
			}, groundtruths[i])
			if err != nil {
				return handleExecutionError(span, logEntry, fmt.Sprintf("failed to re-capture groundtruth for guided config %d", i), err)
			}
			if mismatch {
				logEntry.WithFields(logrus.Fields{
					"index":           i,
					"prior_service":   groundtruths[i].Service,
					"fresh_service":   fresh.Service,
					"prior_pod":       groundtruths[i].Pod,
					"fresh_pod":       fresh.Pod,
					"prior_container": groundtruths[i].Container,
					"fresh_container": fresh.Container,
				}).Warn("groundtruth service set changed between submit and CRD apply; persisting fresh values")
			}
			groundtruths[i] = fresh
		}

		// Marshal display config as array
		displayData, err := json.Marshal(displayMaps)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to marshal injection specs to display config", err)
		}

		engineData, err := json.Marshal(payload.guidedConfigs)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to marshal injection specs to engine config", err)
		}

		annotations, err := task.GetAnnotations(childCtx)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to get annotations", err)
		}

		itemJson, err := json.Marshal(payload.benchmark)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to marshal benchmark item", err)
		}
		annotations[consts.CRDAnnotationBenchmark] = string(itemJson)

		batchID := fmt.Sprintf("batch-%s", utils.GenerateULID(nil))
		isHybrid := len(payload.guidedConfigs) > 1
		crdLabels := utils.MergeSimpleMaps(
			task.GetLabels(),
			map[string]string{
				consts.K8sLabelAppID:    consts.AppID,
				consts.CRDLabelBatchID:  batchID,
				consts.CRDLabelIsHybrid: strconv.FormatBool(isHybrid),
			},
		)

		// Batch create all fault injections in parallel
		names, err := chaos.BatchCreate(childCtx, injectionConfs, chaos.SystemType(payload.system), payload.namespace, annotations, crdLabels)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to inject faults", err)
		}

		var name string
		var faultType chaos.ChaosType
		if len(names) > 1 {
			name = batchID
			faultType = consts.Hybrid
			batchManager.setBatchInjections(batchID, names)
		} else {
			name = names[0]
			if ft, ok := chaos.ChaosNameMap[payload.guidedConfigs[0].ChaosType]; ok {
				faultType = ft
			} else {
				faultType = consts.Hybrid
			}
		}

		if deps.InjectionOwner == nil {
			return handleExecutionError(span, logEntry, "injection owner service is nil", fmt.Errorf("missing injection owner service"))
		}

		_, err = deps.InjectionOwner.CreateInjection(childCtx, &injection.RuntimeCreateInjectionReq{
			Name:              name,
			FaultType:         faultType,
			Category:          chaos.SystemType(payload.pedestal),
			Description:       fmt.Sprintf("Fault batch for task %s (%d faults)", task.TaskID, len(payload.guidedConfigs)),
			DisplayConfig:     string(displayData),
			EngineConfig:      string(engineData),
			Groundtruths:      groundtruths,
			GroundtruthSource: consts.GroundtruthSourceAuto,
			PreDuration:       payload.preDuration,
			TaskID:            task.TaskID,
			BenchmarkID:       utils.IntPtr(payload.benchmark.ID),
			PedestalID:        utils.IntPtr(payload.pedestalID),
			Labels:            payload.labels,
			State:             consts.DatapackInitial,
		})
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to write fault injection schedule to owner service", err)
		}
		// Ownership of the namespace lock passes to the CRD controller from
		// here on; it will release on CRD success/failure (k8s_handler).
		toReleased = false
		return nil
	})
}

// recaptureGroundtruth re-invokes getter at fault-injection time and returns
// the freshly-resolved groundtruth plus a serviceMismatch flag. Callers persist
// the fresh value; mismatch only signals that the action's service set changed
// between the early loop-time call and CRD apply (a known-bad sign that one of
// the layers below — pod-listing, container-name resolution — saw a different
// state). The chaos-mesh selector is label-based so the CRD still lands; the
// fresh GT is what consumers like the datapack builder should record.
//
// Extracted as a getter-typed helper so unit tests can drive both passes
// without standing up a real cluster (the K8s-touching work all lives inside
// the spec's GetGroundtruth).
func recaptureGroundtruth(ctx context.Context, getter func(context.Context) (chaos.Groundtruth, error), prior model.Groundtruth) (model.Groundtruth, bool, error) {
	fresh, err := getter(ctx)
	if err != nil {
		return model.Groundtruth{}, false, err
	}
	freshDB := *model.NewDBGroundtruth(&fresh)
	return freshDB, !sameStringSet(prior.Service, freshDB.Service), nil
}

// sameStringSet returns true when a and b contain the same elements ignoring
// order and duplicates. Used by recaptureGroundtruth to detect cross-pass
// service-set drift (which is structural, not a pod-rename).
func sameStringSet(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	as = dedupSorted(as)
	bs = dedupSorted(bs)
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func dedupSorted(s []string) []string {
	if len(s) < 2 {
		return s
	}
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
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

	guidedConfigs, err := utils.ConvertToType[[]guidedcli.GuidedConfig](payload[consts.InjectGuidedConfigs])
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

	return &injectionPayload{
		benchmark:     benchmark,
		preDuration:   preDuration,
		guidedConfigs: guidedConfigs,
		namespace:     namespace,
		pedestal:      pedestalStr,
		pedestalID:    pedestalID,
		labels:        labels,
		system:        system,
	}, nil
}
