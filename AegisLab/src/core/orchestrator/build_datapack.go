package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	corev1 "k8s.io/api/core/v1"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	db "aegis/platform/db"
	k8s "aegis/platform/k8s"
	redis "aegis/platform/redis"
	"aegis/platform/tracing"
	"aegis/core/orchestrator/common"
	"aegis/platform/utils"
)

type datapackPayload struct {
	benchmark        dto.ContainerVersionItem
	datapack         dto.InjectionItem
	datasetVersionID *int
	labels           []dto.LabelItem
}

type datapackJobCreationParams struct {
	jobName     string
	image       string
	annotations map[string]string
	labels      map[string]string
	payload     *datapackPayload
	dbConfig    *db.DatabaseConfig
}

func (p *datapackJobCreationParams) toK8sJobConfig(envVars []corev1.EnvVar, volumeMountConfigs []k8s.VolumeMountConfig) *k8s.JobConfig {
	return &k8s.JobConfig{
		JobName:            p.jobName,
		Image:              p.image,
		Command:            strings.Split(p.payload.benchmark.Command, " "),
		EnvVars:            envVars,
		Annotations:        p.annotations,
		Labels:             p.labels,
		VolumeMountConfigs: volumeMountConfigs,
		ServiceAccountName: config.GetString("k8s.job.service_account.name"),
	}
}

func executeBuildDatapackWithDeps(ctx context.Context, task *dto.UnifiedTask, deps RuntimeDeps) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(childCtx)
		logEntry := logrus.WithFields(logrus.Fields{"task_id": task.TaskID, "trace_id": task.TraceID})
		k8sGateway := deps.K8sGateway
		if k8sGateway == nil {
			return handleExecutionError(span, logEntry, "k8s gateway not initialized", fmt.Errorf("k8s gateway not initialized"))
		}
		redisGateway := deps.RedisGateway
		if redisGateway == nil {
			return handleExecutionError(span, logEntry, "redis gateway not initialized", fmt.Errorf("redis gateway not initialized"))
		}

		// Gate the BuildDatapack fan-out behind a token bucket. Each Job
		// fires ~30 ClickHouse queries via rcabench-platform's
		// prepare_inputs.py; without this gate, the autonomous inject-loop
		// has crossed ClickHouse's max_concurrent_queries ceiling and
		// triggered "Code 202: Too many simultaneous queries" cascades.
		// Mirrors the BuildContainer / AlgoExecution guards exactly so
		// retry / wait / reschedule semantics are uniform across task
		// types. The token is held for the lifetime of the K8s Job and
		// released by the job-callback path in k8s_handler.go.
		rateLimiter := deps.BuildDatapackRateLimiter
		if rateLimiter == nil {
			return handleExecutionError(span, logEntry, "build datapack rate limiter not initialized", fmt.Errorf("build datapack rate limiter not initialized"))
		}
		acquired, err := acquireBuildDatapackToken(childCtx, rateLimiter, task, logEntry)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to acquire rate limit token", err)
		}
		if !acquired {
			if err := rescheduleBuildDatapackTask(childCtx, deps.DB, redisGateway, task, "failed to acquire build datapack token within timeout, retrying later"); err != nil {
				return err
			}
			return nil
		}

		// Job-creation success "transfers ownership" of the token to the
		// K8s Job; the job-callback path in k8s_handler.go releases it on
		// success or failure of the Job. Any error path BEFORE the Job is
		// successfully created must release the token here, otherwise the
		// bucket slowly leaks on malformed payloads or transient gateway
		// errors and eventually wedges every BuildDatapack task at "no
		// token available".
		jobLaunched := false
		defer func() {
			if jobLaunched {
				return
			}
			if releaseErr := rateLimiter.ReleaseToken(childCtx, task.TaskID, task.TraceID); releaseErr != nil {
				logEntry.WithError(releaseErr).Warn("failed to release build datapack token after pre-launch failure")
			}
		}()

		payload, err := parseDatapackPayload(task.Payload)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to parse datapack payload", err)
		}

		// Pre-flight: block job submission until ClickHouse has spans
		// freshly ingested for the abnormal time window. Closes the race
		// in issue #210: prepare_inputs.py used to query CH for spans in
		// [abnormal_start, abnormal_end] and, under cluster load with the
		// OTel exporter retry queue lagging, get back zero rows -> empty
		// abnormal_traces.parquet -> ValueError -> datapack.build.failed.
		// On bounded-wait timeout we reschedule (retryable) instead of
		// hard-failing; persistent CH errors propagate as task errors.
		watermark, maxWait := freshnessParamsFromConfig()
		nsForFreshness := extractNamespaceFromBenchmarkEnv(payload.benchmark.EnvVars)
		if err := waitForCHFreshness(childCtx, deps.FreshnessProbe, nsForFreshness, payload.datapack.EndTime, watermark, maxWait, logEntry); err != nil {
			if errorsIsFreshnessTimeout(err) {
				if rerr := rescheduleBuildDatapackTask(childCtx, deps.DB, redisGateway, task, "datapack-build deferred: ClickHouse not fresh enough yet (issue #210)"); rerr != nil {
					return rerr
				}
				return nil
			}
			return handleExecutionError(span, logEntry, "datapack-build aborted: CH freshness probe failed", err)
		}

		annotations, err := task.GetAnnotations(childCtx)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to get annotations", err)
		}

		itemJson, err := json.Marshal(payload.datapack)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to marshal datapack item", err)
		}
		annotations[consts.JobAnnotationDatapack] = string(itemJson)

		jobLabels := utils.MergeSimpleMaps(
			task.GetLabels(),
			map[string]string{
				consts.K8sLabelAppID:     consts.AppID,
				consts.JobLabelDatapack:  payload.datapack.Name,
				consts.JobLabelDatasetID: strconv.Itoa(utils.GetIntValue(payload.datasetVersionID, 0)),
			},
		)

		params := &datapackJobCreationParams{
			jobName:     task.TaskID,
			image:       payload.benchmark.ImageRef,
			annotations: annotations,
			labels:      jobLabels,
			payload:     payload,
			dbConfig:    db.NewDatabaseConfig("clickhouse"),
		}
		if err := createDatapackJob(childCtx, k8sGateway, params); err != nil {
			return err
		}
		jobLaunched = true
		return nil
	})
}

