package helm

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegis/config"
	"aegis/infra/tracing"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/repo"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/yaml"
)

type Gateway struct{}

func NewGateway() *Gateway {
	return &Gateway{}
}

func (g *Gateway) AddRepo(namespace, name, url string) error {
	settings, _, err := newRuntime(namespace)
	if err != nil {
		return err
	}

	repoFile := settings.RepositoryConfig
	if err := os.MkdirAll(settings.RepositoryCache, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("could not create repository cache directory: %w", err)
	}

	data, err := os.ReadFile(repoFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not read repository file: %w", err)
	}

	var repoFileModel repo.File
	if err == nil {
		if err := yaml.Unmarshal(data, &repoFileModel); err != nil {
			return fmt.Errorf("cannot unmarshal repository file: %w", err)
		}
	}

	if repoFileModel.Has(name) {
		if repoFileModel.Get(name).URL != url {
			repoFileModel.Get(name).URL = url
		}
		if err := repoFileModel.WriteFile(repoFile, 0644); err != nil {
			return fmt.Errorf("failed to write repository file: %w", err)
		}
		logrus.Infof("Updated repository %s URL to %s", name, url)
		return nil
	}

	entry := &repo.Entry{Name: name, URL: url}
	repository, err := repo.NewChartRepository(entry, getter.All(settings))
	if err != nil {
		return fmt.Errorf("failed to create chart repository: %w", err)
	}
	if _, err := repository.DownloadIndexFile(); err != nil {
		return fmt.Errorf("looks like %q is not a valid chart repository or cannot be reached: %w", url, err)
	}

	repoFileModel.Update(entry)
	if err := repoFileModel.WriteFile(repoFile, 0644); err != nil {
		return fmt.Errorf("failed to write repository file: %w", err)
	}

	return nil
}

func (g *Gateway) Install(ctx context.Context, namespace, releaseName, chartName, version string, values map[string]any, installTimeout, uninstallTimeout time.Duration) error {
	settings, actionConfig, err := newRuntime(namespace)
	if err != nil {
		return err
	}

	installed, err := g.isReleaseInstalled(actionConfig, releaseName)
	if err != nil {
		return err
	}
	if installed {
		logrus.Infof("Uninstalling existing %s release", releaseName)
		if err := g.uninstallRelease(actionConfig, releaseName, uninstallTimeout); err != nil {
			return err
		}
	} else {
		logrus.Infof("No existing %s release found", releaseName)
	}

	return g.installRelease(ctx, settings, actionConfig, namespace, releaseName, chartName, version, values, installTimeout)
}

func (g *Gateway) UpdateRepo(namespace, name string) error {
	settings, _, err := newRuntime(namespace)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(settings.RepositoryConfig)
	if err != nil {
		return fmt.Errorf("could not read repository file: %w", err)
	}

	var repoFileModel repo.File
	if err := yaml.Unmarshal(data, &repoFileModel); err != nil {
		return fmt.Errorf("cannot unmarshal repository file: %w", err)
	}

	for _, entry := range repoFileModel.Repositories {
		if name != "" && name != entry.Name {
			continue
		}
		logrus.Infof("Updating repository %s", entry.Name)
		repository, err := repo.NewChartRepository(entry, getter.All(settings))
		if err != nil {
			return fmt.Errorf("failed to create chart repository for %s: %w", entry.Name, err)
		}
		if _, err := repository.DownloadIndexFile(); err != nil {
			return fmt.Errorf("failed to update repository %s: %w", entry.Name, err)
		}
	}

	return nil
}

func (g *Gateway) installRelease(ctx context.Context, settings *cli.EnvSettings, actionConfig *action.Configuration, namespace, releaseName, chartName, version string, vals map[string]any, timeout time.Duration) error {
	return tracing.WithSpan(ctx, func(ctx context.Context) error {
		now := time.Now()
		defer func() {
			log.Printf("InstallRelease took %s", time.Since(now))
		}()

		installAction := action.NewInstall(actionConfig)
		installAction.ReleaseName = releaseName
		installAction.Namespace = namespace
		// Wait=false: do not block on cluster readiness. The manifest-apply
		// upper bound stays at `timeout` (5–10 min is fine for the
		// API-server-side apply itself), but DSB-class charts (TT, hs, sn,
		// mm — 20–41 services with chained init containers) routinely take
		// 15–25 min to become Ready cluster-side, which is far longer than
		// any sane install timeout. Callers that need cluster-readiness
		// must opt into the workload-level probe via
		// k8s.Gateway.WaitNamespaceReady after this returns.
		installAction.Wait = false
		installAction.Timeout = timeout
		installAction.CreateNamespace = true
		installAction.Version = version

		chartPath, err := findCachedChart(settings, chartName, version)
		if err != nil {
			return err
		}
		if chartPath == "" {
			// Either no cached tgz exists, or the cached tgz is for a
			// different version than requested. In both cases we let
			// LocateChart re-resolve from the repo index so installAction.Version
			// is honored — otherwise a stale `<chart>-<oldver>.tgz` shadows
			// every subsequent version bump (issue #374).
			logrus.Infof("Chart %s version %q not found in cache, downloading...", chartName, version)
			chartPath, err = installAction.LocateChart(chartName, settings)
			if err != nil {
				return fmt.Errorf("failed to locate chart %s: %w", chartName, err)
			}
		} else {
			logrus.Infof("Using cached chart for %s version %q at %s", chartName, version, chartPath)
		}

		chart, err := loader.Load(chartPath)
		if err != nil {
			return fmt.Errorf("failed to load chart %s: %w", chartName, err)
		}
		if _, err := installAction.Run(chart, vals); err != nil {
			return fmt.Errorf("failed to install release %s: %v", releaseName, err)
		}
		return nil
	})
}

