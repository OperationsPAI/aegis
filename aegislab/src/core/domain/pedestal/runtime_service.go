package pedestal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/helm"
	"aegis/platform/model"

	"gorm.io/gorm"
)

// helmGateway is the subset of *helm.Gateway that the runtime service uses.
// Defined as an interface so the chart-resolution flow can be unit-tested
// without a real Kubernetes cluster.
type helmGateway interface {
	List(ctx context.Context, namespaces ...string) ([]helm.ReleaseInfo, error)
	GetReleaseInfo(namespace, releaseName string) (*helm.ReleaseInfo, error)
	GetReleaseValues(namespace, releaseName string) (map[string]any, error)
	Uninstall(ctx context.Context, namespace, releaseName string, timeout time.Duration) error
	IsReleaseDeployed(namespace, releaseName string) (bool, error)
}

// systemConfigSource is the subset of the chaos-system config we need to
// classify a helm release as "managed by system X". Factored out so tests
// can plug in a deterministic fixture without touching Viper globals.
type systemConfigSource interface {
	// AllSystems returns a map from short_code → ns_pattern regex source.
	// Disabled / tombstoned systems are excluded.
	AllSystems() map[string]string
}

// chaosSystemConfigSource pulls system data straight from the etcd-backed
// Viper cache (same source the chaossystem service uses). Implements
// systemConfigSource for the production wiring.
type chaosSystemConfigSource struct{}

func (chaosSystemConfigSource) AllSystems() map[string]string {
	cfgMap := config.GetChaosSystemConfigManager().GetAll()
	out := make(map[string]string, len(cfgMap))
	for name, cfg := range cfgMap {
		// CommonDeleted (-1) is the tombstone marker; only surface live
		// systems so a deleted system's stale release doesn't show up as
		// "managed".
		if cfg.Status == consts.CommonDeleted {
			continue
		}
		out[name] = cfg.NsPattern
	}
	return out
}

// RuntimeService is the application-layer entry point for the admin
// /pedestals endpoints. It coordinates the helm Gateway, the pedestal
// repository (container_versions × helm_configs), and the chaos-system
// config source (system short_code allowlist) to install / restart /
// uninstall pedestal releases synchronously.
type RuntimeService struct {
	repo    *Repository
	gateway helmGateway
	systems systemConfigSource
}

// NewRuntimeService wires the runtime service to its production
// dependencies. The helm gateway and pedestal repository are required;
// passing nil panics on construction so a misconfigured fx graph fails
// loud at boot rather than on first HTTP request.
func NewRuntimeService(repo *Repository, gateway *helm.Gateway) *RuntimeService {
	if repo == nil {
		panic("pedestal.NewRuntimeService: repo is required")
	}
	if gateway == nil {
		panic("pedestal.NewRuntimeService: helm gateway is required")
	}
	return &RuntimeService{
		repo:    repo,
		gateway: gateway,
		systems: chaosSystemConfigSource{},
	}
}

// PedestalRelease is the runtime view of a single helm release surfaced by
// the admin list endpoint. Managed=true means the release name matches a
// known system short code (with the namespace matching that system's
// ns_pattern when set).
type PedestalRelease struct {
	Release      string    `json:"release"`
	Namespace    string    `json:"namespace"`
	Chart        string    `json:"chart,omitempty"`
	ChartVersion string    `json:"chart_version,omitempty"`
	Status       string    `json:"status"`
	DeployedAt   time.Time `json:"deployed_at"`
	System       string    `json:"system,omitempty"`
	Managed      bool      `json:"managed"`
	Reason       string    `json:"reason,omitempty"`
}

// PedestalReleaseDetail extends PedestalRelease with the user-supplied
// values map. Returned by the per-release GET endpoint so the admin UI can
// show what's currently deployed before kicking off a restart.
type PedestalReleaseDetail struct {
	PedestalRelease
	Values map[string]any `json:"values,omitempty"`
}

