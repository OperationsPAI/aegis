package cmd

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"aegis/cmd/aegisctl/client"
	"aegis/consts"

	"github.com/spf13/cobra"
)

// systemPublishChartRunner abstracts shelling out to the helm binary so tests
// can substitute a fake. Intentionally the same shape as chartExecRunner in
// pedestal_chart.go but left independent to avoid cross-test coupling.
type systemPublishChartRunner interface {
	LookPath(name string) (string, error)
	// Run executes name with args; stdout goes to outW, stderr to errW.
	// combined is true if outW and errW should both receive interleaved output.
	Run(name string, args []string, env []string) (stdout string, stderr string, err error)
}

type realSystemPublishRunner struct{}

func (realSystemPublishRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (realSystemPublishRunner) Run(name string, args []string, env []string) (string, string, error) {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var outB, errB strings.Builder
	cmd.Stdout = &outB
	cmd.Stderr = &errB
	err := cmd.Run()
	return outB.String(), errB.String(), err
}

// systemPublishRunner is the package-level indirection; tests swap this out.
var systemPublishRunner systemPublishChartRunner = realSystemPublishRunner{}

var (
	systemPublishChartBumpVersion bool
	systemPublishChartKeepTmp     bool
)

var systemPublishChartCmd = &cobra.Command{
	Use:   "publish-chart <name> <chart-dir>",
	Short: "Package a helm chart directory and push it to the system's OCI registry",
	Long: `Package the given chart directory with "helm package" and push the
resulting .tgz to the OCI registry referenced in helm_configs.repo_url for
the named system. The registry URL and chart name are resolved via
GET /api/v2/systems/by-name/<name>/chart.

Flow:
  1. Look up the system's chart metadata (repo_url, chart_name) from the backend.
  2. helm package <chart-dir> -d <tmp>   -> <chart-name>-<version>.tgz
  3. If env HELM_REGISTRY_USERNAME+HELM_REGISTRY_PASSWORD are set, run
     "helm registry login" against the registry host before pushing.
  4. helm push <tgz> <repo_url>
  5. helm show chart <oci-url>:<version>   (remote verification)
  6. If --bump-version is set, PUT /api/v2/pedestal/helm/<container_version_id>
     so helm_configs.version reflects the freshly published chart.

The helm binary must be on PATH (we shell out rather than pull in the SDK).
Errors and progress go to stderr; the final success line "published <oci>:<ver>"
goes to stdout so it is machine-parseable.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSystemPublishChart(args[0], args[1], systemPublishChartBumpVersion, systemPublishChartKeepTmp)
	},
}

func runSystemPublishChart(systemName, chartDir string, bumpVersion, keepTmp bool) error {
	systemName = strings.TrimSpace(systemName)
	chartDir = strings.TrimSpace(chartDir)
	if systemName == "" {
		return usageErrorf("system name is required")
	}
	if chartDir == "" {
		return usageErrorf("chart-dir is required")
	}
	info, err := os.Stat(chartDir)
	if err != nil {
		if os.IsNotExist(err) {
			return notFoundErrorf("chart-dir not found: %s", chartDir)
		}
		return fmt.Errorf("stat chart-dir: %w", err)
	}
	if !info.IsDir() {
		return usageErrorf("chart-dir must be a directory, got a file: %s", chartDir)
	}
	// helm package expects a Chart.yaml at the root.
	if _, err := os.Stat(filepath.Join(chartDir, "Chart.yaml")); err != nil {
		return usageErrorf("chart-dir %q does not contain a Chart.yaml", chartDir)
	}

	if _, err := systemPublishRunner.LookPath("helm"); err != nil {
		return missingEnvErrorf("helm not found on PATH; install helm or place it on PATH")
	}

	if err := requireAPIContext(true); err != nil {
		return err
	}
	c := newClient()

	// 1. Resolve chart coordinates via the backend.
	lookup, err := fetchSystemChart(c, systemName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(lookup.RepoURL) == "" {
		return notFoundErrorf("system %q has no repo_url in helm_configs; set it via aegisctl pedestal helm set first", systemName)
	}
	if !strings.HasPrefix(strings.ToLower(lookup.RepoURL), "oci://") {
		return usageErrorf("system %q repo_url %q is not an OCI URL (must start with oci://) — publish-chart only supports OCI registries",
			systemName, lookup.RepoURL)
	}

	// 2. helm package.
	tmpDir, err := os.MkdirTemp("", "aegisctl-publish-chart-*")
	if err != nil {
		return fmt.Errorf("create tmp dir: %w", err)
	}
	if !keepTmp {
		defer os.RemoveAll(tmpDir)
	} else {
		fmt.Fprintf(os.Stderr, "keeping tmp dir: %s\n", tmpDir)
	}

	fmt.Fprintf(os.Stderr, "+ helm package %s -d %s\n", chartDir, tmpDir)
	pkgOut, pkgErr, err := systemPublishRunner.Run("helm", []string{"package", chartDir, "-d", tmpDir}, nil)
	if pkgOut != "" {
		fmt.Fprint(os.Stderr, pkgOut)
	}
	if pkgErr != "" {
		fmt.Fprint(os.Stderr, pkgErr)
	}
	if err != nil {
		return fmt.Errorf("helm package failed: %w", err)
	}

	tgzPath, chartName, chartVersion, err := resolvePackagedChart(tmpDir, pkgOut, pkgErr)
	if err != nil {
		return err
	}
	if chartName != lookup.ChartName {
		// Non-fatal — helm push drives name/version from the tgz, and the
		// backend's chart_name may intentionally differ in alias/fork scenarios.
		// But warn so operators notice unintentional drift.
		fmt.Fprintf(os.Stderr, "warning: packaged chart name %q differs from backend chart_name %q (system %q)\n",
			chartName, lookup.ChartName, systemName)
	}

	// 3. Optional registry login (only if creds provided via env).
	if err := maybeRegistryLogin(lookup.RepoURL); err != nil {
		return err
	}

	// 4. helm push.
	fmt.Fprintf(os.Stderr, "+ helm push %s %s\n", tgzPath, lookup.RepoURL)
	pushOut, pushErr, err := systemPublishRunner.Run("helm", []string{"push", tgzPath, lookup.RepoURL}, nil)
	if pushOut != "" {
		fmt.Fprint(os.Stderr, pushOut)
	}
	if pushErr != "" {
		fmt.Fprint(os.Stderr, pushErr)
	}
	if err != nil {
		return fmt.Errorf("helm push failed: %w", err)
	}

	// 5. Remote verification via helm show chart.
	ociRef := buildOCIRef(lookup.RepoURL, chartName)
	fmt.Fprintf(os.Stderr, "+ helm show chart %s --version %s\n", ociRef, chartVersion)
	showOut, showErr, err := systemPublishRunner.Run("helm", []string{"show", "chart", ociRef, "--version", chartVersion}, nil)
	if showOut != "" {
		fmt.Fprint(os.Stderr, showOut)
	}
	if showErr != "" {
		fmt.Fprint(os.Stderr, showErr)
	}
	if err != nil {
		return fmt.Errorf("helm show chart %s@%s failed (push may not have landed on the registry): %w",
			ociRef, chartVersion, err)
	}

	// 6. Optional DB bump.
	if bumpVersion {
		if err := bumpHelmConfigVersion(c, systemName, lookup, chartName, chartVersion); err != nil {
			return fmt.Errorf("bump helm_configs.version: %w", err)
		}
		fmt.Fprintf(os.Stderr, "bumped helm_configs.version for system %q to %q\n", systemName, chartVersion)
	}

	// Machine-parseable success line on stdout.
	fmt.Fprintf(os.Stdout, "published %s:%s\n", ociRef, chartVersion)
	return nil
}

// fetchSystemChart calls the by-name chart endpoint (reuses the same response
// shape as pedestal_chart.go to keep the CLI binary independent of server
// types).
func fetchSystemChart(c *client.Client, systemName string) (*chartLookupResp, error) {
	var resp client.APIResponse[chartLookupResp]
	if err := c.Get(consts.APIPathSystemByNameChart(url.PathEscape(systemName)), &resp); err != nil {
		return nil, fmt.Errorf("lookup chart for system %q: %w", systemName, err)
	}
	return &resp.Data, nil
}

// packagedChartLine matches "Successfully packaged chart and saved it to: <path>"
// (helm 3 prints this to stdout).
var packagedChartLine = regexp.MustCompile(`saved it to:\s*(\S+\.tgz)`) //nolint:gochecknoglobals

// chartTgzName matches "<chart-name>-<semver>.tgz". helm package filenames are
// always of that form. We use the last "-" as a delimiter and treat the right
// side as the version.
var chartTgzName = regexp.MustCompile(`^(.+)-([^-]+)\.tgz$`) //nolint:gochecknoglobals

// resolvePackagedChart finds the .tgz produced by "helm package" and parses
// its filename into (chartName, version). It prefers the explicit path printed
// by helm, falling back to a directory scan for robustness.
func resolvePackagedChart(tmpDir, stdout, stderr string) (tgzPath, chartName, chartVersion string, err error) {
	combined := stdout + "\n" + stderr
	if m := packagedChartLine.FindStringSubmatch(combined); m != nil {
		tgzPath = m[1]
	}
	if tgzPath == "" {
		// Fallback: scan tmpDir for the sole .tgz.
		entries, readErr := os.ReadDir(tmpDir)
		if readErr != nil {
			return "", "", "", fmt.Errorf("could not locate packaged chart: %w", readErr)
		}
		var candidates []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".tgz") {
				candidates = append(candidates, filepath.Join(tmpDir, e.Name()))
			}
		}
		if len(candidates) != 1 {
			return "", "", "", fmt.Errorf("expected exactly one .tgz in %s, found %d", tmpDir, len(candidates))
		}
		tgzPath = candidates[0]
	}
	base := filepath.Base(tgzPath)
	m := chartTgzName.FindStringSubmatch(base)
	if m == nil {
		return "", "", "", fmt.Errorf("cannot parse chart name/version from %q (expected <name>-<version>.tgz)", base)
	}
	return tgzPath, m[1], m[2], nil
}

// maybeRegistryLogin runs "helm registry login <host>" only if both credential
// env vars are set. Helm's own config (set by a prior login) is used otherwise.
func maybeRegistryLogin(repoURL string) error {
	user := os.Getenv("HELM_REGISTRY_USERNAME")
	pass := os.Getenv("HELM_REGISTRY_PASSWORD")
	if user == "" || pass == "" {
		return nil
	}
	host, err := ociRegistryHost(repoURL)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "+ helm registry login %s --username %s --password-stdin\n", host, user)
	// helm registry login reads the password from stdin when --password-stdin
	// is passed. We can't rely on the abstract Run() to pipe stdin, so fall
	// back to --password (which is what helm also supports). Not ideal for
	// shell history, but aegisctl is not shown in history in CI contexts.
	_, errOut, err := systemPublishRunner.Run("helm",
		[]string{"registry", "login", host, "--username", user, "--password", pass}, nil)
	if errOut != "" {
		fmt.Fprint(os.Stderr, errOut)
	}
	if err != nil {
		return fmt.Errorf("helm registry login %s failed: %w", host, err)
	}
	return nil
}

// ociRegistryHost extracts the host[:port] from an oci:// URL for use with
// "helm registry login".
func ociRegistryHost(repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("parse repo_url %q: %w", repoURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("repo_url %q has no host", repoURL)
	}
	return u.Host, nil
}

// buildOCIRef composes the canonical oci://<repo>/<chart> reference used by
// helm show/pull. helm push uploads to <repo_url>/<chart-name>; show must
// target that exact path.
func buildOCIRef(repoURL, chartName string) string {
	return strings.TrimRight(repoURL, "/") + "/" + chartName
}

// bumpHelmConfigVersion resolves the system's active container_version_id and
// upserts the helm_configs row with the new version. Other fields are carried
// over from the current chart lookup so the PUT does not accidentally clear
// repo_name / value_file / local_path.
func bumpHelmConfigVersion(c *client.Client, systemName string, lookup *chartLookupResp, chartName, chartVersion string) error {
	cvID, err := resolveSystemContainerVersionID(c, systemName, lookup.PedestalTag)
	if err != nil {
		return err
	}
	body := pedestalHelmSetReq{
		ChartName: chartName,
		Version:   chartVersion,
		RepoURL:   lookup.RepoURL,
		RepoName:  lookup.RepoName,
		ValueFile: lookup.ValueFile,
		LocalPath: lookup.LocalPath,
	}
	var resp client.APIResponse[pedestalHelmConfig]
	if err := c.Put(consts.APIPathPedestalHelmByID(cvID), body, &resp); err != nil {
		return fmt.Errorf("PUT /api/v2/pedestal/helm/%d: %w", cvID, err)
	}
	return nil
}

// resolveSystemContainerVersionID walks /api/v2/containers + /versions to find
// the container_version row that backs the system's active pedestal. The
// container name convention is that container.name == system.name (verified by
// chaossystem.GetPedestalHelmConfigByName in the backend).
func resolveSystemContainerVersionID(c *client.Client, systemName, pedestalTag string) (int, error) {
	r := client.NewResolver(c)
	containerID, err := r.ContainerID(systemName)
	if err != nil {
		return 0, fmt.Errorf("resolve container id for system %q: %w", systemName, err)
	}
	var vResp client.APIResponse[client.PaginatedData[containerVersionItem]]
	if err := c.Get(consts.APIPathContainerVersionsFor(containerID)+"?page=1&size=1000", &vResp); err != nil {
		return 0, fmt.Errorf("list versions for container %q: %w", systemName, err)
	}
	for _, v := range vResp.Data.Items {
		if v.Name == pedestalTag {
			return v.ID, nil
		}
	}
	return 0, notFoundErrorf("container version with name %q not found under container %q; cannot bump helm_configs.version",
		pedestalTag, systemName)
}

func init() {
	systemPublishChartCmd.Flags().BoolVar(&systemPublishChartBumpVersion, "bump-version", false,
		"After a successful push, update helm_configs.version via PUT /api/v2/pedestal/helm/<id>")
	systemPublishChartCmd.Flags().BoolVar(&systemPublishChartKeepTmp, "keep-tmp", false,
		"Keep the temporary directory used for helm package (for debugging)")

	systemCmd.AddCommand(systemPublishChartCmd)
}
