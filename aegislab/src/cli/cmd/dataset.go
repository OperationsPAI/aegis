package cmd

import (
	"fmt"
	"os"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var datasetCmd = &cobra.Command{
	Use:     "dataset",
	Aliases: []string{"ds"},
	Short:   "Manage datasets",
}

// --- dataset list ---

var datasetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List datasets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		resp, _, err := cli.DatasetsAPI.ListDatasets(ctx).Page(1).Size(100).Execute()
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

		var items []apiclient.DatasetDatasetResp
		if resp.Data != nil {
			items = resp.Data.GetItems()
		}
		rows := make([][]string, 0, len(items))
		for _, d := range items {
			// DatasetDatasetResp has no Description field on the list payload.
			rows = append(rows, []string{d.GetName(), "", d.GetStatus(), d.GetCreatedAt()})
		}
		output.PrintTable([]string{"Name", "Description", "Status", "Created"}, rows)
		return nil
	},
}

// --- dataset get ---

var datasetGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get dataset details by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		r := client.NewResolver(c)

		id, err := r.DatasetID(args[0])
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.DatasetsAPI.GetDatasetById(ctx, int32(id)).Execute()
		if err != nil {
			return err
		}
		d := resp.Data

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(d)
			return nil
		}
		if d == nil {
			return fmt.Errorf("empty dataset response")
		}

		fmt.Printf("Name:        %s\n", d.GetName())
		fmt.Printf("ID:          %d\n", d.GetId())
		fmt.Printf("Description: %s\n", d.GetDescription())
		fmt.Printf("Status:      %s\n", d.GetStatus())
		fmt.Printf("Created:     %s\n", d.GetCreatedAt())
		fmt.Printf("Updated:     %s\n", d.GetUpdatedAt())
		return nil
	},
}

// --- dataset versions ---

var datasetVersionsCmd = &cobra.Command{
	Use:   "versions <name>",
	Short: "List versions for a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		r := client.NewResolver(c)

		id, err := r.DatasetID(args[0])
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.DatasetsAPI.ListDatasetVersions(ctx, int32(id)).Execute()
		if err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		var items []apiclient.DatasetDatasetVersionResp
		if resp.Data != nil {
			items = resp.Data.GetItems()
		}
		rows := make([][]string, 0, len(items))
		for _, v := range items {
			// DatasetDatasetVersionResp exposes Name (version string) and UpdatedAt
			// only; no Status/CreatedAt in the generated DTO.
			rows = append(rows, []string{v.GetName(), "", v.GetUpdatedAt()})
		}
		output.PrintTable([]string{"Version", "Status", "Created"}, rows)
		return nil
	},
}

// --- dataset delete ---

var datasetDeleteYes bool

var datasetDeleteCmd = &cobra.Command{
	Use:   "delete <name-or-id>",
	Short: "Delete a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		r := client.NewResolver(c)
		id, name, err := r.DatasetIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("dataset %q not found: %v", args[0], err)
		}

		if flagDryRun {
			fmt.Fprintf(os.Stderr, "Dry run — would DELETE /api/v2/datasets/%d (%s)\n", id, name)
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "id": id, "name": name})
			} else {
				fmt.Printf("Would delete dataset %s (id %d)\n", name, id)
			}
			return nil
		}
		if err := confirmDeletion("dataset", name, id, datasetDeleteYes); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		if _, _, err := cli.DatasetsAPI.DeleteDataset(ctx, int32(id)).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("Dataset %q (id %d) deleted", name, id))
		return nil
	},
}

// --- dataset resolve ---

var datasetResolveCmd = &cobra.Command{
	Use:   "resolve <name-or-id>",
	Short: "Resolve a dataset reference to both its ID and name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		r := client.NewResolver(c)
		id, name, err := r.DatasetIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("dataset %q not found", args[0])
		}
		printResolvedIDName(id, name)
		return nil
	},
}

func init() {
	datasetDeleteCmd.Flags().BoolVar(&datasetDeleteYes, "yes", false, "Skip confirmation prompt")
	datasetDeleteCmd.Flags().BoolVar(&datasetDeleteYes, "force", false, "Alias for --yes")

	datasetCmd.AddCommand(datasetListCmd)
	datasetCmd.AddCommand(datasetGetCmd)
	datasetCmd.AddCommand(datasetVersionsCmd)
	datasetCmd.AddCommand(datasetDeleteCmd)
	datasetCmd.AddCommand(datasetResolveCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(datasetDeleteCmd)
	})
}