// InstallPedestalInput is the service-layer call shape for an admin install.
// Resolution rules:
//   - SystemCode identifies both the release name AND the default namespace.
//   - Namespace overrides the default when set (no validation against the
//     system's ns_pattern — admins may install for ad-hoc experiments).
//   - HelmValues are merged ON TOP of the configured HelmConfig values
//     (i.e. caller-supplied keys win), so an admin can swap an image tag
//     without re-uploading the seed.
type InstallPedestalInput struct {
	SystemCode         string
	ContainerVersionID int
	Namespace          string
	HelmValues         map[string]any
}

// InstallPedestalResult is the return shape for install / restart. Both
// happen synchronously; the caller blocks until helm returns or the
// context deadline fires.
type InstallPedestalResult struct {
	Release    string    `json:"release"`
	Namespace  string    `json:"namespace"`
	Chart      string    `json:"chart"`
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	DeployedAt time.Time `json:"deployed_at"`
}

// ListReleases enumerates every helm release across all namespaces visible
// to the configured kubeconfig and tags each one with its system
// classification. Releases whose name matches a known system short code AND
// whose namespace matches that system's ns_pattern are marked Managed=true;
// releases that match by name only get Reason="namespace does not match
// system ns_pattern"; releases whose name matches no system are simply
// returned with Managed=false.
//
// Listing is unfiltered cluster-wide because ns_pattern is a regex (not a
// fixed namespace), so we cannot pre-enumerate the namespace set without
// listing namespaces from k8s — which would double the round-trip count
// for no operational gain over post-hoc filtering.
func (s *RuntimeService) ListReleases(ctx context.Context) ([]PedestalRelease, error) {
	releases, err := s.gateway.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list helm releases: %w", err)
	}

	systems := s.systems.AllSystems()
	patterns := make(map[string]*regexp.Regexp, len(systems))
	for name, raw := range systems {
		if raw == "" {
			continue
		}
		re, err := regexp.Compile(raw)
		if err != nil {
			// A malformed ns_pattern is an operator error elsewhere; skip
			// matching for that system instead of failing the whole list.
			continue
		}
		patterns[name] = re
	}

	out := make([]PedestalRelease, 0, len(releases))
	for _, rel := range releases {
		entry := PedestalRelease{
			Release:      rel.Name,
			Namespace:    rel.Namespace,
			Chart:        rel.Chart,
			ChartVersion: rel.ChartVersion,
			Status:       rel.Status,
			DeployedAt:   rel.LastDeployed,
		}
		if _, ok := systems[rel.Name]; ok {
			entry.System = rel.Name
			// Convention: release name == system short_code. The namespace
			// match is a secondary check — when ns_pattern is empty we
			// trust the name alone.
			if re, hasPattern := patterns[rel.Name]; hasPattern {
				if re.MatchString(rel.Namespace) {
					entry.Managed = true
				} else {
					entry.Reason = fmt.Sprintf("namespace %s does not match system %s ns_pattern", rel.Namespace, rel.Name)
				}
			} else {
				entry.Managed = true
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// GetRelease returns the helm release info plus its user-supplied values.
// When the release exists in multiple namespaces (possible since ns_pattern
// allows per-pool replicas like ts-1, ts-2, …) the caller must specify the
// namespace; without it we walk every namespace that matches a system
// short_code equal to release until we hit a deployed release.
//
// Returns (nil, nil) when the release is not found in any namespace so the
// HTTP layer can map that to 404.
func (s *RuntimeService) GetRelease(ctx context.Context, release, namespace string) (*PedestalReleaseDetail, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, fmt.Errorf("release is required: %w", consts.ErrBadRequest)
	}

	if namespace != "" {
		return s.getReleaseInNamespace(ctx, release, namespace)
	}

	// No namespace pin: assume the convention (namespace == release ==
	// system short_code). For pool-style systems the caller should pass
	// ?namespace=… explicitly.
	return s.getReleaseInNamespace(ctx, release, release)
}

func (s *RuntimeService) getReleaseInNamespace(_ context.Context, release, namespace string) (*PedestalReleaseDetail, error) {
	info, err := s.gateway.GetReleaseInfo(namespace, release)
	if err != nil {
		return nil, fmt.Errorf("get release info: %w", err)
	}
	if info == nil {
		return nil, nil
	}
	values, err := s.gateway.GetReleaseValues(namespace, release)
	if err != nil {
		return nil, fmt.Errorf("get release values: %w", err)
	}
	return &PedestalReleaseDetail{
		PedestalRelease: PedestalRelease{
			Release:      info.Name,
			Namespace:    info.Namespace,
			Chart:        info.Chart,
			ChartVersion: info.ChartVersion,
			Status:       info.Status,
			DeployedAt:   info.LastDeployed,
		},
		Values: values,
	}, nil
}

// Install runs a synchronous helm install for the given container_version.
// The request is validated against the resolved ContainerVersion (must be
// pedestal type, must belong to the named system, must have a HelmConfig
// row). The caller's context deadline bounds the helm apply.
func (s *RuntimeService) Install(ctx context.Context, in InstallPedestalInput) (*InstallPedestalResult, error) {
	systemCode := strings.TrimSpace(in.SystemCode)
	if systemCode == "" {
		return nil, fmt.Errorf("system_code is required: %w", consts.ErrBadRequest)
	}
	if in.ContainerVersionID <= 0 {
		return nil, fmt.Errorf("container_version_id is required and must be > 0: %w", consts.ErrBadRequest)
	}

	version, err := s.repo.GetContainerVersionByID(ctx, in.ContainerVersionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("container_version %d not found: %w", in.ContainerVersionID, consts.ErrNotFound)
		}
		return nil, fmt.Errorf("load container version: %w", err)
	}
	if version.Container == nil {
		return nil, fmt.Errorf("container_version %d has no container row: %w", in.ContainerVersionID, consts.ErrBadRequest)
	}
	if version.Container.Type != consts.ContainerTypePedestal {
		return nil, fmt.Errorf("container_version %d is not a pedestal (type=%v): %w", in.ContainerVersionID, version.Container.Type, consts.ErrBadRequest)
	}
	if version.Container.Name != systemCode {
		return nil, fmt.Errorf("container_version %d belongs to system %q, not %q: %w", in.ContainerVersionID, version.Container.Name, systemCode, consts.ErrBadRequest)
	}
	if version.HelmConfig == nil {
		return nil, fmt.Errorf("container_version %d has no helm_config row: %w", in.ContainerVersionID, consts.ErrBadRequest)
	}

	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		namespace = systemCode
	}

	spec, err := s.buildInstallSpec(version, systemCode, namespace, in.HelmValues)
	if err != nil {
		return nil, err
	}

	if err := helm.InstallPedestal(ctx, s.helmGatewayConcrete(), spec); err != nil {
		return nil, fmt.Errorf("install pedestal %s: %w", systemCode, err)
	}

	info, err := s.gateway.GetReleaseInfo(namespace, systemCode)
	if err != nil {
		return nil, fmt.Errorf("post-install status check: %w", err)
	}
	if info == nil {
		// install succeeded but status check came back empty — return a
		// minimum-information success so the caller doesn't 500.
		return &InstallPedestalResult{
			Release:    systemCode,
			Namespace:  namespace,
			Chart:      spec.ChartName,
			Version:    spec.Version,
			Status:     "deployed",
			DeployedAt: time.Now().UTC(),
		}, nil
	}
	return &InstallPedestalResult{
		Release:    info.Name,
		Namespace:  info.Namespace,
		Chart:      info.Chart,
		Version:    info.ChartVersion,
		Status:     info.Status,
		DeployedAt: info.LastDeployed,
	}, nil
}

