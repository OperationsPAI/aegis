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

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// helmGateway is the subset of *helm.Gateway that the runtime service's
// read paths use (list / status / values / uninstall). Defined as an
// interface so the chart-resolution flow can be unit-tested without a real
// Kubernetes cluster. The write paths (Install / Restart) need the concrete
// *helm.Gateway because they call into helm.InstallPedestal which takes the
// gateway directly — see RuntimeService.concrete.
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
	repo *Repository
	// gateway is the read-side view of the helm gateway. Tests inject a
	// fake here. In production this is the same value as concrete, wrapped
	// in the helmGateway interface so the read paths stay decoupled from
	// the concrete type.
	gateway helmGateway
	// concrete is the typed *helm.Gateway used by the write paths
	// (Install / Restart) that call helm.InstallPedestal directly. May be
	// nil in tests that only exercise read paths.
	concrete *helm.Gateway
	systems  systemConfigSource
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
		repo:     repo,
		gateway:  gateway,
		concrete: gateway,
		systems:  chaosSystemConfigSource{},
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
	// Namespaces is populated when the per-release GET found the release in
	// multiple namespaces (pool-style systems like ts-1, ts-2, …) — caller
	// should re-issue with an explicit ?namespace= to disambiguate. Only
	// set in that ambiguity case; ignored otherwise.
	Namespaces []string `json:"namespaces,omitempty"`
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
	// PreviousVersion and NewVersion are populated by Restart so any
	// version change is visible to operators. PreviousVersion is the chart
	// version that was deployed before the restart; NewVersion is the
	// version that was applied. When the caller does not pass
	// container_version_id they should be equal (in-place redeploy).
	PreviousVersion string `json:"previous_version,omitempty"`
	NewVersion      string `json:"new_version,omitempty"`
}

// defaultListLimit is the default cap on the number of releases returned by
// ListReleases when the caller doesn't pass ?limit. Picked to stay well
// inside the gin/JSON response envelope on real clusters (~50 releases
// today; cap headroom is ~4×).
const defaultListLimit = 200

// maxListLimit caps how high ?limit can go. Anything past this is rejected
// — large clusters can blow up the response payload otherwise.
const maxListLimit = 1000

