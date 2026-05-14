package blob

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// BucketConfig is the per-bucket policy + driver pointer parsed from
// `[blob.buckets.<name>]` in config.
type BucketConfig struct {
	Name string

	Driver string // "localfs" | "s3"

	// localfs
	Root string

	// s3 family — endpoint, region, credentials env-var names
	Endpoint     string
	Region       string
	AccessKeyEnv string
	SecretKeyEnv string
	// AccessKey / SecretKey allow embedding credentials directly in the
	// TOML (dev only — prefer *Env in shared deploys).
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	PathStyle bool

	// policy
	MaxObjectBytes      int64
	AllowedContentTypes []string
	PublicRead          bool
	RetentionDays       int
	InlineMaxBytes      int64

	// ACL — simple role lists; service tokens get the "service" role.
	WriteRoles []string
	ReadRoles  []string
}

// AllowsContentType returns true if the bucket has no allowlist or the
// given content type is on it.
func (b *BucketConfig) AllowsContentType(ct string) bool {
	if len(b.AllowedContentTypes) == 0 {
		return true
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	for _, allowed := range b.AllowedContentTypes {
		if strings.EqualFold(allowed, ct) {
			return true
		}
	}
	return false
}

// Bucket bundles a parsed config with the live Driver instance the
// handler routes through.
type Bucket struct {
	Config BucketConfig
	Driver Driver
}

// Registry maps stable bucket name → Driver. Producers only reference
// bucket names; the registry is the routing role.
type Registry struct {
	buckets map[string]*Bucket
}

func (r *Registry) Lookup(name string) (*Bucket, error) {
	b, ok := r.buckets[name]
	if !ok {
		return nil, ErrBucketNotFound
	}
	return b, nil
}

// Names returns all configured bucket names — used by health checks
// and the lifecycle worker.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.buckets))
	for n := range r.buckets {
		out = append(out, n)
	}
	return out
}

// NewTestRegistry builds a Registry from a pre-assembled bucket map.
// Intended for tests that wire drivers by hand and want to bypass the
// viper-config path used in production.
func NewTestRegistry(buckets map[string]*Bucket) *Registry {
	cp := make(map[string]*Bucket, len(buckets))
	for k, v := range buckets {
		cp[k] = v
	}
	return &Registry{buckets: cp}
}

// RegistryDeps lets the fx wiring inject the signing key (used by
// localfs presign).
type RegistryDeps struct {
	SigningKey []byte
}

// NewRegistryFromConfig assembles the registry from
// `[blob.buckets.*]`. Unknown drivers fail boot loudly — a typo in a
// bucket's driver name should not silently fall back to localfs.
func NewRegistryFromConfig(deps RegistryDeps) (*Registry, error) {
	raw := viper.GetStringMap("blob.buckets")
	buckets := make(map[string]*Bucket, len(raw))

	for name := range raw {
		cfg, err := parseBucketConfig(name)
		if err != nil {
			return nil, fmt.Errorf("blob: bucket %q: %w", name, err)
		}
		drv, err := buildDriver(cfg, deps)
		if err != nil {
			return nil, fmt.Errorf("blob: bucket %q driver: %w", name, err)
		}
		buckets[name] = &Bucket{Config: cfg, Driver: drv}
	}
	return &Registry{buckets: buckets}, nil
}

func parseBucketConfig(name string) (BucketConfig, error) {
	prefix := "blob.buckets." + name
	cfg := BucketConfig{
		Name:                name,
		Driver:              viper.GetString(prefix + ".driver"),
		Root:                viper.GetString(prefix + ".root"),
		Endpoint:            viper.GetString(prefix + ".endpoint"),
		Region:              viper.GetString(prefix + ".region"),
		AccessKeyEnv:        viper.GetString(prefix + ".access_key_env"),
		SecretKeyEnv:        viper.GetString(prefix + ".secret_key_env"),
		AccessKey:           viper.GetString(prefix + ".access_key"),
		SecretKey:           viper.GetString(prefix + ".secret_key"),
		Bucket:              viper.GetString(prefix + ".bucket"),
		UseSSL:              viper.GetBool(prefix + ".use_ssl"),
		PathStyle:           viper.GetBool(prefix + ".path_style"),
		MaxObjectBytes:      viper.GetInt64(prefix + ".max_object_bytes"),
		AllowedContentTypes: viper.GetStringSlice(prefix + ".allowed_content_types"),
		PublicRead:          viper.GetBool(prefix + ".public_read"),
		RetentionDays:       viper.GetInt(prefix + ".retention_days"),
		InlineMaxBytes:      viper.GetInt64(prefix + ".inline_max_bytes"),
		WriteRoles:          viper.GetStringSlice(prefix + ".write_roles"),
		ReadRoles:           viper.GetStringSlice(prefix + ".read_roles"),
	}
	if cfg.Driver == "" {
		return cfg, fmt.Errorf("driver is required")
	}
	if cfg.InlineMaxBytes == 0 {
		cfg.InlineMaxBytes = 64 * 1024
	}
	return cfg, nil
}

func buildDriver(cfg BucketConfig, deps RegistryDeps) (Driver, error) {
	switch cfg.Driver {
	case "localfs":
		return NewLocalFSDriver(cfg, deps.SigningKey)
	case "s3":
		return NewS3Driver(cfg)
	default:
		return nil, fmt.Errorf("unknown driver %q", cfg.Driver)
	}
}