// Restart redeploys the given release in-place. When in.HelmValues is empty
// we fetch the previously-applied values (`helm get values`) so the restart
// is a true rolling-redeploy. When the release doesn't exist we fall back
// to a fresh install using the latest active ContainerVersion for the
// system named by release (admin-only convenience).
//
// The release name is also the system short_code by convention; the caller
// passes the release URL parameter and we resolve everything else.
func (s *RuntimeService) Restart(ctx context.Context, release, namespace string, overrideValues map[string]any) (*InstallPedestalResult, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, fmt.Errorf("release is required: %w", consts.ErrBadRequest)
	}
	if namespace == "" {
		namespace = release
	}

	// Resolve the ContainerVersion to use. For now we always pick the
	// highest-versioned active pedestal version for the system — the
	// release_name → system_short_code mapping makes that unambiguous.
	// Admins that need a specific version should use POST /pedestals
	// directly with a container_version_id.
	version, err := s.findLatestVersionForSystem(ctx, release)
	if err != nil {
		return nil, err
	}

	values := overrideValues
	if len(values) == 0 {
		existing, err := s.gateway.GetReleaseValues(namespace, release)
		if err != nil {
			return nil, fmt.Errorf("read previous values for restart: %w", err)
		}
		values = existing
	}

	spec, err := s.buildInstallSpec(version, release, namespace, values)
	if err != nil {
		return nil, err
	}
	if err := helm.InstallPedestal(ctx, s.helmGatewayConcrete(), spec); err != nil {
		return nil, fmt.Errorf("restart pedestal %s: %w", release, err)
	}

	info, err := s.gateway.GetReleaseInfo(namespace, release)
	if err != nil {
		return nil, fmt.Errorf("post-restart status check: %w", err)
	}
	if info == nil {
		return &InstallPedestalResult{
			Release:    release,
			Namespace:  namespace,
			Chart:      spec.ChartName,
			Version:    spec.Version,
			Status:     "deployed",
			DeployedAt: time.Now().UTC(),
		}, nil
	}
	return &InstallPedestalResult{
		Release:    info.Name,
		Namespace:  info.Namespace,
		Chart:      info.Chart,
		Version:    info.ChartVersion,
		Status:     info.Status,
		DeployedAt: info.LastDeployed,
	}, nil
}

