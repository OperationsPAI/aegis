package cmd

import (
	"fmt"
	"os"
	"strconv"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var datapackCmd = &cobra.Command{
	Use:   "datapack",
	Short: "Inspect datapacks (one per fault injection)",
}

type datapackFileHealth struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Rows    *int64 `json:"rows,omitempty"`
	Size    string `json:"size,omitempty"`
}

type datapackDiagnoseResp struct {
	InjectionID    int                  `json:"injection_id"`
	InjectionName  string               `json:"injection_name"`
	State          string               `json:"state"`
	Health         string               `json:"health"`
	MissingFiles   []string             `json:"missing_files,omitempty"`
	EmptyParquets  []string             `json:"empty_parquets,omitempty"`
	UnexpectedXtra []string             `json:"unexpected_extras,omitempty"`
	Parquets       []datapackFileHealth `json:"parquets"`
	JSON           []datapackFileHealth `json:"json"`
	Marker         datapackFileHealth   `json:"marker"`
	Notes          []string             `json:"notes,omitempty"`
}

type datasetVersionDetail struct {
	ID        int `json:"id"`
	Datapacks []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"datapacks"`
}

type datasetVersionsListItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

var (
	datapackDiagnoseDataset   string
	datapackDiagnoseInjection string
)

var datapackDiagnoseCmd = &cobra.Command{
	Use:   "diagnose",
	Short: "Report datapack health (missing files, zero-row parquets, state inconsistencies)",
	Long: `Surfaces broken or half-built datapacks. Read-only.

Pass exactly one of --dataset or --injection. With --dataset the command
enumerates datapacks in the dataset's latest version and reports each.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if (datapackDiagnoseDataset == "") == (datapackDiagnoseInjection == "") {
			return fmt.Errorf("exactly one of --dataset or --injection is required")
		}
		c := newClient()

		var reports []datapackDiagnoseResp
		if datapackDiagnoseInjection != "" {
			id, err := strconv.Atoi(datapackDiagnoseInjection)
			if err != nil || id <= 0 {
				return fmt.Errorf("--injection must be a positive integer id, got %q", datapackDiagnoseInjection)
			}
			rep, err := fetchDatapackDiagnose(c, id)
			if err != nil {
				return err
			}
			reports = append(reports, *rep)
		} else {
			ids, err := resolveDatasetDatapackIDs(c, datapackDiagnoseDataset)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				fmt.Fprintf(os.Stderr, "Dataset %q has no datapacks in its latest version\n", datapackDiagnoseDataset)
				return nil
			}
			for _, id := range ids {
				rep, err := fetchDatapackDiagnose(c, id)
				if err != nil {
					fmt.Fprintf(os.Stderr, "diagnose injection %d failed: %v\n", id, err)
					continue
				}
				reports = append(reports, *rep)
			}
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(reports)
			return nil
		}
		printDatapackDiagnoseTable(reports)
		return nil
	},
}

func fetchDatapackDiagnose(c *client.Client, injectionID int) (*datapackDiagnoseResp, error) {
	var resp client.APIResponse[datapackDiagnoseResp]
	path := fmt.Sprintf("/api/v2/injections/%d/diagnose", injectionID)
	if err := c.Get(path, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func resolveDatasetDatapackIDs(c *client.Client, datasetName string) ([]int, error) {
	r := client.NewResolver(c)
	datasetID, err := r.DatasetID(datasetName)
	if err != nil {
		return nil, fmt.Errorf("resolve dataset %q: %w", datasetName, err)
	}

	var versionsResp client.APIResponse[client.PaginatedData[datasetVersionsListItem]]
	if err := c.Get(fmt.Sprintf("/api/v2/datasets/%d/versions", datasetID), &versionsResp); err != nil {
		return nil, fmt.Errorf("list versions for dataset %d: %w", datasetID, err)
	}
	versions := versionsResp.Data.Items
	if len(versions) == 0 {
		return nil, fmt.Errorf("dataset %q has no versions", datasetName)
	}
	latest := versions[len(versions)-1]

	var detailResp client.APIResponse[datasetVersionDetail]
	if err := c.Get(fmt.Sprintf("/api/v2/datasets/%d/version/%d", datasetID, latest.ID), &detailResp); err != nil {
		return nil, fmt.Errorf("get version %d detail: %w", latest.ID, err)
	}
	ids := make([]int, 0, len(detailResp.Data.Datapacks))
	for _, d := range detailResp.Data.Datapacks {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func printDatapackDiagnoseTable(reports []datapackDiagnoseResp) {
	headers := []string{"InjectionID", "Name", "State", "Health", "Missing", "EmptyParquets"}
	rows := make([][]string, 0, len(reports))
	for _, r := range reports {
		rows = append(rows, []string{
			strconv.Itoa(r.InjectionID),
			r.InjectionName,
			r.State,
			r.Health,
			joinOrDash(r.MissingFiles),
			joinOrDash(r.EmptyParquets),
		})
	}
	output.PrintTable(headers, rows)
}

func joinOrDash(in []string) string {
	if len(in) == 0 {
		return "-"
	}
	out := in[0]
	for _, s := range in[1:] {
		out += "," + s
	}
	return out
}

func init() {
	datapackDiagnoseCmd.Flags().StringVar(&datapackDiagnoseDataset, "dataset", "", "Dataset name; diagnoses every datapack in its latest version")
	datapackDiagnoseCmd.Flags().StringVar(&datapackDiagnoseInjection, "injection", "", "Injection id; diagnoses a single datapack")
	datapackCmd.AddCommand(datapackDiagnoseCmd)
	rootCmd.AddCommand(datapackCmd)
}
