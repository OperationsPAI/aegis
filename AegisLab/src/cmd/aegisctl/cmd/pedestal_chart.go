package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
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
	pedestalChartInstallNamespace string
	pedestalChartInstallTgz       string
	pedestalChartInstallVersion   string
	pedestalChartInstallWait      bool
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

Chart source resolution:
  --tgz <path>                   (local file wins)
  else: GET /api/v2/pedestal/helm/<container_version_id> for chart_name +
        repo_url + version  (NOTE: requires knowing a container_version_id
        — not yet resolvable by system short-code; see TODO below).

The release name is set to the namespace (matches aegis's own
installPedestal behavior so app labels line up).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPedestalChartInstall(args[0], pedestalChartInstallNamespace,
			pedestalChartInstallTgz, pedestalChartInstallVersion, pedestalChartInstallWait)
	},
}

func runPedestalChartInstall(systemCode, namespace, tgz, version string, wait bool) error {
	if strings.TrimSpace(systemCode) == "" {
		return usageErrorf("system short-code is required")
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

	source := tgz
	if source == "" {
		// Backend lookup by system short-code is not yet wired up:
		// /api/v2/pedestal/helm/<id> is keyed by container_version_id, not by
		// system short-code. Until Agent D lands the systems -> chart
		// resolution endpoint, require --tgz for this path.
		return usageErrorf("--tgz is required (backend lookup by system short-code is not yet implemented; pass a local .tgz)")
	}
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return notFoundErrorf("--tgz file not found: %s", source)
		}
		return fmt.Errorf("stat --tgz: %w", err)
	}

	if _, err := chartRunner.LookPath("helm"); err != nil {
		return missingEnvErrorf("helm not found on PATH; install helm or place it on PATH")
	}

	helmArgs := []string{"install", namespace, source, "-n", namespace, "--create-namespace"}
	if version != "" {
		helmArgs = append(helmArgs, "--version", version)
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
	output.PrintInfo(fmt.Sprintf("installed chart %q as release %q in namespace %q", source, namespace, namespace))
	return nil
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
	if err := c.Get("/api/v2/systems?page=1&size=200", &resp); err != nil {
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
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallTgz, "tgz", "", "Local .tgz to install (wins over backend lookup)")
	pedestalChartInstallCmd.Flags().StringVar(&pedestalChartInstallVersion, "version", "", "Chart version (passed to helm --version)")
	pedestalChartInstallCmd.Flags().BoolVar(&pedestalChartInstallWait, "wait", false, "Pass --wait to helm install")

	pedestalChartCmd.AddCommand(pedestalChartPushCmd)
	pedestalChartCmd.AddCommand(pedestalChartInstallCmd)
	pedestalCmd.AddCommand(pedestalChartCmd)
}
