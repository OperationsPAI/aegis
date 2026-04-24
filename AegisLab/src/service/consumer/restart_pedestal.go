package consumer

import (
	"aegis/config"
	"aegis/consts"
	"aegis/dto"
	helm "aegis/infra/helm"
	k8s "aegis/infra/k8s"
	redis "aegis/infra/redis"
	"aegis/service/common"
	"aegis/tracing"
	"aegis/utils"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// defaultHelmRepoURL resolves a repository URL for the given repo_name from
// etcd-backed dynamic config (`helm.repo.<name>.url`). Used when
// helm_configs.repo_url is empty — lets RestartPedestal self-install the chart
// without requiring the operator to pre-stage a local tgz.
//
// Returns an empty string when no override is configured; caller then falls
// back to the local tgz (if any) or fails with a clear error.
func defaultHelmRepoURL(name string) string {
	if name == "" {
		return ""
	}
	return config.GetString(fmt.Sprintf("helm.repo.%s.url", name))
}

// helmInstallTimeouts resolves the (overall, k8s-wait) timeouts used when
// helm-installing a pedestal chart. Defaults are 1800s overall / 600s wait,
// overridable via dynamic config keys so ops can retune without a rebuild:
//
//	helm.restart_pedestal.timeout_seconds       (overall install timeout)
//	helm.restart_pedestal.wait_timeout_seconds  (k8s readiness wait timeout)
//
// Non-positive values fall back to the defaults.
func helmInstallTimeouts() (time.Duration, time.Duration) {
	overall := 1800 * time.Second
	wait := 600 * time.Second
	if v := config.GetInt("helm.restart_pedestal.timeout_seconds"); v > 0 {
		overall = time.Duration(v) * time.Second
	}
	if v := config.GetInt("helm.restart_pedestal.wait_timeout_seconds"); v > 0 {
		wait = time.Duration(v) * time.Second
	}
	return overall, wait
}

func restartWorkloadReadyTimeout() time.Duration {
	timeout := 600 * time.Second
	if v := config.GetInt("restart_pedestal.workload_ready_timeout_seconds"); v > 0 {
		timeout = time.Duration(v) * time.Second
	}
	return timeout
}

func restartPostReadySoakDuration() time.Duration {
	soak := 20 * time.Second
	if v := config.GetInt("restart_pedestal.post_ready_soak_seconds"); v >= 0 {
		soak = time.Duration(v) * time.Second
	}
	return soak
}

func extractPreDuration(injectPayload map[string]any) time.Duration {
	if injectPayload == nil {
		return 0
	}
	raw, ok := injectPayload[consts.InjectPreDuration]
	if !ok || raw == nil {
		return 0
	}

	switch v := raw.(type) {
	case float64:
		if v > 0 {
			return time.Duration(v) * time.Minute
		}
	case float32:
		if v > 0 {
			return time.Duration(v) * time.Minute
		}
	case int:
		if v > 0 {
			return time.Duration(v) * time.Minute
		}
	case int64:
		if v > 0 {
			return time.Duration(v) * time.Minute
		}
	}
	return 0
}

func waitForPedestalWorkloadReady(ctx context.Context, gateway *k8s.Gateway, namespace string) (time.Time, error) {
	if gateway == nil {
		logrus.Warnf("k8s gateway is nil; skipping workload-ready wait for namespace %q", namespace)
		return time.Now(), nil
	}

	timeout := restartWorkloadReadyTimeout()
	if err := gateway.WaitForNamespacePodsReady(ctx, namespace, timeout); err != nil {
		return time.Time{}, err
	}

	soak := restartPostReadySoakDuration()
	if soak <= 0 {
		return time.Now(), nil
	}

	timer := time.NewTimer(soak)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	case <-timer.C:
		return time.Now(), nil
	}
}

func adjustInjectTimeAfterWarmup(injectTime, warmupReadyAt time.Time, injectPayload map[string]any) time.Time {
	minInjectTime := warmupReadyAt.Add(extractPreDuration(injectPayload))
	if injectTime.Before(minInjectTime) {
		return minInjectTime
	}
	return injectTime
}

type restartPayload struct {
	pedestal      dto.ContainerVersionItem
	interval      int
	faultDuration int
	injectPayload map[string]any
	skipInstall   bool
	// requiredNamespace, when non-empty, pins this RestartPedestal task to a
	// specific namespace instead of picking one from the NsPattern pool.
	// Populated by SubmitFaultInjection whenever a guided config names a
	// namespace — see #156 for the silent-fallback bug this fixes.
	requiredNamespace string
}

