package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// containerRegister flags. Every flag maps 1:1 to a field in the atomic
// POST /api/v2/containers/register payload — see
// module/container/register.go for the contract.
var (
	crFlagPedestal  bool
	crFlagBenchmark bool

	crFlagName     string
	crFlagRegistry string
	crFlagRepo     string
	crFlagTag      string
	crFlagVersion  string

	// Benchmark-only.
	crFlagCommand string
	crFlagEnv     []string

	// Pedestal-only.
	crFlagChartName    string
	crFlagChartVersion string
	crFlagRepoURL      string
	crFlagRepoName     string
	crFlagValuesFile   string

	// Observability knob. When set, each HTTP request/response pair is
	// printed to stderr — including the register_id echoed by the server
	// on both success and failure so an operator reading logs after the
	// fact can grep forward from the CLI-side record.
	crFlagVerbose bool
)

// containerRegisterRequest mirrors module/container.RegisterContainerReq.
// Kept local so the CLI has no server-package dependency.
type containerRegisterRequest struct {
	Form string `json:"form"`

	Name     string `json:"name"`
	Registry string `json:"registry,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Version  string `json:"version,omitempty"`

	Command string                 `json:"command,omitempty"`
	EnvVars []containerRegisterEnv `json:"env,omitempty"`

	ChartName    string `json:"chart_name,omitempty"`
	ChartVersion string `json:"chart_version,omitempty"`
	RepoURL      string `json:"repo_url,omitempty"`
	RepoName     string `json:"repo_name,omitempty"`
	ValuesFile   string `json:"values_file,omitempty"`
}

type containerRegisterEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// containerRegisterResponse mirrors RegisterContainerResp.
type containerRegisterResponse struct {
	RegisterID       string `json:"register_id"`
	ContainerID      int    `json:"container_id"`
	ContainerName    string `json:"container_name"`
	ContainerType    string `json:"container_type"`
	VersionID        int    `json:"version_id"`
	VersionName      string `json:"version_name"`
	ImageRef         string `json:"image_ref"`
	HelmConfigID     int    `json:"helm_config_id,omitempty"`
	ChartName        string `json:"chart_name,omitempty"`
	ChartVersion     string `json:"chart_version,omitempty"`
	ContainerExisted bool   `json:"container_existed"`
}

var containerRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Atomically register a container trio (pedestal or benchmark)",
	Long: `Atomically create a (container, container_version, helm_config) row trio
(for pedestals) or a (container, container_version) pair (for benchmarks)
by calling POST /api/v2/containers/register. The backend runs the inserts
in a single DB transaction and emits stage-tagged logs correlated by a
register_id that is ALSO returned in this CLI's output.

Use --verbose to mirror every HTTP request/response to stderr so a
mid-flight failure can be cross-referenced against server logs.`,
	Example: `  # pedestal form
  aegisctl container register --pedestal \
    --name ob --registry docker.io --repo opspai --tag 0.1.1 \
    --chart-name onlineboutique-aegis --chart-version 0.1.1 \
    --repo-url oci://registry-1.docker.io/opspai --repo-name opspai

  # benchmark form
  aegisctl container register --benchmark \
    --name ob-bench --registry docker.io --repo opspai/clickhouse_dataset --tag e2e-X \
    --command 'bash /entrypoint.sh' --env FOO=bar --env BAZ=qux`,
	RunE: runContainerRegister,
}

func runContainerRegister(cmd *cobra.Command, args []string) error {
	if crFlagPedestal == crFlagBenchmark {
		return usageErrorf("exactly one of --pedestal / --benchmark must be set")
	}
	if strings.TrimSpace(crFlagName) == "" {
		return usageErrorf("--name is required")
	}

	req := containerRegisterRequest{
		Name:         crFlagName,
		Registry:     crFlagRegistry,
		Repo:         crFlagRepo,
		Tag:          crFlagTag,
		Version:      crFlagVersion,
		Command:      crFlagCommand,
		ChartName:    crFlagChartName,
		ChartVersion: crFlagChartVersion,
		RepoURL:      crFlagRepoURL,
		RepoName:     crFlagRepoName,
		ValuesFile:   crFlagValuesFile,
	}
	if crFlagPedestal {
		req.Form = "pedestal"
	} else {
		req.Form = "benchmark"
	}

	for _, kv := range crFlagEnv {
		// resetFlagSet (used by CLI tests) replays DefValue through
		// Set(), which for StringArrayValue stringifies the empty slice
		// as "[]". Skip that single artefact so tests don't need to
		// special-case it.
		if kv == "" || kv == "[]" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return usageErrorf("--env must be KEY=VALUE (got %q)", kv)
		}
		req.EnvVars = append(req.EnvVars, containerRegisterEnv{Key: parts[0], Value: parts[1]})
	}

	// --verbose: emit request payload to stderr BEFORE we hit the server so
	// that, if the TCP call itself stalls, the operator still sees what we
	// were about to send.
	if crFlagVerbose {
		if b, err := json.MarshalIndent(req, "", "  "); err == nil {
			fmt.Fprintf(os.Stderr, "[verbose] POST /api/v2/containers/register\n%s\n", string(b))
		}
	}

	// Kept on the manual client: the typed apiclient's GenericOpenAPIError
	// formatter only inspects "Title"/"Detail" fields and discards the
	// backend's response `message`, which is where the register_id and
	// stage tag live. Tests (and operators reading stderr) depend on
	// those bytes surviving in the error string.
	c := newClient()
	var resp client.APIResponse[containerRegisterResponse]
	if err := c.Post(consts.APIPathContainersRegister, req, &resp); err != nil {
		// The backend error message already embeds register_id when
		// the failure originated from RegisterContainer. Print it
		// prominently so it survives log truncation.
		if crFlagVerbose {
			fmt.Fprintf(os.Stderr, "[verbose] POST failed: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
		fmt.Fprintln(os.Stderr, "See server logs for the register_id above; each stage emits its own structured log line.")
		return err
	}

	if crFlagVerbose {
		if b, err := json.MarshalIndent(resp, "", "  "); err == nil {
			fmt.Fprintf(os.Stderr, "[verbose] response:\n%s\n", string(b))
		}
	}

	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(resp.Data)
		return nil
	}

	d := resp.Data
	fmt.Printf("Registered %s container %q (id=%d) version %q (id=%d)\n",
		d.ContainerType, d.ContainerName, d.ContainerID, d.VersionName, d.VersionID)
	if d.ImageRef != "" {
		fmt.Printf("  image:       %s\n", d.ImageRef)
	}
	if d.HelmConfigID != 0 {
		fmt.Printf("  helm config: id=%d chart=%s version=%s\n", d.HelmConfigID, d.ChartName, d.ChartVersion)
	}
	fmt.Printf("  register_id: %s\n", d.RegisterID)
	return nil
}

func init() {
	containerRegisterCmd.Flags().BoolVar(&crFlagPedestal, "pedestal", false, "Register as a pedestal (creates container+version+helm_config)")
	containerRegisterCmd.Flags().BoolVar(&crFlagBenchmark, "benchmark", false, "Register as a benchmark (creates container+version; requires --command)")

	containerRegisterCmd.Flags().StringVar(&crFlagName, "name", "", "containers.name (must equal system short code for pedestals)")
	containerRegisterCmd.Flags().StringVar(&crFlagRegistry, "registry", "", "Image registry (e.g. docker.io)")
	containerRegisterCmd.Flags().StringVar(&crFlagRepo, "repo", "", "Image namespace/repository (e.g. opspai or opspai/clickhouse_dataset)")
	containerRegisterCmd.Flags().StringVar(&crFlagTag, "tag", "", "Image tag")
	containerRegisterCmd.Flags().StringVar(&crFlagVersion, "version", "", "container_versions.name (semver); defaults to --chart-version (pedestal) or --tag (benchmark, when semver)")

	containerRegisterCmd.Flags().StringVar(&crFlagCommand, "command", "", "Benchmark entrypoint command (required for --benchmark)")
	containerRegisterCmd.Flags().StringArrayVar(&crFlagEnv, "env", nil, "Benchmark env var KEY=VALUE (repeatable)")

	containerRegisterCmd.Flags().StringVar(&crFlagChartName, "chart-name", "", "Helm chart name (pedestal)")
	containerRegisterCmd.Flags().StringVar(&crFlagChartVersion, "chart-version", "", "Helm chart version (pedestal)")
	containerRegisterCmd.Flags().StringVar(&crFlagRepoURL, "repo-url", "", "Helm repo URL (pedestal)")
	containerRegisterCmd.Flags().StringVar(&crFlagRepoName, "repo-name", "", "Helm repo short name (pedestal)")
	containerRegisterCmd.Flags().StringVar(&crFlagValuesFile, "values-file", "", "Pre-existing server-visible values.yaml path (pedestal, optional)")

	containerRegisterCmd.Flags().BoolVar(&crFlagVerbose, "verbose", false, "Print each HTTP request/response to stderr")

	containerCmd.AddCommand(containerRegisterCmd)
}
