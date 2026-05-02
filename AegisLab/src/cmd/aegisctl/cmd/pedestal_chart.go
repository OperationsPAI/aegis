package cmd

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// chartExecRunner abstracts shelling out to external binaries (kubectl / helm)
// so tests can inject a fake instead of requiring a live cluster.
type chartExecRunner interface {
	// LookPath reports whether a binary is on PATH. Empty string + nil means
	// "not found" (matches exec.LookPath semantics loosely).
	LookPath(name string) (string, error)
	// Run executes name with args and returns combined stdout+stderr.
	Run(name string, args ...string) ([]byte, error)
}

type realChartExec struct{}

func (realChartExec) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (realChartExec) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// chartRunner is the package-level indirection; tests swap this out.
var chartRunner chartExecRunner = realChartExec{}

// Default location inside the producer pod where helm charts are cached.
const producerChartDir = "/var/lib/rcabench/dataset/charts"

// Default namespace where the aegis backend / producer runs.
const defaultBackendNamespace = "aegislab-backend"

var backendNamespaceHints = []string{"exp", defaultBackendNamespace}

var pedestalChartCmd = &cobra.Command{
	Use:   "chart",
	Short: "Distribute and install pedestal helm charts",
	Long: `Automate the manual dance of copying a packaged helm chart into the
producer pod and pre-installing it so the first guided-inject submit does
not fail on "app=<system>" label resolution.`,
}

// --- chart push ---

var (
	pedestalChartPushName        string
	pedestalChartPushTgz         string
	pedestalChartPushProducerPod string
	pedestalChartPushNamespace   string
)

var pedestalChartPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Copy a packaged chart (.tgz) into the producer pod's chart cache",
	Long: `Copy a packaged chart (.tgz) into the producer pod's chart cache at
` + producerChartDir + `. This survives pod restart only as long as that
directory is on a PVC; on pod rollout the push may need to be re-run.

The producer pod is auto-resolved by listing pods with a label containing
"producer" in the aegislab-backend namespace. Override with --producer-pod
if your deployment uses a different naming scheme.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPedestalChartPush(pedestalChartPushName, pedestalChartPushTgz,
			pedestalChartPushProducerPod, pedestalChartPushNamespace)
	},
}

func runPedestalChartPush(name, tgz, producerPod, namespace string) error {
	if strings.TrimSpace(name) == "" {
		return usageErrorf("--name is required")
	}
	if strings.TrimSpace(tgz) == "" {
		return usageErrorf("--tgz is required")
	}
	info, err := os.Stat(tgz)
	if err != nil {
		if os.IsNotExist(err) {
			return notFoundErrorf("--tgz file not found: %s", tgz)
		}
		return fmt.Errorf("stat --tgz: %w", err)
	}
	if info.IsDir() {
		return usageErrorf("--tgz must be a file, got directory: %s", tgz)
	}

	if namespace == "" {
		namespace = defaultBackendNamespace
	}

	// Require kubectl on PATH. We deliberately do not reimplement kubectl cp
	// via client-go's exec+tar because the shell-out form is 10 lines of code
	// instead of ~150 and the CLI is a reasonable assumption for an operator
	// tool that already expects a kubeconfig.
	if _, err := chartRunner.LookPath("kubectl"); err != nil {
		return missingEnvErrorf("kubectl not found on PATH; install kubectl or place it on PATH")
	}

	if producerPod == "" {
		resolved, err := resolveProducerPod(namespace)
		if err != nil {
			return err
		}
		producerPod = resolved
	}

	basename := filepath.Base(tgz)
	remotePath := producerChartDir + "/" + basename

	cpArgs := []string{"-n", namespace, "cp", tgz, producerPod + ":" + remotePath}
	output.PrintInfo(fmt.Sprintf("+ kubectl %s", strings.Join(cpArgs, " ")))
	if out, err := chartRunner.Run("kubectl", cpArgs...); err != nil {
		return fmt.Errorf("kubectl cp failed: %w\n%s", err, string(out))
	}

	// Verify: ls -l target path inside pod.
	verifyArgs := []string{"-n", namespace, "exec", producerPod, "--", "ls", "-l", remotePath}
	out, err := chartRunner.Run("kubectl", verifyArgs...)
	if err != nil {
		return fmt.Errorf("verify ls failed: %w\n%s", err, string(out))
	}
	output.PrintInfo(fmt.Sprintf("chart pushed to %s:%s", producerPod, remotePath))
	fmt.Print(string(out))
	return nil
}

// resolveProducerPod lists pods in namespace and picks the first one whose
// app label (or pod name) contains "producer". Returns a usage-grade error if
// nothing matches so the caller sees a clear hint.
func resolveProducerPod(namespace string) (string, error) {
	// Try label selector first (fast + robust to naming drift).
	for _, selector := range []string{"app=aegislab-producer", "app.kubernetes.io/component=producer"} {
		args := []string{"-n", namespace, "get", "pods", "-l", selector,
			"-o", "jsonpath={.items[0].metadata.name}"}
		out, err := chartRunner.Run("kubectl", args...)
		if err == nil {
			name := strings.TrimSpace(string(out))
			if name != "" {
				return name, nil
			}
		}
	}
	// Fallback: grep pod names for "producer".
	args := []string{"-n", namespace, "get", "pods", "-o", "jsonpath={.items[*].metadata.name}"}
	out, err := chartRunner.Run("kubectl", args...)
	if err != nil {
		return "", fmt.Errorf("resolve producer pod in namespace %q: %w\n%s", namespace, err, string(out))
	}
	for _, n := range strings.Fields(string(out)) {
		if strings.Contains(n, "producer") {
			return n, nil
		}
	}
	return "", notFoundErrorf("no producer pod found in namespace %q (tried app=aegislab-producer and name-match); pass --producer-pod explicitly", namespace)
}

// --- chart install ---

var (
	pedestalChartInstallNamespace          string
	pedestalChartInstallTgz                string
	pedestalChartInstallRepo               string
	pedestalChartInstallChart              string
	pedestalChartInstallVersion            string
	pedestalChartInstallWait               bool
	pedestalChartInstallApplyOverrides     bool
	pedestalChartInstallFromPedestalVerArg string
)

var pedestalChartInstallCmd = &cobra.Command{
	Use:   "install <system-short-code>",
	Short: "helm install a chart for a system, creating its namespace if needed",
	Long: `Pre-install a pedestal chart so the first guided-inject submit finds
live "app=<svc>" pods (backend validates this against the cluster; without a
pre-install the chicken-and-egg causes submit to fail).

Namespace resolution order:
  --namespace <ns>               (explicit wins)
  derived from system's ns_pattern via GET /api/v2/systems
    (e.g. "^ts\\d+$" -> "ts0")

Chart source resolution (first match wins):
  --tgz <path-or-url>            local .tgz, or https:// URL of a packaged chart
  --repo <url> --chart <name>    helm-style repo install (passed as
                                 "helm install <ns> <chart> --repo <url>")
  else: GET /api/v2/systems/by-name/<code>/chart and use
        local_path -> repo_url/chart_name -> error.

The release name is set to the namespace (matches aegis's own
installPedestal behavior so app labels line up).

By default the chart's value_file (raw YAML on the producer pod) is passed
to helm as the only -f source, matching historical behaviour. Pass
--apply-overrides to instead use the merged values map returned by
/api/v2/systems/by-name/<code>/chart, which already overlays the
helm_config_values DB rows on top of value_file (matching what the
RestartPedestal pipeline does internally). Pin a specific pedestal
container_version with --from-pedestal-version <ver>; otherwise the
backend's latest active row wins.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPedestalChartInstall(args[0], pedestalChartInstallNamespace,
			pedestalChartInstallTgz, pedestalChartInstallRepo, pedestalChartInstallChart,
			pedestalChartInstallVersion, pedestalChartInstallWait,
			pedestalChartInstallApplyOverrides, pedestalChartInstallFromPedestalVerArg)
	},
}

