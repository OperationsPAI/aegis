package system

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	buildkit "aegis/platform/buildkit"
	"aegis/clients/sso"
	"aegis/platform/config"
	etcd "aegis/platform/etcd"
	k8s "aegis/platform/k8s"
	redis "aegis/platform/redis"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
)

type Service struct {
	repo         *Repository
	buildkit     *buildkit.Gateway
	etcd         *etcd.Gateway
	k8s          *k8s.Gateway
	redis        *redis.Gateway
	runtimeQuery runtimeQuerySource
	sso          *ssoclient.Client
}

type serviceParams struct {
	fx.In

	Repo         *Repository
	Buildkit     *buildkit.Gateway
	Etcd         *etcd.Gateway
	K8s          *k8s.Gateway
	Redis        *redis.Gateway
	RuntimeQuery runtimeQuerySource
	SSO          *ssoclient.Client
}

func NewService(params serviceParams) *Service {
	return &Service{
		repo:         params.Repo,
		buildkit:     params.Buildkit,
		etcd:         params.Etcd,
		k8s:          params.K8s,
		redis:        params.Redis,
		runtimeQuery: params.RuntimeQuery,
		sso:          params.SSO,
	}
}

func (s *Service) GetHealth(ctx context.Context) (*HealthCheckResp, error) {
	start := time.Now()
	services := make(map[string]ServiceInfo)
	overallStatus := "healthy"

	buildkitInfo := s.checkBuildKitHealth(ctx)
	services["buildkit"] = buildkitInfo
	if buildkitInfo.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	dbInfo := s.checkDatabaseHealth(ctx)
	services["database"] = dbInfo
	if dbInfo.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	tracingInfo := s.checkTracingHealth(ctx)
	services["tracing"] = tracingInfo
	if tracingInfo.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	k8sInfo := s.checkKubernetesHealth(ctx)
	services["kubernetes"] = k8sInfo
	if k8sInfo.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	redisInfo := s.checkRedisHealth(ctx)
	services["redis"] = redisInfo
	if redisInfo.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	return &HealthCheckResp{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Version:   config.GetString("version"),
		Uptime:    time.Since(start).String(),
		Services:  services,
	}, nil
}

func (s *Service) GetMetrics(_ context.Context) (*MonitoringMetricsResp, error) {
	return &MonitoringMetricsResp{
		Timestamp: time.Now(),
		Metrics: map[string]MetricValue{
			"cpu_usage":          {Value: 25.5, Timestamp: time.Now(), Unit: "percent"},
			"memory_usage":       {Value: 60.2, Timestamp: time.Now(), Unit: "percent"},
			"disk_usage":         {Value: 45.8, Timestamp: time.Now(), Unit: "percent"},
			"active_connections": {Value: 142, Timestamp: time.Now(), Unit: "count"},
		},
		Labels: map[string]string{
			"instance": "rcabench-01",
			"version":  config.GetString("version"),
		},
	}, nil
}

func (s *Service) GetSystemInfo(_ context.Context) (*SystemInfo, error) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	return &SystemInfo{
		CPUUsage:    25.5,
		MemoryUsage: float64(memStats.Alloc) / float64(memStats.Sys) * 100,
		DiskUsage:   45.8,
		LoadAverage: "1.2, 1.5, 1.8",
	}, nil
}

func (s *Service) ListNamespaceLocks(ctx context.Context) (*ListNamespaceLockResp, error) {
	return s.runtimeQuery.ListNamespaceLocks(ctx)
}

func (s *Service) ListQueuedTasks(ctx context.Context) (*QueuedTasksResp, error) {
	return s.runtimeQuery.ListQueuedTasks(ctx)
}

func (s *Service) checkBuildKitHealth(parent context.Context) ServiceInfo {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	err := s.buildkit.CheckHealth(ctx, 5*time.Second)
	responseTime := time.Since(start)
	if err != nil {
		return ServiceInfo{
			Status:       "unhealthy",
			LastChecked:  time.Now(),
			ResponseTime: responseTime.String(),
			Error:        "BuildKit daemon unreachable",
			Details:      err.Error(),
		}
	}
	return ServiceInfo{Status: "healthy", LastChecked: time.Now(), ResponseTime: responseTime.String()}
}

func (s *Service) checkDatabaseHealth(parent context.Context) ServiceInfo {
	start := time.Now()
	db := s.repo.db
	if db == nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: "N/A", Error: "Database connection not available"}
	}

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var result int
	err := db.WithContext(ctx).Raw("SELECT 1").Scan(&result).Error
	responseTime := time.Since(start)
	if err != nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: responseTime.String(), Error: "Database query failed", Details: err.Error()}
	}
	return ServiceInfo{Status: "healthy", LastChecked: time.Now(), ResponseTime: responseTime.String()}
}

func (s *Service) checkTracingHealth(parent context.Context) ServiceInfo {
	start := time.Now()
	otlpURL := fmt.Sprintf("http://%s/v1/traces", config.GetString("tracing.endpoint"))
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, otlpURL, nil)
	if err != nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: time.Since(start).String(), Error: "Failed to create OTLP request", Details: err.Error()}
	}

	httpClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	resp, err := httpClient.Do(req)
	responseTime := time.Since(start)
	if err != nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: responseTime.String(), Error: "Tracing OTLP endpoint unreachable", Details: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusOK {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: responseTime.String(), Error: fmt.Sprintf("Tracing OTLP returned unexpected status %d", resp.StatusCode)}
	}
	return ServiceInfo{Status: "healthy", LastChecked: time.Now(), ResponseTime: responseTime.String(), Details: "Tracing OTLP endpoint responding"}
}

func (s *Service) checkKubernetesHealth(parent context.Context) ServiceInfo {
	start := time.Now()
	if s.k8s == nil {
		return ServiceInfo{Status: "unavailable", LastChecked: time.Now(), ResponseTime: time.Since(start).String(), Error: "Kubernetes gateway not configured"}
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := s.k8s.CheckHealth(ctx); err != nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: time.Since(start).String(), Error: "Kubernetes health check failed", Details: err.Error()}
	}
	return ServiceInfo{Status: "healthy", LastChecked: time.Now(), ResponseTime: time.Since(start).String()}
}

func (s *Service) checkRedisHealth(parent context.Context) ServiceInfo {
	start := time.Now()
	if s.redis == nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: "N/A", Error: "Redis connection not available"}
	}

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	err := s.redis.Ping(ctx)
	responseTime := time.Since(start)
	if err != nil {
		return ServiceInfo{Status: "unhealthy", LastChecked: time.Now(), ResponseTime: responseTime.String(), Error: "Redis ping failed", Details: err.Error()}
	}
	return ServiceInfo{Status: "healthy", LastChecked: time.Now(), ResponseTime: responseTime.String()}
}
