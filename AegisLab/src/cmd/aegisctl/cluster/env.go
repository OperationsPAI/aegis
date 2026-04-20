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
}

type RedisProbe interface {
	SMembers(ctx context.Context, key string) ([]string, error)
	SRem(ctx context.Context, key string, members ...string) (int64, error)
}

type EtcdProbe interface {
	Get(ctx context.Context, key string) (value string, exists bool, err error)
	Put(ctx context.Context, key, value string) error
}