// chartSource captures the resolved helm positional + optional --repo flag.
// `positional` is what goes as the second helm-install arg (can be local path,
// URL, or chart name); `repo` is non-empty only when positional is a repo
// chart name that needs --repo.
type chartSource struct {
	positional     string
	repo           string
	version        string
	valuesFile     string
	values         map[string]any
	applyOverrides bool
	pedestalTag    string
}

func runPedestalChartInstall(systemCode, namespace, tgz, repo, chartName, version string, wait bool, applyOverrides bool, fromPedestalVersion string) error {
	if strings.TrimSpace(systemCode) == "" {
		return usageErrorf("system short-code is required")
	}

	// --from-pedestal-version pins which container_versions.name the backend
	// should resolve helm_config_values for. When set it takes precedence over
	// --version for the `?version=` query string, but not over helm's own
	// chart --version flag (those are independent semvers — chart version
	// vs. container_version semver, see GetSystemChart docstring).
	apiQueryVersion := strings.TrimSpace(fromPedestalVersion)
	if apiQueryVersion == "" {
		apiQueryVersion = version
	}

	if namespace == "" {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		derived, err := deriveNamespaceFromSystem(systemCode)
		if err != nil {
			return err
		}
		namespace = derived
	}

	src, err := resolveChartSource(systemCode, tgz, repo, chartName, version, apiQueryVersion)
	if err != nil {
		return err
	}
	src.applyOverrides = applyOverrides

	if _, err := chartRunner.LookPath("helm"); err != nil {
		return missingEnvErrorf("helm not found on PATH; install helm or place it on PATH")
	}

	if applyOverrides {
		tag := src.pedestalTag
		if tag == "" {
			tag = apiQueryVersion
		}
		output.PrintInfo(fmt.Sprintf("merged %d helm_config_values overrides for %s@%s", len(src.values), systemCode, tag))
	}

	helmArgs := []string{"install", namespace, src.positional, "-n", namespace, "--create-namespace"}
	cleanup := func() {}
	if valuesFile, fileCleanup, err := materializeChartValuesFile(src); err != nil {
		return err
	} else if valuesFile != "" {
		helmArgs = append(helmArgs, "-f", valuesFile)
		cleanup = fileCleanup
	}
	defer cleanup()
	if src.repo != "" {
		helmArgs = append(helmArgs, "--repo", src.repo)
	}
	if src.version != "" {
		helmArgs = append(helmArgs, "--version", src.version)
	}
	if wait {
		helmArgs = append(helmArgs, "--wait")
	}
	// Mirror kubectl's --v=2-style preview so operators can copy-paste.
	output.PrintInfo(fmt.Sprintf("+ helm %s", strings.Join(helmArgs, " ")))
	out, err := chartRunner.Run("helm", helmArgs...)
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		return fmt.Errorf("helm install failed: %w", err)
	}
	output.PrintInfo(fmt.Sprintf("installed chart %q as release %q in namespace %q", src.positional, namespace, namespace))
	return nil
}

// isURL returns true for http(s), oci, and file URLs that helm accepts as a
// positional chart source without a local stat check.
func isURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "oci://") ||
		strings.HasPrefix(lower, "file://")
}

