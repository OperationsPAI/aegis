package cluster

import "context"

// CheckEnv is the abstraction every CheckFunc receives.
type CheckEnv interface {
	Config() Config
	K8s() K8sProbe
	Net() NetProbe
	ClickHouse() ClickHouseProbe
	MySQL() MySQLProbe
	Redis() RedisProbe
	Etcd() EtcdProbe
	Helm() HelmProbe
}

// Config is the subset of aegisctl config we need.
type Config struct {
	K8sNamespace   string
	MySQLHost      string
	MySQLPort      string
	ClickHouseHost string
	ClickHousePort string
	ClickHouseDB   string
	ClickHouseUser string
	ClickHousePass string
	RedisAddr      string
	EtcdEndpoints  []string
	EtcdUsername   string
	EtcdPassword   string
	ServiceAccount string
	DatasetPVC     string
	ExperimentPVC  string
}

type K8sProbe interface {
	NamespaceExists(ctx context.Context, name string) (bool, error)
	CreateNamespace(ctx context.Context, name string) error
	ServiceAccountExists(ctx context.Context, namespace, name string) (bool, error)
	PVCBound(ctx context.Context, namespace, name string) (exists bool, bound bool, err error)
	HasCRDGroup(ctx context.Context, group string) (bool, error)
	CreateServiceAccount(ctx context.Context, namespace, name string) error
	CreatePVC(ctx context.Context, namespace, name string, spec PVCSpec) error
	// ConfigMapData returns the data payload of a ConfigMap, or (nil, false)
	// when the ConfigMap is missing. Used by otel pipeline checks.
	ConfigMapData(ctx context.Context, namespace, name string) (map[string]string, bool, error)
	// ClusterRoleAllowsPodNamespaceRead reports whether any ClusterRole in the
	// cluster grants get/list/watch on pods + namespaces (the minimum RBAC the
	// otel k8sattributes processor needs).
	ClusterRoleAllowsPodNamespaceRead(ctx context.Context) (bool, error)
}

type PVCSpec struct {
	StorageClassName string
	Size             string
}

type NetProbe interface {
	DialTimeout(ctx context.Context, address string) error
}

type ClickHouseProbe interface {
	TablesIn(ctx context.Context, database string) ([]string, error)
}

type MySQLProbe interface {
	TaskState(ctx context.Context, taskID string) (state int, exists bool, err error)
	// DynamicConfigsByPrefix returns every (key, default_value) row whose
	// config_key begins with the given prefix. Used by the 5-layer onboarding
	// preflight to diff DB vs etcd.
	DynamicConfigsByPrefix(ctx context.Context, prefix string) (map[string]string, error)
	// SystemFixtures returns the database fixture surface for one system by
	// name: pedestal/benchmark containers plus their versions / helm configs.
	SystemFixtures(ctx context.Context, systemName string) (SystemFixtureSummary, error)
}

// SystemFixtureSummary describes the DB fixtures that should exist for one
// enabled system.
type SystemFixtureSummary struct {
	PedestalCount              int
	BenchmarkCount             int
	PedestalVersionCount       int
	BenchmarkVersionCount      int
	PedestalVersionsMissingHelm int
	BenchmarkVersionsEmptyCmd   int
}

type RedisProbe interface {
	SMembers(ctx context.Context, key string) ([]string, error)
	SRem(ctx context.Context, key string, members ...string) (int64, error)
}

type EtcdProbe interface {
	Get(ctx context.Context, key string) (value string, exists bool, err error)
	Put(ctx context.Context, key, value string) error
	Close() error
	// ListPrefix returns every (key, value) pair under the given prefix.
	ListPrefix(ctx context.Context, prefix string) (map[string]string, error)
}

// HelmProbe resolves helm chart locations to verify the source is actually
// reachable. Implementations may shell out to the `helm` binary, HTTP-GET an
// index.yaml, or os.Stat a local path.
type HelmProbe interface {
	ResolveChart(ctx context.Context, src HelmChartSource) error
}

// HelmChartSource is the minimal set of HelmConfig fields the preflight
// needs to decide which resolution strategy to apply.
type HelmChartSource struct {
	ChartName string
	Version   string
	RepoURL   string
	RepoName  string
	LocalPath string
}