// executeRestartPedestal handles the execution of a restart pedestal task
func executeRestartPedestal(ctx context.Context, task *dto.UnifiedTask, deps RuntimeDeps) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(childCtx)
		span.AddEvent(fmt.Sprintf("Starting restarting pedestal attempt %d", task.ReStartNum+1))
		logEntry := logrus.WithFields(logrus.Fields{
			"task_id":  task.TaskID,
			"trace_id": task.TraceID,
		})
		helmGateway := deps.HelmGateway
		if helmGateway == nil {
			return handleExecutionError(span, logEntry, "helm gateway not initialized", fmt.Errorf("helm gateway not initialized"))
		}
		redisGateway := deps.RedisGateway
		if redisGateway == nil {
			return handleExecutionError(span, logEntry, "redis gateway not initialized", fmt.Errorf("redis gateway not initialized"))
		}

		rateLimiter := deps.RestartRateLimiter
		if rateLimiter == nil {
			return handleExecutionError(span, logEntry, "restart pedestal rate limiter not initialized", errors.New("restart pedestal rate limiter not initialized"))
		}
		acquired, err := rateLimiter.AcquireToken(childCtx, task.TaskID, task.TraceID)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to acquire rate limit token", err)
		}

		if !acquired {
			span.AddEvent("no token available, waiting")
			logEntry.Warn("No restart pedestal token available, waiting...")

			acquired, err = rateLimiter.WaitForToken(childCtx, task.TaskID, task.TraceID)
			if err != nil {
				return handleExecutionError(span, logEntry, "failed to wait for token", err)
			}

			if !acquired {
				if err := rescheduleRestartPedestalTask(childCtx, deps.DB, redisGateway, task, "rate limited, retrying later"); err != nil {
					return err
				}
				return nil
			}
		}

		payload, err := parseRestartPayload(task.Payload)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to parse restart payload", err)
		}

		system := chaos.SystemType(payload.pedestal.ContainerName)
		if !system.IsValid() {
			return handleExecutionError(span, logEntry, fmt.Sprintf("invalid pedestal system type: %s", payload.pedestal.Name), fmt.Errorf("invalid pedestal system type: %s", payload.pedestal.Name))
		}

		cfg, exists := config.GetChaosSystemConfigManager().Get(system)
		if !exists {
			return handleExecutionError(span, logEntry, fmt.Sprintf("no configuration found for system type: %s", system), fmt.Errorf("no configuration found for system type: %s", system))
		}

		monitor := deps.Monitor
		if monitor == nil {
			return handleExecutionError(span, logEntry, "monitor not initialized", errors.New("monitor not initialized"))
		}

		toReleased := false

		var namespace string
		defer func() {
			if acquired {
				if releaseErr := rateLimiter.ReleaseToken(childCtx, task.TaskID, task.TraceID); releaseErr != nil {
					logEntry.Errorf("failed to release restart pedestal token: %v", releaseErr)
				}
			}
			if toReleased && namespace != "" {
				if err := monitor.ReleaseLock(childCtx, namespace, task.TraceID); err != nil {
					if err := handleExecutionError(span, logEntry, fmt.Sprintf("failed to release lock for namespace %s", namespace), err); err != nil {
						logEntry.Error(err)
						return
					}
				}
			}
		}()

		t := time.Now()
		deltaTime := time.Duration(payload.interval) * consts.DefaultTimeUnit
		lockEndTime := t.Add(deltaTime)

		// #156: honor the guided-submitted namespace as a hard constraint.
		// Without this branch, GetNamespaceToRestart would iterate every
		// enabled ns matching cfg.NsPattern and silently downgrade a
		// `sockshop14` request to `sockshop0`. If the required ns is not
		// (yet) registered in the chaos-system config, AcquireLock will
		// return a clear "not found in current configuration" error — the
		// user's next step is to bump that system's Count and retry, not
		// have the submit quietly reroute.
		if payload.requiredNamespace != "" {
			rx, patternErr := regexp.Compile(cfg.NsPattern)
			if patternErr != nil {
				toReleased = false
				return handleExecutionError(span, logEntry,
					fmt.Sprintf("invalid NsPattern %q for system %s", cfg.NsPattern, system),
					patternErr)
			}
			if !rx.MatchString(payload.requiredNamespace) {
				toReleased = false
				return handleExecutionError(span, logEntry,
					fmt.Sprintf("required namespace %q does not match system %s NsPattern %q",
						payload.requiredNamespace, system, cfg.NsPattern),
					fmt.Errorf("namespace/system mismatch"))
			}

			if lockErr := monitor.AcquireNamespaceForRestart(payload.requiredNamespace, lockEndTime, task.TraceID); lockErr != nil {
				if releaseErr := rateLimiter.ReleaseToken(childCtx, task.TaskID, task.TraceID); releaseErr != nil {
					logEntry.Errorf("failed to release restart pedestal token after required-namespace lock failure: %v", releaseErr)
				}
				acquired = false
				reason := fmt.Sprintf("failed to acquire lock for required namespace %s: %v, retrying", payload.requiredNamespace, lockErr)
				if err := rescheduleRestartPedestalTask(childCtx, deps.DB, redisGateway, task, reason); err != nil {
					return err
				}
				return nil
			}
			namespace = payload.requiredNamespace
		} else {
			namespace = monitor.GetNamespaceToRestart(lockEndTime, cfg.NsPattern, task.TraceID)
			if namespace == "" {
				// Failed to acquire namespace lock, immediately release rate limit token
				if releaseErr := rateLimiter.ReleaseToken(childCtx, task.TaskID, task.TraceID); releaseErr != nil {
					logEntry.Errorf("failed to release restart pedestal token after namespace lock failure: %v", releaseErr)
				}

				acquired = false
				if err := rescheduleRestartPedestalTask(childCtx, deps.DB, redisGateway, task, "failed to acquire lock for namespace, retrying"); err != nil {
					return err
				}

				return nil
			}
		}

		deltaTime = time.Duration(payload.interval-payload.faultDuration) * consts.DefaultTimeUnit
		injectTime := t.Add(deltaTime)

		index, err := cfg.ExtractNsNumber(namespace)
		if err != nil {
			toReleased = true
			return handleExecutionError(span, logEntry, "failed to read namespace index", err)
		}

		updateTaskState(childCtx,
			newTaskStateUpdate(
				task.TraceID,
				task.TaskID,
				consts.TaskTypeRestartPedestal,
				consts.TaskRunning,
				fmt.Sprintf("Restarting pedestal in namespace %s", namespace),
			).withSimpleEvent(consts.EventRestartPedestalStarted).withDB(deps.DB).withRedis(redisGateway),
		)

		if payload.pedestal.Extra == nil {
			toReleased = true
			publishEvent(redisGateway, childCtx, fmt.Sprintf(consts.StreamTraceLogKey, task.TraceID), dto.TraceStreamEvent{
				TaskID:    task.TaskID,
				TaskType:  consts.TaskTypeRestartPedestal,
				EventName: consts.EventRestartPedestalFailed,
				Payload:   "missing extra info in pedestal item",
			})

			return handleExecutionError(span, logEntry, "missing extra info in pedestal item", fmt.Errorf("missing extra info in pedestal item"))
		}

		// Skip the helm install when the caller has pre-installed the chart
		// out-of-band (e.g. `aegisctl pedestal chart install` +
		// wait-for-ready) and the release is already deployed. Namespace
		// lock, index extraction, and the FaultInjection handoff below still
		// run unchanged. Falls through to a real install if the release is
		// missing, in a non-deployed state, or the status check errors out.
		skippedInstall := false
		if payload.skipInstall {
			deployed, checkErr := helmGateway.IsReleaseDeployed(namespace, namespace)
			if checkErr != nil {
				logEntry.Warnf("skip_install requested but status check failed (%v); falling back to install", checkErr)
			} else if deployed {
				logEntry.Infof("skip_install: release %s/%s already deployed; skipping helm install", namespace, namespace)
				skippedInstall = true
			} else {
				logEntry.Infof("skip_install requested but release %s/%s not deployed; installing", namespace, namespace)
			}
		}

		if !skippedInstall {
			if err := installPedestal(childCtx, helmGateway, namespace, index, payload.pedestal.Extra); err != nil {
				toReleased = true
				publishEvent(redisGateway, childCtx, fmt.Sprintf(consts.StreamTraceLogKey, task.TraceID), dto.TraceStreamEvent{
					TaskID:    task.TaskID,
					TaskType:  consts.TaskTypeRestartPedestal,
					EventName: consts.EventRestartPedestalFailed,
					Payload:   err.Error(),
				})

				return handleExecutionError(span, logEntry, fmt.Sprintf("failed to install pedestal of system %s", system), err)
			}
		}

		warmupReadyAt, err := waitForPedestalWorkloadReady(childCtx, deps.K8sGateway, namespace)
		if err != nil {
			toReleased = true
			return handleExecutionError(span, logEntry, "workload readiness/warmup wait failed", err)
		}
		adjustedInjectTime := adjustInjectTimeAfterWarmup(injectTime, warmupReadyAt, payload.injectPayload)
		if !adjustedInjectTime.Equal(injectTime) {
			logEntry.WithFields(logrus.Fields{
				"old_inject_time": injectTime.String(),
				"new_inject_time": adjustedInjectTime.String(),
				"pre_duration":    extractPreDuration(payload.injectPayload).String(),
			}).Warn("inject time adjusted to guarantee warm-up and normal-window coverage")
			injectTime = adjustedInjectTime
		}

		message := fmt.Sprintf("Injection start at %s, duration %dm", injectTime.Local().String(), payload.faultDuration)
		updateTaskState(childCtx,
			newTaskStateUpdate(
				task.TraceID,
				task.TaskID,
				consts.TaskTypeRestartPedestal,
				consts.TaskCompleted,
				message,
			).withEvent(consts.EventRestartPedestalCompleted, message).withDB(deps.DB).withRedis(redisGateway),
		)

		tracing.SetSpanAttribute(childCtx, consts.TaskStateKey, consts.GetTaskStateName(consts.TaskCompleted))

		payload.injectPayload[consts.InjectNamespace] = namespace
		payload.injectPayload[consts.InjectPedestal] = system
		payload.injectPayload[consts.InjectPedestalID] = payload.pedestal.ID

		if err := common.ProduceFaultInjectionTasksWithDB(childCtx, deps.DB, deps.RedisGateway, task, injectTime, payload.injectPayload); err != nil {
			toReleased = true
			return handleExecutionError(span, logEntry, "failed to submit inject task", err)
		}

		return nil
	})
}