// resolveChartSource picks the helm positional argument (and optional --repo)
// based on the precedence documented on pedestalChartInstallCmd.Long. It falls
// back to GET /api/v2/systems/by-name/<code>/chart when the operator didn't
// pass a source explicitly. apiQueryVersion is the value passed as ?version=
// (the container_version.name) and may differ from chartVersion (the helm
// chart semver passed to `helm --version`) — see #372.
func resolveChartSource(systemCode, tgz, repo, chartName, chartVersion, apiQueryVersion string) (chartSource, error) {
	switch {
	case tgz != "":
		if isURL(tgz) {
			return chartSource{positional: tgz, version: chartVersion}, nil
		}
		if _, err := os.Stat(tgz); err != nil {
			if os.IsNotExist(err) {
				return chartSource{}, notFoundErrorf("--tgz file not found: %s", tgz)
			}
			return chartSource{}, fmt.Errorf("stat --tgz: %w", err)
		}
		return chartSource{positional: tgz, version: chartVersion}, nil

	case repo != "" && chartName != "":
		if strings.HasPrefix(repo, "oci://") {
			return chartSource{positional: buildOCIRef(repo, chartName), version: chartVersion}, nil
		}
		return chartSource{positional: chartName, repo: repo, version: chartVersion}, nil

	case repo != "" || chartName != "":
		return chartSource{}, usageErrorf("--repo and --chart must be provided together")
	}

	// No explicit source — consult the backend.
	if err := requireAPIContext(true); err != nil {
		return chartSource{}, err
	}
	c := newClient()
	var resp client.APIResponse[chartLookupResp]
	// Pass apiQueryVersion through as a query string so the backend can
	// return the helm_config_values for that specific container_version
	// (issue #190 / #372). Empty value preserves the old "latest semver"
	// behaviour.
	chartPath := fmt.Sprintf("/api/v2/systems/by-name/%s/chart", systemCode)
	if apiQueryVersion != "" {
		chartPath = fmt.Sprintf("%s?version=%s", chartPath, url.QueryEscape(apiQueryVersion))
	}
	if err := c.Get(chartPath, &resp); err != nil {
		return chartSource{}, fmt.Errorf("lookup chart for system %q: %w (hint: pass --tgz or --repo/--chart explicitly)", systemCode, err)
	}
	backendVersion := chartVersion
	if backendVersion == "" {
		backendVersion = resp.Data.Version
	}
	// Local path wins over repo lookup when the file is actually present —
	// this is the air-gapped / pre-staged case.
	if resp.Data.LocalPath != "" {
		if _, err := os.Stat(resp.Data.LocalPath); err == nil {
			return chartSource{
				positional:  resp.Data.LocalPath,
				version:     backendVersion,
				valuesFile:  resp.Data.ValueFile,
				values:      resp.Data.Values,
				pedestalTag: resp.Data.PedestalTag,
			}, nil
		}
	}
	if resp.Data.RepoURL != "" && resp.Data.ChartName != "" {
		if strings.HasPrefix(resp.Data.RepoURL, "oci://") {
			return chartSource{
				positional:  buildOCIRef(resp.Data.RepoURL, resp.Data.ChartName),
				version:     backendVersion,
				valuesFile:  resp.Data.ValueFile,
				values:      resp.Data.Values,
				pedestalTag: resp.Data.PedestalTag,
			}, nil
		}
		return chartSource{
			positional:  resp.Data.ChartName,
			repo:        resp.Data.RepoURL,
			version:     backendVersion,
			valuesFile:  resp.Data.ValueFile,
			values:      resp.Data.Values,
			pedestalTag: resp.Data.PedestalTag,
		}, nil
	}
	return chartSource{}, notFoundErrorf("system %q has no installable chart source (no local_path, no repo_url); pass --tgz or --repo/--chart", systemCode)
}

func materializeChartValuesFile(src chartSource) (string, func(), error) {
	// applyOverrides flips the precedence: prefer the merged values map
	// (file YAML overlaid with helm_config_values DB rows by the backend)
	// over the raw value_file path. Without this flag, the raw file wins
	// even when DB overrides drift away from it — see #372.
	if src.applyOverrides && len(src.values) > 0 {
		data, err := yaml.Marshal(src.values)
		if err != nil {
			return "", func() {}, fmt.Errorf("marshal chart values: %w", err)
		}
		return writeTempChartValuesFile(data)
	}
	if src.valuesFile != "" {
		if _, err := os.Stat(src.valuesFile); err == nil {
			return src.valuesFile, func() {}, nil
		}
		if data, err := fetchRemoteChartValuesFile(src.valuesFile); err == nil && len(data) > 0 {
			return writeTempChartValuesFile(data)
		}
	}
	if len(src.values) == 0 {
		return "", func() {}, nil
	}
	data, err := yaml.Marshal(src.values)
	if err != nil {
		return "", func() {}, fmt.Errorf("marshal chart values: %w", err)
	}
	return writeTempChartValuesFile(data)
}