// parseDatapackPayload extracts and validates the datapack payload from the task
func parseDatapackPayload(payload map[string]any) (*datapackPayload, error) {
	benchmark, err := utils.ConvertToType[dto.ContainerVersionItem](payload[consts.BuildBenchmark])
	if err != nil {
		return nil, fmt.Errorf("failed to convert '%s' to ContainerVersion: %w", consts.BuildBenchmark, err)
	}

	datapack, err := utils.ConvertToType[dto.InjectionItem](payload[consts.BuildDatapack])
	if err != nil {
		return nil, fmt.Errorf("failed to convert '%s' to InjectionItem: %w", consts.BuildDatapack, err)
	}

	datasetID, err := utils.GetPointerIntFromMap(payload, consts.BuildDatasetVersionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get '%s' from payload: %w", consts.BuildDatasetVersionID, err)
	}

	labels, err := utils.ConvertToType[[]dto.LabelItem](payload[consts.BuildLabels])
	if err != nil {
		return nil, fmt.Errorf("failed to convert '%s' to []LabelItem: %w", consts.BuildLabels, err)
	}

	return &datapackPayload{
		benchmark:        benchmark,
		datapack:         datapack,
		datasetVersionID: datasetID,
		labels:           labels,
	}, nil
}

func createDatapackJob(ctx context.Context, gateway *k8s.Gateway, params *datapackJobCreationParams) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(childCtx)
		logEntry := logrus.WithFields(logrus.Fields{
			"job_name":    params.jobName,
			"datapack_id": params.payload.datapack.ID,
		})

		// jfs.backend=s3 routes the job's OUTPUT_PATH at an s3:// URL
		// (handled by rcabench-platform's serde + copy_files) and skips
		// the dataset PVC mount entirely — the bucket *is* the dataset
		// store on the read side (S3DatapackStore), so leaving the NAS
		// mount would just write parquet files that the API never reads.
		// AWS_* env vars give rcabench-platform's fsspec/s3fs the same
		// endpoint/credentials the API-side blob.Client already uses.
		var (
			volumeMountConfigs []k8s.VolumeMountConfig
			datapackPathPrefix string
			extraEnvVars       []corev1.EnvVar
		)
		if config.GetString("jfs.backend") == "s3" {
			bucket := config.GetString("jfs.s3.datapack_bucket")
			if bucket == "" {
				return handleExecutionError(span, logEntry, "jfs.s3.datapack_bucket not configured", fmt.Errorf("missing jfs.s3.datapack_bucket"))
			}
			datapackPathPrefix = "s3://" + bucket
			extraEnvVars = s3DatapackEnvVars()
		} else {
			var err error
			volumeMountConfigs, err = getRequiredVolumeMountConfigs(gateway, []consts.VolumeMountName{
				consts.VolumeMountDataset,
			})
			if err != nil {
				return handleExecutionError(span, logEntry, "failed to get volume mount configurations", err)
			}
			datapackPathPrefix = volumeMountConfigs[0].MountPath
		}

		jobEnvVars, err := getDatapackJobEnvVars(params.jobName, datapackPathPrefix, params.payload, params.dbConfig)
		if err != nil {
			return handleExecutionError(span, logEntry, "failed to get job environment variables", err)
		}
		jobEnvVars = append(jobEnvVars, extraEnvVars...)

		return gateway.CreateJob(childCtx, params.toK8sJobConfig(jobEnvVars, volumeMountConfigs))
	})
}