func (g *Gateway) isReleaseInstalled(actionConfig *action.Configuration, releaseName string) (bool, error) {
	statusAction := action.NewStatus(actionConfig)
	_, err := statusAction.Run(releaseName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, fmt.Errorf("failed to get release status: %w", err)
	}
	return true, nil
}

// IsReleaseDeployed returns true when <releaseName> exists in <namespace> and
// is in the `deployed` status. Used by callers (e.g. RestartPedestal with
// SkipRestartPedestal=true) to short-circuit a reinstall when the chart is
// already in place. Unreachable/failed releases return false so the caller
// falls through to a real install.
func (g *Gateway) IsReleaseDeployed(namespace, releaseName string) (bool, error) {
	_, actionConfig, err := newRuntime(namespace)
	if err != nil {
		return false, err
	}
	statusAction := action.NewStatus(actionConfig)
	rel, err := statusAction.Run(releaseName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, fmt.Errorf("failed to get release status: %w", err)
	}
	if rel == nil || rel.Info == nil {
		return false, nil
	}
	return rel.Info.Status.String() == "deployed", nil
}

func (g *Gateway) uninstallRelease(actionConfig *action.Configuration, releaseName string, timeout time.Duration) error {
	uninstallAction := action.NewUninstall(actionConfig)
	uninstallAction.Wait = true
	uninstallAction.Timeout = timeout

	_, err := uninstallAction.Run(releaseName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "release: not found") {
			logrus.Infof("Release %s is not installed, nothing to uninstall", releaseName)
			return nil
		}
		return fmt.Errorf("failed to uninstall release %s: %w", releaseName, err)
	}
	return nil
}

func newRuntime(namespace string) (*cli.EnvSettings, *action.Configuration, error) {
	settings := cli.New()
	settings.SetNamespace(namespace)
	settings.Debug = config.GetBool("helm.debug")

	actionConfig := new(action.Configuration)
	configFlags := genericclioptions.NewConfigFlags(true)
	configFlags.Namespace = &namespace
	if err := actionConfig.Init(configFlags, namespace, os.Getenv("HELM_DRIVER"), log.Printf); err != nil {
		return nil, nil, fmt.Errorf("failed to initialize Helm action configuration: %w", err)
	}
	// Required for installing charts from OCI references (oci://...).
	registryClient, err := registry.NewClient(
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize Helm registry client: %w", err)
	}
	actionConfig.RegistryClient = registryClient

	return settings, actionConfig, nil
}

// findCachedChart returns the path of a cached chart tgz (or local dir) that
// matches both <chartName> and <version>. When <version> is non-empty, only an
// exact <chartBaseName>-<version>.tgz match counts as a hit; an unversioned
// glob would otherwise return e.g. trainticket-0.3.0.tgz when the caller asked
// for 0.3.1, silently installing the stale chart (issue #374). When <version>
// is empty (caller didn't pin a version), fall back to the legacy any-version
// glob so installAction.LocateChart can resolve the latest from the repo.
func findCachedChart(settings *cli.EnvSettings, chartName, version string) (string, error) {
	if _, err := os.Stat(chartName); err == nil {
		abs, err := filepath.Abs(chartName)
		if err == nil {
			logrus.Infof("Found local chart at: %s", abs)
			return abs, nil
		}
	}

	cacheDir := settings.RepositoryCache
	chartBaseName := chartName
	if strings.Contains(chartName, "/") {
		parts := strings.Split(chartName, "/")
		if len(parts) == 2 {
			chartBaseName = parts[1]
		}
	}

	versionGlob := "*"
	if version != "" {
		versionGlob = version
	}
	searchPatterns := []string{
		fmt.Sprintf("%s/*/%s-%s.tgz", cacheDir, chartBaseName, versionGlob),
		fmt.Sprintf("%s/%s-%s.tgz", cacheDir, chartBaseName, versionGlob),
	}

	for _, pattern := range searchPatterns {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			logrus.Infof("Found cached chart at: %s", matches[0])
			return matches[0], nil
		}
	}

	localChartDir := filepath.Join(cacheDir, chartName)
	if stat, err := os.Stat(localChartDir); err == nil && stat.IsDir() {
		logrus.Infof("Found cached chart directory at: %s", localChartDir)
		return localChartDir, nil
	}

	return "", nil
}