func writeTempChartValuesFile(data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "aegisctl-chart-values-*.yaml")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp chart values file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write temp chart values file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("close temp chart values file: %w", err)
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

func fetchRemoteChartValuesFile(remotePath string) ([]byte, error) {
	if strings.TrimSpace(remotePath) == "" {
		return nil, fmt.Errorf("remote chart values path is empty")
	}
	if _, err := chartRunner.LookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl not found on PATH; cannot read remote chart values %q", remotePath)
	}
	namespace, pod, err := resolveBackendValuesPod()
	if err != nil {
		return nil, err
	}
	out, err := chartRunner.Run("kubectl", "-n", namespace, "exec", pod, "--", "cat", remotePath)
	if err != nil {
		return nil, fmt.Errorf("read remote chart values %q from %s/%s: %w\n%s", remotePath, namespace, pod, err, string(out))
	}
	return out, nil
}

func resolveBackendValuesPod() (string, string, error) {
	if ns, pod := resolveBackendValuesPodBySelector("app.kubernetes.io/component=api-gateway"); pod != "" {
		return ns, pod, nil
	}
	if ns, pod := resolveBackendValuesPodBySelector("app.kubernetes.io/component=producer"); pod != "" {
		return ns, pod, nil
	}

	out, err := chartRunner.Run("kubectl", "get", "pods", "-A", "-o", "jsonpath={range .items[*]}{.metadata.namespace}{\"\\t\"}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		return "", "", fmt.Errorf("list backend pods for remote values lookup: %w\n%s", err, string(out))
	}

	type match struct {
		namespace string
		pod       string
		score     int
	}
	best := match{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ns, pod := fields[0], fields[1]
		score := backendPodMatchScore(ns, pod)
		if score > best.score {
			best = match{namespace: ns, pod: pod, score: score}
		}
	}
	if best.score > 0 {
		return best.namespace, best.pod, nil
	}
	return "", "", notFoundErrorf("could not find an api-gateway or producer pod to read remote helm values")
}

func resolveBackendValuesPodBySelector(selector string) (string, string) {
	out, err := chartRunner.Run("kubectl", "get", "pods", "-A", "-l", selector, "-o", "jsonpath={range .items[*]}{.metadata.namespace}{\"\\t\"}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		return "", ""
	}
	bestNS, bestPod, bestScore := "", "", 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ns, pod := fields[0], fields[1]
		score := backendPodMatchScore(ns, pod)
		if score > bestScore {
			bestNS, bestPod, bestScore = ns, pod, score
		}
	}
	return bestNS, bestPod
}

func backendPodMatchScore(namespace, pod string) int {
	if namespace == "" || pod == "" {
		return 0
	}
	score := 1
	for i, hint := range backendNamespaceHints {
		if namespace == hint {
			score += 20 - i
			break
		}
	}
	switch {
	case strings.Contains(pod, "api-gateway"):
		score += 10
	case strings.Contains(pod, "producer"):
		score += 8
	}
	return score
}

// chartLookupResp mirrors chaossystem.SystemChartResp; kept decoupled so the
// CLI has no direct dependency on server-internal types.
type chartLookupResp struct {
	SystemName  string         `json:"system_name"`
	ChartName   string         `json:"chart_name"`
	Version     string         `json:"version"`
	RepoURL     string         `json:"repo_url"`
	RepoName    string         `json:"repo_name"`
	LocalPath   string         `json:"local_path"`
	ValueFile   string         `json:"value_file"`
	Values      map[string]any `json:"values,omitempty"`
	Checksum    string         `json:"checksum"`
	PedestalTag string         `json:"pedestal_tag"`
}