// s3DatapackEnvVars constructs the AWS-SDK-flavoured env consumed by
// rcabench-platform's serde / copy_files when OUTPUT_PATH is an s3:// URL.
// Every k8s-side handle is config-driven so this works against any
// S3-compatible backend (rustfs, MinIO, real AWS, Volcengine TOS, …) by
// reconfiguring `blob.buckets.datapack` — no Go change required.
//
//	blob.buckets.datapack.endpoint            → AWS_ENDPOINT_URL_S3
//	blob.buckets.datapack.region              → AWS_REGION / AWS_DEFAULT_REGION
//	blob.buckets.datapack.access_key_env      → name of Secret key holding AK
//	blob.buckets.datapack.secret_key_env      → name of Secret key holding SK
//	blob.buckets.datapack.creds_secret_name   → k8s Secret in the job namespace
//	                                            that holds those two keys
func s3DatapackEnvVars() []corev1.EnvVar {
	endpoint := config.GetString("blob.buckets.datapack.endpoint")
	region := config.GetString("blob.buckets.datapack.region")
	if region == "" {
		region = "us-east-1"
	}
	envAK := config.GetString("blob.buckets.datapack.access_key_env")
	envSK := config.GetString("blob.buckets.datapack.secret_key_env")
	credsSecret := config.GetString("blob.buckets.datapack.creds_secret_name")

	envs := []corev1.EnvVar{
		{Name: "AWS_ENDPOINT_URL_S3", Value: endpoint},
		{Name: "AWS_REGION", Value: region},
		{Name: "AWS_DEFAULT_REGION", Value: region},
	}
	if credsSecret != "" && envAK != "" {
		envs = append(envs, corev1.EnvVar{
			Name: "AWS_ACCESS_KEY_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: credsSecret},
					Key:                  envAK,
					Optional:             ptrBool(true),
				},
			},
		})
	}
	if credsSecret != "" && envSK != "" {
		envs = append(envs, corev1.EnvVar{
			Name: "AWS_SECRET_ACCESS_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: credsSecret},
					Key:                  envSK,
					Optional:             ptrBool(true),
				},
			},
		})
	}
	return envs
}

func ptrBool(b bool) *bool { return &b }

func getDatapackJobEnvVars(taskID string, datapackPathPrefix string, payload *datapackPayload, dbConfig *db.DatabaseConfig) ([]corev1.EnvVar, error) {
	tz := config.GetString("system.timezone")
	if tz == "" {
		tz = time.Local.String()
	}

	now := time.Now()
	timestamp := now.Format(customTimeFormat)

	serviceToken, err := issueServiceToken(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate service token: %w", err)
	}

	jobEnvVars := []corev1.EnvVar{
		{Name: "TIMEZONE", Value: tz},
		{Name: "TIMESTAMP", Value: timestamp},
		{Name: "INJECTION_ID", Value: strconv.Itoa(payload.datapack.ID)},
		{Name: "NORMAL_START", Value: strconv.FormatInt(payload.datapack.StartTime.Add(-time.Duration(payload.datapack.PreDuration)*time.Minute).Unix(), 10)},
		{Name: "NORMAL_END", Value: strconv.FormatInt(payload.datapack.StartTime.Unix(), 10)},
		{Name: "ABNORMAL_START", Value: strconv.FormatInt(payload.datapack.StartTime.Unix(), 10)},
		{Name: "ABNORMAL_END", Value: strconv.FormatInt(payload.datapack.EndTime.Unix(), 10)},
		{Name: "WORKSPACE", Value: "/app"},
		{Name: "INPUT_PATH", Value: filepath.Join(datapackPathPrefix, payload.datapack.Name)},
		{Name: "OUTPUT_PATH", Value: filepath.Join(datapackPathPrefix, payload.datapack.Name)},
		{Name: "RCABENCH_BASE_URL", Value: config.GetString("k8s.service.internal_url")},
		{Name: "RCABENCH_TOKEN", Value: serviceToken},
		{Name: "RCABENCH_SKIP_STABILITY_VALIDATION", Value: "1"},
		{Name: "DB_HOST", Value: dbConfig.Host},
		{Name: "DB_PORT", Value: fmt.Sprint(dbConfig.Port)},
		{Name: "DB_USER", Value: dbConfig.User},
		{Name: "DB_PASSWORD", Value: dbConfig.Password},
		{Name: "DB_DATABASE", Value: dbConfig.Database},
		{Name: "DB_TIMEZONE", Value: dbConfig.Timezone},
	}

	envNameIndexMap := make(map[string]int, len(jobEnvVars))
	for index, jobEnvVar := range jobEnvVars {
		envNameIndexMap[jobEnvVar.Name] = index
	}

	for _, envVar := range payload.benchmark.EnvVars {
		if _, exists := envNameIndexMap[envVar.Key]; !exists {
			if envVar.TemplateString != "" {
				continue
			}

			if envVar.TemplateString != "" {
				logrus.Warnf("Skipping templated env var %s in benchmark version %d", envVar.Key, payload.benchmark.ID)
				continue
			}

			valueStr, ok := envVar.Value.(string)
			if !ok {
				logrus.Warnf("Skipping non-string env var %s", envVar.Key)
				continue
			}

			jobEnvVars = append(jobEnvVars, corev1.EnvVar{Name: envVar.Key, Value: valueStr})
		}
	}

	return jobEnvVars, nil
}