// ListReleases enumerates every helm release across all namespaces visible
// to the configured kubeconfig and tags each one with its system
// classification. Releases whose name matches a known system short code AND
// whose namespace matches that system's ns_pattern are marked Managed=true;
// releases that match by name only get Reason="namespace does not match
// system ns_pattern"; releases whose name matches no system are simply
// returned with Managed=false.
//
// limit caps the number of releases returned; zero / negative means use
// the default (200). Values above maxListLimit are clamped.
//
// Listing is unfiltered cluster-wide because ns_pattern is a regex (not a
// fixed namespace), so we cannot pre-enumerate the namespace set without
// listing namespaces from k8s — which would double the round-trip count
// for no operational gain over post-hoc filtering.
func (s *RuntimeService) ListReleases(ctx context.Context, limit int) ([]PedestalRelease, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

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
		if len(out) >= limit {
			break
		}
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
// namespace; without it we first try the conventional namespace (==release)
// and, on miss, fall back to a cluster-wide List filtered by release name —
// returning the first deployed match. If multiple deployed matches exist,
// Namespaces is populated so the caller can re-issue with an explicit pin.
//
// Returns (nil, nil) when the release is not found anywhere so the HTTP
// layer can map that to 404.
func (s *RuntimeService) GetRelease(ctx context.Context, release, namespace string) (*PedestalReleaseDetail, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, fmt.Errorf("release is required: %w", consts.ErrBadRequest)
	}

	if namespace != "" {
		return s.getReleaseInNamespace(ctx, release, namespace)
	}

	// No namespace pin: try the conventional layout (namespace == release)
	// first. This is the common case and avoids a cluster-wide list.
	detail, err := s.getReleaseInNamespace(ctx, release, release)
	if err != nil {
		return nil, err
	}
	if detail != nil {
		return detail, nil
	}

	// Conventional lookup missed — fall back to a cluster-wide list and
	// match by release name. Lets pool-style releases (ts-1, ts-2, …)
	// resolve without the caller having to guess the namespace.
	all, err := s.gateway.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list helm releases for fallback: %w", err)
	}
	var matches []helm.ReleaseInfo
	for _, rel := range all {
		if rel.Name == release {
			matches = append(matches, rel)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	// Prefer a deployed release when multiple exist; ties broken by the
	// gateway's listing order (helm returns most-recent first for the
	// All=true list).
	pick := matches[0]
	for _, rel := range matches {
		if rel.Status == "deployed" {
			pick = rel
			break
		}
	}
	detail, err = s.getReleaseInNamespace(ctx, release, pick.Namespace)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, nil
	}
	if len(matches) > 1 {
		nss := make([]string, 0, len(matches))
		for _, rel := range matches {
			nss = append(nss, rel.Namespace)
		}
		detail.Namespaces = nss
	}
	return detail, nil
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
// row, parent Container must be live). The caller's context deadline bounds
// the helm apply; the handler wraps the gin context with an
// adminInstallTimeoutCeiling() before calling.
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
	// GetContainerVersionByID filters status>=0 on the version row only;
	// the parent Container preload is unfiltered, so a soft-deleted
	// Container would still get here. Reject it explicitly to keep Install
	// and findLatestVersionForSystem in lock-step on what counts as
	// installable.
	if version.Container.Status < 0 {
		return nil, fmt.Errorf("container_version %d belongs to a soft-deleted container: %w", in.ContainerVersionID, consts.ErrBadRequest)
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

	if s.concrete == nil {
		return nil, fmt.Errorf("helm gateway is not wired for install (test surface?): %w", consts.ErrBadRequest)
	}
	if err := helm.InstallPedestal(ctx, s.concrete, spec); err != nil {
		return nil, s.wrapInstallFailure(err, namespace, systemCode)
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

// wrapInstallFailure enriches a helm-install error with the current
// release status (when discoverable) so partial-install state — release
// stuck in `pending-install` / `failed` — is visible from the failure
// message rather than requiring an operator to manually run `helm status`.
// Also emits a Warn log with the same fields for ops greppability.
//
// Best-effort: any error inspecting the release is folded into the
// returned message but not propagated as a primary failure.
func (s *RuntimeService) wrapInstallFailure(origErr error, namespace, release string) error {
	info, statusErr := s.gateway.GetReleaseInfo(namespace, release)
	if statusErr != nil || info == nil {
		// Either inspection failed or there is no release row at all —
		// return the original error unchanged. Logging at info-level here
		// would be noisy on the cluster-not-reachable path.
		return fmt.Errorf("install pedestal %s: %w", release, origErr)
	}
	if info.Status != "" && info.Status != "deployed" {
		logrus.WithFields(logrus.Fields{
			"namespace": namespace,
			"release":   release,
			"status":    info.Status,
		}).Warn("pedestal install left release in non-deployed state")
		return fmt.Errorf("install pedestal %s failed (release left in status=%s): %w", release, info.Status, origErr)
	}
	return fmt.Errorf("install pedestal %s: %w", release, origErr)
}

// Restart redeploys the given release in-place. By default Restart pins to
// the chart version that's currently deployed (read from `helm get release`)
// — operators that just want "redeploy what's running" never accidentally
// upgrade. If containerVersionID is non-zero the caller is explicitly
// opting into an upgrade and the named version is used instead.
//
// When in.HelmValues is empty we fetch the previously-applied values
// (`helm get values`) so the restart is a true rolling-redeploy.
//
// If the release doesn't exist (or its chart version is empty in helm's
// view), we return 409 Conflict rather than silently falling back to "use
// the latest active version".
func (s *RuntimeService) Restart(ctx context.Context, release, namespace string, containerVersionID int, overrideValues map[string]any) (*InstallPedestalResult, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return nil, fmt.Errorf("release is required: %w", consts.ErrBadRequest)
	}
	if namespace == "" {
		namespace = release
	}

	current, err := s.gateway.GetReleaseInfo(namespace, release)
	if err != nil {
		return nil, fmt.Errorf("read current release for restart: %w", err)
	}
	if current == nil {
		return nil, fmt.Errorf("release %s/%s not found — restart requires an existing release: %w", namespace, release, consts.ErrConflict)
	}

	previousVersion := current.ChartVersion

	// Resolve the ContainerVersion to use. Default = the currently-
	// deployed chart version (in-place redeploy). Explicit override =
	// caller-supplied container_version_id (upgrade opt-in).
	var version *model.ContainerVersion
	if containerVersionID > 0 {
		version, err = s.repo.GetContainerVersionByID(ctx, containerVersionID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("container_version %d not found: %w", containerVersionID, consts.ErrNotFound)
			}
			return nil, fmt.Errorf("load container version: %w", err)
		}
		if version.Container == nil || version.Container.Status < 0 {
			return nil, fmt.Errorf("container_version %d belongs to a soft-deleted container: %w", containerVersionID, consts.ErrBadRequest)
		}
		if version.Container.Type != consts.ContainerTypePedestal {
			return nil, fmt.Errorf("container_version %d is not a pedestal (type=%v): %w", containerVersionID, version.Container.Type, consts.ErrBadRequest)
		}
		if version.Container.Name != release {
			return nil, fmt.Errorf("container_version %d belongs to system %q, not %q: %w", containerVersionID, version.Container.Name, release, consts.ErrBadRequest)
		}
		if version.HelmConfig == nil {
			return nil, fmt.Errorf("container_version %d has no helm_config row: %w", containerVersionID, consts.ErrBadRequest)
		}
	} else {
		if previousVersion == "" {
			return nil, fmt.Errorf("release %s/%s has no chart version recorded — cannot restart without an explicit container_version_id: %w", namespace, release, consts.ErrConflict)
		}
		version, err = s.findVersionForSystemAndChart(ctx, release, previousVersion)
		if err != nil {
			return nil, err
		}
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
	if s.concrete == nil {
		return nil, fmt.Errorf("helm gateway is not wired for restart (test surface?): %w", consts.ErrBadRequest)
	}
	if err := helm.InstallPedestal(ctx, s.concrete, spec); err != nil {
		return nil, s.wrapInstallFailure(fmt.Errorf("restart pedestal %s: %w", release, err), namespace, release)
	}

	newVersion := spec.Version
	if containerVersionID > 0 && newVersion != previousVersion {
		logrus.WithFields(logrus.Fields{
			"release":          release,
			"namespace":        namespace,
			"previous_version": previousVersion,
			"new_version":      newVersion,
		}).Info("pedestal upgrade via restart endpoint")
	}

	info, err := s.gateway.GetReleaseInfo(namespace, release)
	if err != nil {
		return nil, fmt.Errorf("post-restart status check: %w", err)
	}
	if info == nil {
		return &InstallPedestalResult{
			Release:         release,
			Namespace:       namespace,
			Chart:           spec.ChartName,
			Version:         spec.Version,
			Status:          "deployed",
			DeployedAt:      time.Now().UTC(),
			PreviousVersion: previousVersion,
			NewVersion:      newVersion,
		}, nil
	}
	return &InstallPedestalResult{
		Release:         info.Name,
		Namespace:       info.Namespace,
		Chart:           info.Chart,
		Version:         info.ChartVersion,
		Status:          info.Status,
		DeployedAt:      info.LastDeployed,
		PreviousVersion: previousVersion,
		NewVersion:      newVersion,
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
		Namespace:      namespace,
		ReleaseName:    releaseName,
		ChartName:      cfg.ChartName,
		Version:        cfg.Version,
		RepoURL:        resolveRepoURL(cfg),
		RepoName:       cfg.RepoName,
		LocalPath:      cfg.LocalPath,
		Values:         values,
		OverallTimeout: overall,
		WaitTimeout:    wait,
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
// the helm-SDK-internal timeouts (manifest apply / pre-install uninstall)
// passed through to PedestalInstallSpec.
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

// adminInstallTimeoutCeiling returns the request-envelope ceiling for
// install / restart handlers. Sized at "configured install timeout + 60s
// slack" so the helm-SDK-internal apply timeout fires first, leaving a
// margin for post-install status check + JSON serialization before the
// gin context is cancelled.
func adminInstallTimeoutCeiling() time.Duration {
	install, _ := adminInstallTimeouts()
	return install + 60*time.Second
}

// adminUninstallTimeoutCeiling is the request-envelope ceiling for the
// uninstall handler. Sized at "configured uninstall timeout + 60s slack".
func adminUninstallTimeoutCeiling() time.Duration {
	_, uninstall := adminInstallTimeouts()
	return uninstall + 60*time.Second
}

// findVersionForSystemAndChart resolves the ContainerVersion whose
// HelmConfig.Version matches the chart version currently deployed for the
// named system. Used by Restart to redeploy "what's running" without
// surprising operators with a silent upgrade.
//
// Returns ErrNotFound when no version row matches the chart version (likely
// means the deployed release was installed out-of-band; caller must pass
// an explicit container_version_id to restart it).
func (s *RuntimeService) findVersionForSystemAndChart(ctx context.Context, systemCode, chartVersion string) (*model.ContainerVersion, error) {
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
		Joins("JOIN helm_configs ON helm_configs.container_version_id = container_versions.id").
		Where("container_versions.container_id = ? AND container_versions.status >= 0 AND helm_configs.version = ?", container.ID, chartVersion).
		Order("container_versions.name_major DESC, container_versions.name_minor DESC, container_versions.name_patch DESC, container_versions.id DESC").
		First(&version).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no container_version for system %s at chart version %s — pass container_version_id explicitly to restart this release: %w", systemCode, chartVersion, consts.ErrNotFound)
		}
		return nil, fmt.Errorf("load version for chart %s: %w", chartVersion, err)
	}
	if version.HelmConfig == nil {
		return nil, fmt.Errorf("version %d for system %s has no helm_config: %w", version.ID, systemCode, consts.ErrBadRequest)
	}
	return &version, nil
}