// deriveNamespaceFromSystem calls GET /api/v2/systems and converts the named
// system's ns_pattern to a concrete namespace using the same regex->template
// logic as the backend's convertPatternToTemplate.
func deriveNamespaceFromSystem(systemCode string) (string, error) {
	c := newClient()
	type systemItem struct {
		Name      string `json:"name"`
		NsPattern string `json:"ns_pattern"`
	}
	var resp client.APIResponse[client.PaginatedData[systemItem]]
	if err := c.Get("/api/v2/systems?page=1&size=100", &resp); err != nil {
		return "", fmt.Errorf("list systems: %w", err)
	}
	for _, s := range resp.Data.Items {
		if s.Name == systemCode {
			ns := nsPatternToNamespace(s.NsPattern, 0)
			if ns == "" {
				return "", fmt.Errorf("cannot derive namespace from ns_pattern %q for system %q; pass --namespace explicitly",
					s.NsPattern, systemCode)
			}
			return ns, nil
		}
	}
	return "", notFoundErrorf("system %q not found via /api/v2/systems; pass --namespace explicitly", systemCode)
}

// nsPatternToNamespace mirrors config.convertPatternToTemplate + Sprintf(idx).
// Exported to the package (lowercase) so tests can cover it directly.
var nsPatternDigitsRe = regexp.MustCompile(`\\d\+`)

func nsPatternToNamespace(pattern string, idx int) string {
	tmpl := pattern
	tmpl = strings.TrimPrefix(tmpl, "^")
	tmpl = strings.TrimSuffix(tmpl, "$")
	tmpl = nsPatternDigitsRe.ReplaceAllString(tmpl, "%d")
	if tmpl == "" {
		return ""
	}
	if !strings.Contains(tmpl, "%d") {
		// Literal pattern (no digit slot) — just use it as-is.
		return tmpl
	}
	return fmt.Sprintf(tmpl, idx)
}

func init() {
	pedestalChartPushCmd.Flags().StringVar(&pedestalChartPushName, "name", "", "System short-code the chart belongs to (required, used only for messaging today)")
	pedestalChartPushCmd.Flags().StringVar(&pedestalChartPushTgz, "tgz", "", "Path to the packaged helm chart .tgz (required)")
	pedestalChartPushCmd.Flags().StringVar(&pedestalChartPushProducerPod, "producer-pod", "", "Producer pod name (auto-resolved in aegislab-backend namespace if omitted)")
	pedestalChartPushCmd.Flags().StringVar(&pedestalChartPushNamespace, "backend-namespace", defaultBackendNamespace, "Namespace where the producer pod lives")

	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallNamespace, "namespace", "", "Target namespace (derived from system ns_pattern if omitted)")
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallTgz, "tgz", "", "Local .tgz path OR https://... URL (wins over backend lookup and --repo)")
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallRepo, "repo", "", "Helm chart repo URL (use with --chart)")
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallChart, "chart", "", "Chart name in the repo given by --repo")
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallVersion, "version", "", "Chart version (passed to helm --version)")
	pedestalChartInstallCmd.Flags().BoolVar(&pedestalChartInstallWait, "wait", false, "Pass --wait to helm install")
	pedestalChartInstallCmd.Flags().BoolVar(&pedestalChartInstallApplyOverrides, "apply-overrides", false, "Merge helm_config_values rows for the matched pedestal version into the values file before helm install (matches RestartPedestal behaviour). Default: false (current behaviour preserved).")
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallFromPedestalVerArg, "from-pedestal-version", "", "Pin which container_versions.name the backend should resolve helm_config_values for (default: latest status=1 row for this system).")

	pedestalChartCmd.AddCommand(pedestalChartPushCmd)
	pedestalChartCmd.AddCommand(pedestalChartInstallCmd)
	pedestalCmd.AddCommand(pedestalChartCmd)
}
