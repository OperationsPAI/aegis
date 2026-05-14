package cmd

import (
	"fmt"
	"os"
	"strconv"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ---------- spec types ----------

// ExecuteSpec is the YAML structure for algorithm execution submission.
type ExecuteSpec struct {
	Specs  []ExecutionSpecItem `yaml:"specs"            json:"specs"`
	Labels []LabelItem         `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// ExecutionSpecItem describes a single algorithm execution.
type ExecutionSpecItem struct {
	Algorithm ContainerRef `yaml:"algorithm"            json:"algorithm"`
	Datapack  *string      `yaml:"datapack,omitempty"   json:"datapack,omitempty"`
	Dataset   *DatasetRef  `yaml:"dataset,omitempty"    json:"dataset,omitempty"`
}

// DatasetRef references a dataset by name and version.
type DatasetRef struct {
	Name    string `yaml:"name"    json:"name"`
	Version string `yaml:"version" json:"version"`
}

// ---------- execute root ----------

var executeCmd = &cobra.Command{
	Use:   "execute",
	Short: "Manage algorithm executions",
	Long: `Manage RCA algorithm executions in aegislab projects.

WORKFLOW:
  # Create algorithm execution from a YAML spec file
  aegisctl execute create --input execution.yaml --project pair_diagnosis

  # List executions in a project
  aegisctl execute list --project pair_diagnosis

  # Get execution details by ID
  aegisctl execute get <execution-id>

SPEC FILE FORMAT (execution.yaml):
  specs:
    - algorithm:
        name: random
        version: "1.0.0"
      datapack: "<injection-name>"     # reference an injection's datapack
    - algorithm:
        name: traceback
        version: "1.0.0"
      dataset:                          # or reference a dataset version
        name: rca_pair_diagnosis_dataset
        version: "1.0.0"
  labels:
    - key: experiment
      value: algorithm-comparison`,
}

// ---------- execute create ----------

var executeCreateInput string

var executeCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Submit an algorithm execution from a YAML spec",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runExecuteCreate(cmd, args)
	},
}

func runExecuteCreate(cmd *cobra.Command, args []string) error {
	specPath, err := resolveExecuteSpecPath(cmd)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read spec file: %w", err)
	}

	var spec ExecuteSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("parse spec YAML: %w", err)
	}

	pid, err := resolveProjectIDByName()
	if err != nil {
		return err
	}

	path := consts.APIPathProjectExecutionsExecute(pid)
	if flagDryRun {
		plan := map[string]any{
			"dry_run":    true,
			"operation":  "execute_submit",
			"project":    flagProject,
			"project_id": pid,
			"method":     "POST",
			"path":       path,
			"spec":       spec,
		}
		output.PrintJSON(plan)
		return nil
	}

	// TODO: missing swag annotation — generated RunAlgorithm wants typed
	// ExecutionSubmitExecutionReq with algorithm_id / dataset_id ints, but
	// the CLI submits free-form name/version refs. Keep manual until the
	// backend handler exposes the same name/version shape via SDK.
	c := newClient()
	var resp client.APIResponse[any]
	if err := c.Post(path, spec, &resp); err != nil {
		return err
	}

	output.PrintJSON(resp.Data)
	return nil
}

// ---------- execute list ----------

var (
	executeListPage int
	executeListSize int
)

var executeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List algorithm executions in a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := resolveProjectIDByName()
		if err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.ProjectsAPI.ListProjectExecutions(ctx, int32(pid)).
			Page(int32(executeListPage)).
			Size(int32(executeListSize)).
			Execute()
		if err != nil {
			return err
		}

		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(resp.Data)
			return nil
		case output.FormatNDJSON:
			if resp.Data != nil {
				if err := output.PrintMetaJSON(resp.Data.GetPagination()); err != nil {
					return err
				}
				return output.PrintNDJSON(resp.Data.GetItems())
			}
			return nil
		}

		var items []apiclient.ExecutionExecutionResp
		if resp.Data != nil {
			items = resp.Data.GetItems()
		}
		headers := []string{"ID", "ALGORITHM", "DATAPACK", "STATE", "DURATION", "CREATED"}
		var rows [][]string
		for _, item := range items {
			rows = append(rows, []string{
				strconv.FormatInt(int64(item.GetId()), 10),
				item.GetAlgorithmName(),
				item.GetDatapackName(),
				item.GetState(),
				fmt.Sprintf("%v", item.GetDuration()),
				item.GetCreatedAt(),
			})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

// ---------- execute get ----------

var executeGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get detailed info about an execution",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		idInt, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("execution id must be numeric: %w", err)
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.ExecutionsAPI.GetExecutionById(ctx, int32(idInt)).Execute()
		if err != nil {
			return err
		}
		output.PrintJSON(resp.Data)
		return nil
	},
}

// ---------- init ----------

func init() {
	executeListCmd.Flags().IntVar(&executeListPage, "page", 1, "Page number")
	executeListCmd.Flags().IntVar(&executeListSize, "size", 20, "Page size")

	executeCmd.AddCommand(executeCreateCmd)
	executeCmd.AddCommand(executeListCmd)
	executeCmd.AddCommand(executeGetCmd)

	executeCreateCmd.Flags().StringVarP(&executeCreateInput, "input", "f", "", "Path to execution spec YAML file (required)")
}

func resolveExecuteSpecPath(cmd *cobra.Command) (string, error) {
	if executeCreateInput == "" {
		return "", usageErrorf("--input is required")
	}
	return executeCreateInput, nil
}