// acquireBuildDatapackToken runs the standard "acquire-or-wait" two-stage
// token grab: try AcquireToken first; if the bucket is full, fall through
// to WaitForToken; if that times out (returns false, nil) the caller is
// expected to reschedule the task. Pulled out of executeBuildDatapackWithDeps
// so the surge-cap behavior can be unit tested with a fakeIssuer instead of
// requiring a live Redis Gateway.
func acquireBuildDatapackToken(ctx context.Context, issuer tokenIssuer, task *dto.UnifiedTask, logEntry *logrus.Entry) (bool, error) {
	span := trace.SpanFromContext(ctx)
	acquired, err := issuer.AcquireToken(ctx, task.TaskID, task.TraceID)
	if err != nil {
		return false, err
	}
	if acquired {
		return true, nil
	}
	span.AddEvent("no token available, waiting")
	logEntry.Info("No build datapack token available, waiting...")
	acquired, err = issuer.WaitForToken(ctx, task.TaskID, task.TraceID)
	if err != nil {
		return false, err
	}
	return acquired, nil
}

// rescheduleBuildDatapackTask reschedules a BuildDatapack task with a random
// delay between 1 and 5 minutes when the rate-limit token wait timed out.
// Mirrors rescheduleContainerBuildingTask / rescheduleAlgoExecutionTask so
// the three task types behave consistently under sustained load.
func rescheduleBuildDatapackTask(ctx context.Context, dbConn *gorm.DB, redisGateway *redis.Gateway, task *dto.UnifiedTask, reason string) error {
	return tracing.WithSpan(ctx, func(childCtx context.Context) error {
		span := trace.SpanFromContext(childCtx)

		randomDelayMinutes := minDelayMinutes + rand.Intn(maxDelayMinutes-minDelayMinutes+1)
		executeTime := time.Now().Add(time.Duration(randomDelayMinutes) * time.Minute)

		span.AddEvent(fmt.Sprintf("rescheduling build datapack task: %s", reason))
		logrus.WithFields(logrus.Fields{
			"task_id":     task.TaskID,
			"trace_id":    task.TraceID,
			"delay_mins":  randomDelayMinutes,
			"retry_count": task.ReStartNum + 1,
		}).Warnf("%s: scheduled for %s", reason, executeTime.Format(time.DateTime))

		tracing.SetSpanAttribute(childCtx, consts.TaskStateKey, consts.GetTaskStateName(consts.TaskRescheduled))

		updateTaskState(childCtx,
			newTaskStateUpdate(
				task.TraceID,
				task.TaskID,
				consts.TaskTypeBuildDatapack,
				consts.TaskRescheduled,
				reason,
			).withEvent(consts.EventNoTokenAvailable, executeTime.String()).withDB(dbConn).withRedis(redisGateway),
		)

		task.Reschedule(executeTime)
		if err := common.SubmitTaskWithDB(childCtx, dbConn, redisGateway, task); err != nil {
			span.RecordError(err)
			span.AddEvent("failed to submit rescheduled task")
			return fmt.Errorf("failed to submit rescheduled build datapack task: %w", err)
		}

		return nil
	})
}