// rescheduleRestartPedestalTask reschedules a pedestal restart task with exponential backoff and jitter
func rescheduleRestartPedestalTask(ctx context.Context, db *gorm.DB, redisGateway *redis.Gateway, task *dto.UnifiedTask, reason string) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(ctx)

		randomFactor := 0.3 + rand.Float64()*0.7 // Random factor between 0.3 and 1.0
		deltaTime := time.Duration(math.Min(math.Pow(2, float64(task.ReStartNum)), 5.0)*randomFactor) * consts.DefaultTimeUnit
		executeTime := time.Now().Add(deltaTime)

		span.AddEvent(fmt.Sprintf("rescheduling task: %s", reason))
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
				consts.TaskTypeRestartPedestal,
				consts.TaskRescheduled,
				reason,
			).withEvent(consts.EventNoNamespaceAvailable, executeTime.String()).withDB(db).withRedis(redisGateway),
		)

		task.Reschedule(executeTime)
		if err := common.SubmitTaskWithDB(ctx, db, redisGateway, task); err != nil {
			span.RecordError(err)
			span.AddEvent("failed to submit rescheduled task")
			return fmt.Errorf("failed to submit rescheduled restart task: %w", err)
		}

		return nil
	})
}

// parseRestartPayload parses the payload for a restart pedestal task
func parseRestartPayload(payload map[string]any) (*restartPayload, error) {
	message := "invalid or missing '%s' in task payload"

	pedestal, err := utils.ConvertToType[dto.ContainerVersionItem](payload[consts.RestartPedestal])
	if err != nil {
		return nil, fmt.Errorf(message, consts.RestartPedestal)
	}

	intervalFloat, ok := payload[consts.RestartIntarval].(float64)
	if !ok || intervalFloat <= 0 {
		return nil, fmt.Errorf(message, consts.RestartIntarval)
	}
	interval := int(intervalFloat)

	faultDurationFloat, ok := payload[consts.RestartFaultDuration].(float64)
	if !ok || faultDurationFloat <= 0 {
		return nil, fmt.Errorf(message, consts.RestartFaultDuration)
	}
	faultDuration := int(faultDurationFloat)

	injectPayload, ok := payload[consts.RestartInjectPayload].(map[string]any)
	if !ok {
		return nil, fmt.Errorf(message, consts.RestartInjectPayload)
	}

	// skipInstall is optional — absent or non-bool payloads fall through to
	// "run the helm install normally".
	skipInstall, _ := payload[consts.RestartSkipInstall].(bool)

	// requiredNamespace is optional. When set (guided submit carried a
	// user-specified namespace) we bypass pool selection; see #156.
	requiredNamespace, _ := payload[consts.RestartRequiredNamespace].(string)

	return &restartPayload{
		pedestal:          pedestal,
		interval:          interval,
		faultDuration:     faultDuration,
		injectPayload:     injectPayload,
		skipInstall:       skipInstall,
		requiredNamespace: strings.TrimSpace(requiredNamespace),
	}, nil
}

