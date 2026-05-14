package consumer

import (
	"fmt"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"aegis/platform/config"
	"aegis/platform/consts"
	k8s "aegis/platform/k8s"
)

// DatapackOutputBackend abstracts where a BuildDatapack Job writes its
// parquet output. Two implementations exist today: an S3-compatible bucket
// (selected when jfs.backend=s3) and a PVC mount (default). The interface
// only covers what the orchestrator actually needs — env vars to inject
// into the Job, k8s VolumeMountConfigs to attach, a path prefix to hand
// to the Job via INPUT_PATH/OUTPUT_PATH, and a join function that knows
// whether the prefix is a URL or a filesystem path.
type DatapackOutputBackend interface {
	PathPrefix() string
	EnvVars() []corev1.EnvVar
	VolumeMountConfigs(gateway *k8s.Gateway) ([]k8s.VolumeMountConfig, error)
	JoinPath(prefix, name string) string
}

// selectDatapackBackend picks the backend based on the jfs.backend config
// key. Any value other than "s3" falls back to the PVC backend so the
// historical "filesystem" / "" / unset cases all keep working.
func selectDatapackBackend() (DatapackOutputBackend, error) {
	if config.GetString("jfs.backend") == "s3" {
		bucket := config.GetString("jfs.s3.datapack_bucket")
		if bucket == "" {
			return nil, fmt.Errorf("jfs.s3.datapack_bucket not configured")
		}
		return &s3DatapackBackend{bucket: bucket}, nil
	}
	return &pvcDatapackBackend{}, nil
}

// joinDatapackPath joins a datapack name onto a path prefix. If the prefix
// looks like a URL (any "scheme://" form — s3://, gs://, oss://, http(s)://)
// we splice with "/" so the double-slash after the scheme survives. Anything
// else goes through filepath.Join. filepath.Join("s3://b", "x") collapses
// to "s3:/b/x" which then fails fsspec/s3fs URL parsing.
func joinDatapackPath(prefix, name string) string {
	if strings.Contains(prefix, "://") {
		return strings.TrimRight(prefix, "/") + "/" + name
	}
	return filepath.Join(prefix, name)
}

type s3DatapackBackend struct {
	bucket string
}

func (b *s3DatapackBackend) PathPrefix() string { return "s3://" + b.bucket }

func (b *s3DatapackBackend) EnvVars() []corev1.EnvVar { return s3DatapackEnvVars() }

func (b *s3DatapackBackend) VolumeMountConfigs(_ *k8s.Gateway) ([]k8s.VolumeMountConfig, error) {
	return nil, nil
}

func (b *s3DatapackBackend) JoinPath(prefix, name string) string {
	return joinDatapackPath(prefix, name)
}

type pvcDatapackBackend struct {
	resolved []k8s.VolumeMountConfig
}

func (b *pvcDatapackBackend) PathPrefix() string {
	if len(b.resolved) == 0 {
		return ""
	}
	return b.resolved[0].MountPath
}

func (b *pvcDatapackBackend) EnvVars() []corev1.EnvVar { return nil }

func (b *pvcDatapackBackend) VolumeMountConfigs(gateway *k8s.Gateway) ([]k8s.VolumeMountConfig, error) {
	cfgs, err := getRequiredVolumeMountConfigs(gateway, []consts.VolumeMountName{
		consts.VolumeMountDataset,
	})
	if err != nil {
		return nil, err
	}
	b.resolved = cfgs
	return cfgs, nil
}

func (b *pvcDatapackBackend) JoinPath(prefix, name string) string {
	return joinDatapackPath(prefix, name)
}
