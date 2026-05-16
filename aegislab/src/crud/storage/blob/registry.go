package blob

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// ErrBucketAlreadyExists is returned by Registry.Create when a bucket
// with that name already exists (from config or DB).
var ErrBucketAlreadyExists = errors.New("bucket already exists")

// bucketNameRE enforces typical S3-compatible bucket naming.
var bucketNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}$`)

// BucketConfigRecord persists runtime-created buckets in the database.
// Buckets declared in TOML config are NOT stored here — the DB row is
// only written for buckets created via POST /buckets at runtime.
type BucketConfigRecord struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	Name           string `gorm:"uniqueIndex;not null;size:64"`
	Driver         string `gorm:"not null;size:32"`
	Root           string `gorm:"size:512"`
	Endpoint       string `gorm:"size:512"`
	PublicEndpoint string `gorm:"size:512"`
	Region         string `gorm:"size:64"`
	AccessKeyEnv   string `gorm:"size:128"`
	SecretKeyEnv   string `gorm:"size:128"`
	Bucket         string `gorm:"size:128"`
	UseSSL         bool
	PathStyle      bool
	MaxObjectBytes int64
	RetentionDays  int
	PublicRead     bool
	ContentTypes   string `gorm:"type:text"` // comma-separated allow-list
	WriteRoles     string `gorm:"type:text"` // comma-separated
	ReadRoles      string `gorm:"type:text"` // comma-separated
	ProxyUploads   bool
	// LifecycleConfig is the JSON-encoded BucketLifecycle policy.
	// Persistence-only in v1; the DeletionWorker does not consume it yet.
	LifecycleConfig string    `gorm:"column:lifecycle_config;type:text;size:4096"`
	CreatedAt       time.Time `gorm:"autoCreateTime"`
}

func (BucketConfigRecord) TableName() string { return "blob_bucket_configs" }

// BucketConfig is the per-bucket policy + driver pointer parsed from
// `[blob.buckets.<name>]` in config.
type BucketConfig struct {
	Name string

	Driver string // "localfs" | "s3"

	// localfs
	Root string

	// s3 family — endpoint, region, credentials env-var names
	Endpoint string
	// PublicEndpoint, when set, is used to sign presigned URLs handed
	// out to browsers. The driver still talks to `Endpoint` for its own
	// S3 calls. Leave empty if the regular `Endpoint` is reachable from
	// browsers (e.g., the same LB that the SPA loads from).
	PublicEndpoint string
	Region         string
	AccessKeyEnv   string
	SecretKeyEnv   string
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

	// ProxyUploads forces presign-put / presign-get to mint same-origin
	// `/api/v2/blob/raw/<token>` URLs instead of returning a direct
	// driver URL. The browser then PUTs/GETs bytes through aegis-blob,
	// which streams them to the underlying driver server-side. Used when
	// the driver's presigned URLs are unreachable from the browser
	// (e.g. SigV4 break across edge proxies / LBs).
	ProxyUploads bool

	// Lifecycle is the runtime-set retention policy persisted alongside
	// the bucket config. Validated on write; execution is deferred — see
	// lifecycleExecutionDeferred in bucket_lifecycle.go.
	Lifecycle *BucketLifecycle
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
//
// The mu guards buckets for concurrent hot-add via Create. Config-loaded
// buckets are written once at boot and never mutated after that, so
// read-only paths that predate Create are safe without locking; after
// Create was called callers must hold at least a read lock. In practice
// Create is rare enough that a simple Mutex (not RWMutex) is fine.
type Registry struct {
	mu      sync.Mutex
	buckets map[string]*Bucket
	db      *gorm.DB // nil when DB-backed creation is not wired (test-only)
	deps    RegistryDeps
}

func (r *Registry) Lookup(name string) (*Bucket, error) {
	r.mu.Lock()
	b, ok := r.buckets[name]
	r.mu.Unlock()
	if !ok {
		return nil, ErrBucketNotFound
	}
	return b, nil
}

// Names returns all configured bucket names — used by health checks
// and the lifecycle worker.
func (r *Registry) Names() []string {
	r.mu.Lock()
	out := make([]string, 0, len(r.buckets))
	for n := range r.buckets {
		out = append(out, n)
	}
	r.mu.Unlock()
	return out
}

// Create validates, persists, and hot-adds a new bucket. Returns
// ErrBucketAlreadyExists if the name is already registered. The DB row
// is written first; if driver construction fails the row is removed so
// the registry stays consistent.
func (r *Registry) Create(ctx context.Context, cfg BucketConfig) (*Bucket, error) {
	if !bucketNameRE.MatchString(cfg.Name) {
		return nil, fmt.Errorf("invalid bucket name %q (must match [a-z0-9][a-z0-9-]{2,62})", cfg.Name)
	}
	if err := cfg.Lifecycle.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.buckets[cfg.Name]; exists {
		return nil, ErrBucketAlreadyExists
	}
	if r.db != nil {
		rec, err := bucketConfigToRecord(cfg)
		if err != nil {
			return nil, fmt.Errorf("encode lifecycle: %w", err)
		}
		if err := r.db.WithContext(ctx).Create(&rec).Error; err != nil {
			if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "Duplicate") {
				return nil, ErrBucketAlreadyExists
			}
			return nil, fmt.Errorf("persist bucket config: %w", err)
		}
	}
	drv, err := buildDriver(cfg, r.deps)
	if err != nil {
		if r.db != nil {
			_ = r.db.WithContext(ctx).Where("name = ?", cfg.Name).Delete(&BucketConfigRecord{}).Error
		}
		return nil, fmt.Errorf("build driver for %q: %w", cfg.Name, err)
	}
	b := &Bucket{Config: cfg, Driver: drv}
	r.buckets[cfg.Name] = b
	return b, nil
}

// SetLifecycle replaces a bucket's lifecycle policy, both in the
// in-memory Bucket.Config and (when DB-backed) in the persisted row.
// A nil policy clears the column. Static-config buckets without a DB
// row are refused — those policies belong in TOML.
//
// lifecycleExecutionDeferred: this updates the stored shape only; no
// sweep / GC consumes the rules yet.
func (r *Registry) SetLifecycle(ctx context.Context, name string, lc *BucketLifecycle) error {
	if err := lc.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, exists := r.buckets[name]
	if !exists {
		return ErrBucketNotFound
	}
	if r.db == nil {
		return fmt.Errorf("bucket %q is not DB-backed (declared in static config)", name)
	}
	enc, err := encodeBucketLifecycle(lc)
	if err != nil {
		return fmt.Errorf("encode lifecycle: %w", err)
	}
	res := r.db.WithContext(ctx).
		Model(&BucketConfigRecord{}).
		Where("name = ?", name).
		Update("lifecycle_config", enc)
	if res.Error != nil {
		return fmt.Errorf("persist lifecycle: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("bucket %q is not DB-backed (declared in static config)", name)
	}
	b.Config.Lifecycle = lc
	return nil
}

// HasDB reports whether the registry is backed by a database. Buckets
// declared in static TOML config have no DB row and cannot be dropped
// at runtime — Drop reports an error in that case.
func (r *Registry) HasDB() bool { return r.db != nil }

// Drop removes a runtime-created bucket from the registry and the DB.
// Buckets declared in static TOML config (no DB row) are refused with
// an error — operators must edit the config file and restart.
func (r *Registry) Drop(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.buckets[name]; !exists {
		return ErrBucketNotFound
	}
	if r.db == nil {
		return fmt.Errorf("bucket %q is not DB-backed (declared in static config)", name)
	}
	res := r.db.WithContext(ctx).Where("name = ?", name).Delete(&BucketConfigRecord{})
	if res.Error != nil {
		return fmt.Errorf("delete bucket config row: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// No DB row means this bucket came from static TOML even though
		// db is set — refuse the drop rather than leave the registry and
		// config out of sync.
		return fmt.Errorf("bucket %q is not DB-backed (declared in static config)", name)
	}
	delete(r.buckets, name)
	return nil
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

// NewTestRegistryWithDB is like NewTestRegistry but also wires a DB
// connection so Drop can persist its row deletes. Tests seed the
// blob_bucket_configs table separately.
func NewTestRegistryWithDB(buckets map[string]*Bucket, db *gorm.DB) *Registry {
	cp := make(map[string]*Bucket, len(buckets))
	for k, v := range buckets {
		cp[k] = v
	}
	return &Registry{buckets: cp, db: db}
}

// NewTestRegistryWithDeps is like NewTestRegistryWithDB but also wires
// RegistryDeps so tests that exercise Registry.Create with a localfs
// driver can supply a non-empty signing key.
func NewTestRegistryWithDeps(buckets map[string]*Bucket, db *gorm.DB, deps RegistryDeps) *Registry {
	cp := make(map[string]*Bucket, len(buckets))
	for k, v := range buckets {
		cp[k] = v
	}
	return &Registry{buckets: cp, db: db, deps: deps}
}

// RegistryDeps lets the fx wiring inject the signing key (used by
// localfs presign).
type RegistryDeps struct {
	SigningKey []byte
}

// NewRegistryFromConfig assembles the registry from
// `[blob.buckets.*]` (static TOML config) plus any rows in the
// blob_bucket_configs DB table (runtime-created buckets). Unknown
// drivers fail boot loudly — a typo in a bucket's driver name should
// not silently fall back to localfs.
func NewRegistryFromConfig(deps RegistryDeps) (*Registry, error) {
	return newRegistryFromConfigWithDB(deps, nil)
}

// NewRegistryFromConfigWithDB is the production wiring path that also
// loads DB-persisted runtime buckets.
func NewRegistryFromConfigWithDB(deps RegistryDeps, db *gorm.DB) (*Registry, error) {
	return newRegistryFromConfigWithDB(deps, db)
}

func newRegistryFromConfigWithDB(deps RegistryDeps, db *gorm.DB) (*Registry, error) {
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

	if db != nil {
		var rows []BucketConfigRecord
		if err := db.Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("blob: load db buckets: %w", err)
		}
		for _, row := range rows {
			if _, exists := buckets[row.Name]; exists {
				// static config wins over DB — skip
				continue
			}
			cfg := bucketConfigFromRecord(row)
			drv, err := buildDriver(cfg, deps)
			if err != nil {
				return nil, fmt.Errorf("blob: db bucket %q driver: %w", row.Name, err)
			}
			buckets[row.Name] = &Bucket{Config: cfg, Driver: drv}
		}
	}

	return &Registry{buckets: buckets, db: db, deps: deps}, nil
}

func parseBucketConfig(name string) (BucketConfig, error) {
	prefix := "blob.buckets." + name
	cfg := BucketConfig{
		Name:                name,
		Driver:              viper.GetString(prefix + ".driver"),
		Root:                viper.GetString(prefix + ".root"),
		Endpoint:            viper.GetString(prefix + ".endpoint"),
		PublicEndpoint:      viper.GetString(prefix + ".public_endpoint"),
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
		ProxyUploads:        viper.GetBool(prefix + ".proxy_uploads"),
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

func bucketConfigToRecord(cfg BucketConfig) (BucketConfigRecord, error) {
	lc, err := encodeBucketLifecycle(cfg.Lifecycle)
	if err != nil {
		return BucketConfigRecord{}, err
	}
	return BucketConfigRecord{
		Name:            cfg.Name,
		Driver:          cfg.Driver,
		Root:            cfg.Root,
		Endpoint:        cfg.Endpoint,
		PublicEndpoint:  cfg.PublicEndpoint,
		Region:          cfg.Region,
		AccessKeyEnv:    cfg.AccessKeyEnv,
		SecretKeyEnv:    cfg.SecretKeyEnv,
		Bucket:          cfg.Bucket,
		UseSSL:          cfg.UseSSL,
		PathStyle:       cfg.PathStyle,
		MaxObjectBytes:  cfg.MaxObjectBytes,
		RetentionDays:   cfg.RetentionDays,
		PublicRead:      cfg.PublicRead,
		ContentTypes:    strings.Join(cfg.AllowedContentTypes, ","),
		WriteRoles:      strings.Join(cfg.WriteRoles, ","),
		ReadRoles:       strings.Join(cfg.ReadRoles, ","),
		ProxyUploads:    cfg.ProxyUploads,
		LifecycleConfig: lc,
	}, nil
}

func bucketConfigFromRecord(r BucketConfigRecord) BucketConfig {
	splitNonEmpty := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, ",")
	}
	cfg := BucketConfig{
		Name:                r.Name,
		Driver:              r.Driver,
		Root:                r.Root,
		Endpoint:            r.Endpoint,
		PublicEndpoint:      r.PublicEndpoint,
		Region:              r.Region,
		AccessKeyEnv:        r.AccessKeyEnv,
		SecretKeyEnv:        r.SecretKeyEnv,
		Bucket:              r.Bucket,
		UseSSL:              r.UseSSL,
		PathStyle:           r.PathStyle,
		MaxObjectBytes:      r.MaxObjectBytes,
		RetentionDays:       r.RetentionDays,
		PublicRead:          r.PublicRead,
		AllowedContentTypes: splitNonEmpty(r.ContentTypes),
		WriteRoles:          splitNonEmpty(r.WriteRoles),
		ReadRoles:           splitNonEmpty(r.ReadRoles),
		ProxyUploads:        r.ProxyUploads,
	}
	if lc, err := decodeBucketLifecycle(r.LifecycleConfig); err == nil {
		cfg.Lifecycle = lc
	}
	if cfg.InlineMaxBytes == 0 {
		cfg.InlineMaxBytes = 64 * 1024
	}
	return cfg
}
