package helm

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// installGateway is the subset of *Gateway that InstallPedestal exercises.
// Defined here (rather than at the call site) so the helper itself can be
// unit-tested with a recording fake — the production *Gateway satisfies
// this interface implicitly.
type installGateway interface {
	AddRepo(namespace, name, url string) error
	UpdateRepo(namespace, name string) error
	Install(ctx context.Context, namespace, releaseName, chartName, version string, values map[string]any, installTimeout, uninstallTimeout time.Duration) error
}

// PedestalInstallSpec is the fully-resolved input shape for InstallPedestal.
// Both callers (orchestrator RestartPedestal and the admin /pedestals
// endpoint) construct this themselves so the shared helper stays free of
// orchestrator-only dependencies (etcd lookups, namespace-pool template
// substitution, dynamic-config timeout reads).
type PedestalInstallSpec struct {
	// Namespace is the k8s namespace to install into. For pedestal charts
	// this is conventionally also the release name (one chart per namespace,
	// named after the system short code).
	Namespace string
	// ReleaseName is the helm release name. Pass the system short code
	// directly; the orchestrator passes the per-pool namespace string which
	// is also the release name by convention.
	ReleaseName string

	// ChartName / Version / RepoURL / RepoName describe the remote chart
	// source. When both RepoURL and RepoName are empty (or remote install
	// fails), the helper falls back to LocalPath.
	ChartName string
	Version   string
	RepoURL   string
	RepoName  string

	// LocalPath is an absolute path to a pre-staged chart tgz on disk. Used
	// as the remote-install fallback. A non-existent path is treated as
	// "no local fallback" — the helper logs and tries remote-only.
	LocalPath string

	// Values is the merged helm values map (file + dynamic). The helper
	// passes it through unchanged to gateway.Install.
	Values map[string]any

	// InstallTimeout is the helm-side timeout for the manifest apply
	// (Wait=false; cluster readiness is not gated by this).
	InstallTimeout time.Duration
	// UninstallTimeout is the timeout for the pre-install uninstall pass
	// inside Install (used when the release already exists).
	UninstallTimeout time.Duration
}

// InstallPedestal performs a pedestal helm install with the
// "remote-with-local-fallback" pattern: try the configured remote
// repository first; if that fails (or no remote is configured) and a local
// tgz exists, install from that. The caller is responsible for resolving
// any repo_url fallbacks (e.g. etcd-backed `helm.repo.<name>.url`) before
// calling — see `defaultHelmRepoURL` in the orchestrator package for the
// historical lookup.
//
// Returns nil on a successful install, or a wrapped error describing
// whether remote / local / both failed.
func InstallPedestal(ctx context.Context, gw installGateway, spec PedestalInstallSpec) error {
	if gw == nil {
		return fmt.Errorf("helm gateway is nil")
	}
	if spec.ReleaseName == "" {
		return fmt.Errorf("release name is required")
	}
	if spec.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}

	logEntry := logrus.WithFields(logrus.Fields{
		"release_name": spec.ReleaseName,
		"namespace":    spec.Namespace,
	})

	hasLocal := spec.LocalPath != ""
	if hasLocal {
		if _, err := os.Stat(spec.LocalPath); err != nil {
			logEntry.Warnf("local chart %q not accessible (%v); will try remote install", spec.LocalPath, err)
			hasLocal = false
		}
	}

	hasRemote := spec.RepoURL != "" && spec.RepoName != ""

	var installErr error
	if hasRemote {
		logEntry.Infof("Attempting to install chart from remote repository: %s/%s", spec.RepoName, spec.ChartName)

		isOCI := strings.HasPrefix(spec.RepoURL, "oci://")
		var fullChart string
		if isOCI {
			// OCI registries don't expose an index.yaml; skip AddRepo/UpdateRepo
			// and let installAction.LocateChart pull the OCI reference directly.
			fullChart = strings.TrimRight(spec.RepoURL, "/") + "/" + spec.ChartName
		} else if err := gw.AddRepo(spec.ReleaseName, spec.RepoName, spec.RepoURL); err != nil {
			logEntry.Warnf("Failed to add repository: %v", err)
			installErr = err
		} else if err := gw.UpdateRepo(spec.ReleaseName, spec.RepoName); err != nil {
			logEntry.Warnf("Failed to update repository: %v", err)
			installErr = err
		} else {
			fullChart = fmt.Sprintf("%s/%s", spec.RepoName, spec.ChartName)
		}

		if installErr == nil && fullChart != "" {
			logEntry.WithField("chart", fullChart).
				WithField("version", spec.Version).
				Infof("Installing Helm chart from remote with parameters: %+v", spec.Values)

			if err := gw.Install(ctx,
				spec.Namespace,
				spec.ReleaseName,
				fullChart,
				spec.Version,
				spec.Values,
				spec.InstallTimeout,
				spec.UninstallTimeout,
			); err != nil {
				logEntry.Warnf("Failed to install chart from remote: %v", err)
				installErr = err
			} else {
				logEntry.Info("Helm chart installed successfully from remote repository")
				return nil
			}
		}
	}

	if hasLocal {
		if installErr != nil {
			logEntry.Infof("Remote installation failed, falling back to local chart: %s", spec.LocalPath)
		} else {
			logEntry.Infof("Installing chart from local path: %s", spec.LocalPath)
		}

		if err := gw.Install(ctx,
			spec.Namespace,
			spec.ReleaseName,
			spec.LocalPath,
			spec.Version,
			spec.Values,
			spec.InstallTimeout,
			spec.UninstallTimeout,
		); err != nil {
			return fmt.Errorf("failed to install chart from local path %s: %w", spec.LocalPath, err)
		}

		logEntry.Info("Helm chart installed successfully from local path")
		return nil
	}

	if installErr != nil {
		return fmt.Errorf("failed to install chart: remote installation failed and no local fallback available: %w", installErr)
	}

	return fmt.Errorf("no chart source configured (neither remote nor local)")
}