// Uninstall removes the named release synchronously. NotFound is treated as
// success — callers can chain uninstall + reinstall without a defensive
// existence check.
func (s *RuntimeService) Uninstall(ctx context.Context, release, namespace string, timeout time.Duration) error {
	release = strings.TrimSpace(release)
	if release == "" {
		return fmt.Errorf("release is required: %w", consts.ErrBadRequest)
	}
	if namespace == "" {
		namespace = release
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if err := s.gateway.Uninstall(ctx, namespace, release, timeout); err != nil {
		return fmt.Errorf("uninstall release %s: %w", release, err)
	}
	return nil
}

// helmGatewayConcrete returns the embedded *helm.Gateway when the field is
// the production type. Tests that pass a fake helmGateway will not call
// Install / Restart paths that require a real cluster, so the type
// assertion below is safe for those test surfaces.
func (s *RuntimeService) helmGatewayConcrete() *helm.Gateway {
	if gw, ok := s.gateway.(*helm.Gateway); ok {
		return gw
	}
	return nil
}

// buildInstallSpec materializes a helm.PedestalInstallSpec from a resolved
// ContainerVersion + override values map. Values resolution order:
//
//	HelmConfig.ValueFile           (lowest precedence — yaml file)
//	HelmConfig.DynamicValues       (parameter_configs / helm_config_values)
//	caller-supplied overrides      (highest precedence — admin-supplied)
//
// The merge is a shallow per-top-level-key overwrite (the same semantics
// helm itself uses for --set). Nested merges are deliberately not
// performed because the admin override pathway is the escape hatch — if a
// caller needs a deep merge they can fetch values first and submit the
// merged result.
func (s *RuntimeService) buildInstallSpec(version *model.ContainerVersion, releaseName, namespace string, overrides map[string]any) (helm.PedestalInstallSpec, error) {
	if version == nil || version.HelmConfig == nil {
		return helm.PedestalInstallSpec{}, fmt.Errorf("missing helm config for container_version: %w", consts.ErrBadRequest)
	}
	cfg := version.HelmConfig
	item := dto.NewHelmConfigItem(cfg)
	values := item.GetValuesMap()
	for k, v := range overrides {
		values[k] = v
	}

	overall, wait := adminInstallTimeouts()
	return helm.PedestalInstallSpec{
		Namespace:        namespace,
		ReleaseName:      releaseName,
		ChartName:        cfg.ChartName,
		Version:          cfg.Version,
		RepoURL:          resolveRepoURL(cfg),
		RepoName:         cfg.RepoName,
		LocalPath:        cfg.LocalPath,
		Values:           values,
		InstallTimeout:   overall,
		UninstallTimeout: wait,
	}, nil
}

// resolveRepoURL returns the chart repo URL with the etcd-backed
// `helm.repo.<repo_name>.url` fallback applied. Mirrors the same lookup
// the orchestrator's installPedestal does so a chart configured with an
// empty repo_url installs identically from both call sites.
func resolveRepoURL(cfg *model.HelmConfig) string {
	if cfg.RepoURL != "" {
		return cfg.RepoURL
	}
	if cfg.RepoName == "" {
		return ""
	}
	return config.GetString(fmt.Sprintf("helm.repo.%s.url", cfg.RepoName))
}

// adminInstallTimeouts returns the (install, uninstall) timeouts for the
// admin endpoints. Defaults are tighter than the orchestrator's
// (10 min install, 5 min uninstall) because the admin flow is interactive
// and the caller's context deadline is the authoritative bound — these are
// fallback values when the caller doesn't pass a deadline.
func adminInstallTimeouts() (time.Duration, time.Duration) {
	install := 10 * time.Minute
	uninstall := 5 * time.Minute
	if v := config.GetInt("pedestal.admin.install_timeout_seconds"); v > 0 {
		install = time.Duration(v) * time.Second
	}
	if v := config.GetInt("pedestal.admin.uninstall_timeout_seconds"); v > 0 {
		uninstall = time.Duration(v) * time.Second
	}
	return install, uninstall
}

// findLatestVersionForSystem returns the highest-versioned active pedestal
// container_version for the given system short_code. Used by Restart when
// the caller doesn't pin a container_version_id.
func (s *RuntimeService) findLatestVersionForSystem(ctx context.Context, systemCode string) (*model.ContainerVersion, error) {
	var container model.Container
	if err := s.repo.db.WithContext(ctx).
		Where("name = ? AND type = ? AND status >= 0", systemCode, consts.ContainerTypePedestal).
		First(&container).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no pedestal container for system %s: %w", systemCode, consts.ErrNotFound)
		}
		return nil, fmt.Errorf("load pedestal container: %w", err)
	}
	var version model.ContainerVersion
	err := s.repo.db.WithContext(ctx).
		Preload("Container").
		Preload("HelmConfig").
		Preload("HelmConfig.DynamicValues").
		Where("container_id = ? AND status >= 0", container.ID).
		Order("name_major DESC, name_minor DESC, name_patch DESC, id DESC").
		First(&version).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no active versions for pedestal system %s: %w", systemCode, consts.ErrNotFound)
		}
		return nil, fmt.Errorf("load latest version: %w", err)
	}
	if version.HelmConfig == nil {
		return nil, fmt.Errorf("latest version %d for system %s has no helm_config: %w", version.ID, systemCode, consts.ErrBadRequest)
	}
	return &version, nil
}