// installPedestal installs or upgrades the pedestal using Helm
// Priority: Remote (if configured) -> Local fallback (if remote fails and LocalPath is set)
func installPedestal(ctx context.Context, gateway *helm.Gateway, releaseName string, namespaceIdx int, item *dto.HelmConfigItem) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(childCtx)
		logEntry := logrus.WithFields(logrus.Fields{
			"release_name":  releaseName,
			"namespace_idx": namespaceIdx,
		})

		if item == nil {
			return handleExecutionError(span, logEntry, "missing helm config in container extra info", fmt.Errorf("missing helm config in container extra info"))
		}

		paramItems := item.DynamicValues
		for i := range paramItems {
			if paramItems[i].TemplateString != "" {
				paramItems[i].Value = fmt.Sprintf(paramItems[i].TemplateString, namespaceIdx)
			}
		}

		helmValues := item.GetValuesMap()

		// Determine chart source and installation strategy.
		// local_path is only usable if the file actually exists — a missing
		// pre-staged tgz (common after pod restart since /tmp is ephemeral)
		// should fall through to a remote install instead of hard-failing.
		hasLocal := item.LocalPath != ""
		if hasLocal {
			if _, err := os.Stat(item.LocalPath); err != nil {
				logEntry.Warnf("local chart %q not accessible (%v); will try remote install", item.LocalPath, err)
				hasLocal = false
			}
		}

		// If the operator didn't record a repo_url, try the etcd-backed
		// override `helm.repo.<repo_name>.url`. Lets ops swap defaults at
		// runtime without a DB migration.
		if item.RepoURL == "" && item.RepoName != "" {
			if url := defaultHelmRepoURL(item.RepoName); url != "" {
				logEntry.Infof("helm_configs.repo_url empty for %q; using etcd override %q", item.RepoName, url)
				item.RepoURL = url
			}
		}

		hasRemote := item.RepoURL != "" && item.RepoName != ""

		var installErr error

		if hasRemote {
			logEntry.Infof("Attempting to install chart from remote repository: %s/%s", item.RepoName, item.ChartName)

			isOCI := strings.HasPrefix(item.RepoURL, "oci://")
			var fullChart string
			if isOCI {
				// OCI registries don't expose an index.yaml; skip AddRepo/UpdateRepo
				// and let installAction.LocateChart pull the OCI reference directly.
				fullChart = strings.TrimRight(item.RepoURL, "/") + "/" + item.ChartName
			} else if err := gateway.AddRepo(releaseName, item.RepoName, item.RepoURL); err != nil {
				logEntry.Warnf("Failed to add repository: %v", err)
				installErr = err
			} else if err := gateway.UpdateRepo(releaseName, item.RepoName); err != nil {
				logEntry.Warnf("Failed to update repository: %v", err)
				installErr = err
			} else {
				fullChart = fmt.Sprintf("%s/%s", item.RepoName, item.ChartName)
			}

			if installErr == nil && fullChart != "" {
				logrus.WithFields(logrus.Fields{
					"release_name": releaseName,
					"chart":        fullChart,
					"version":      item.Version,
					"namespace":    releaseName,
				}).Infof("Installing Helm chart from remote with parameters: %+v", helmValues)

				overallTO, waitTO := helmInstallTimeouts()
				if err := gateway.Install(ctx,
					releaseName,
					releaseName,
					fullChart,
					item.Version,
					helmValues,
					overallTO,
					waitTO,
				); err != nil {
					logEntry.Warnf("Failed to install chart from remote: %v", err)
					installErr = err
				} else {
					logEntry.Info("Helm chart installed successfully from remote repository")
					return nil
				}
			}
		}

		// Fallback to local chart if remote failed or not configured
		if hasLocal {
			if installErr != nil {
				logEntry.Infof("Remote installation failed, falling back to local chart: %s", item.LocalPath)
			} else {
				logEntry.Infof("Installing chart from local path: %s", item.LocalPath)
			}

			logrus.WithFields(logrus.Fields{
				"release_name": releaseName,
				"chart":        item.LocalPath,
				"namespace":    releaseName,
			}).Infof("Installing Helm chart from local path with parameters: %+v", helmValues)

			overallTO, waitTO := helmInstallTimeouts()
			if err := gateway.Install(ctx,
				releaseName,
				releaseName,
				item.LocalPath,
				item.Version,
				helmValues,
				overallTO,
				waitTO,
			); err != nil {
				return fmt.Errorf("failed to install chart from local path %s: %w", item.LocalPath, err)
			}

			logEntry.Info("Helm chart installed successfully from local path")
			return nil
		}

		// No valid source available
		if installErr != nil {
			return fmt.Errorf("failed to install chart: remote installation failed and no local fallback available: %w", installErr)
		}

		return fmt.Errorf("no chart source configured (neither remote nor local)")
	})
}
