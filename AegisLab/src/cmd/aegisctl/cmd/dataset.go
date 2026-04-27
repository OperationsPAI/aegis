package cmd

import (
	"fmt"
	"os"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

// Local structs for dataset API responses.

type datasetDetail struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type datasetListItem struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type datasetVersionItem struct {
	ID        int    `json:"id"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

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
		c := newClient()

		var resp client.APIResponse[client.PaginatedData[datasetListItem]]
		if err := c.Get("/api/v2/datasets?page=1&size=100", &resp); err != nil {
			return err
		}

		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(resp.Data)
			return nil
		case output.FormatNDJSON:
			if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
				return err
			}
			return output.PrintNDJSON(resp.Data.Items)
		}

		rows := make([][]string, 0, len(resp.Data.Items))
		for _, d := range resp.Data.Items {
			rows = append(rows, []string{d.Name, d.Description, d.Status, d.CreatedAt})
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

		var resp client.APIResponse[datasetDetail]
		if err := c.Get(fmt.Sprintf("/api/v2/datasets/%d", id), &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		fmt.Printf("Name:        %s\n", resp.Data.Name)
		fmt.Printf("ID:          %d\n", resp.Data.ID)
		fmt.Printf("Description: %s\n", resp.Data.Description)
		fmt.Printf("Status:      %s\n", resp.Data.Status)
		fmt.Printf("Created:     %s\n", resp.Data.CreatedAt)
		fmt.Printf("Updated:     %s\n", resp.Data.UpdatedAt)
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

		var resp client.APIResponse[client.PaginatedData[datasetVersionItem]]
		if err := c.Get(fmt.Sprintf("/api/v2/datasets/%d/versions", id), &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		rows := make([][]string, 0, len(resp.Data.Items))
		for _, v := range resp.Data.Items {
			rows = append(rows, []string{v.Version, v.Status, v.CreatedAt})
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
		var resp client.APIResponse[any]
		if err := c.Delete(fmt.Sprintf("/api/v2/datasets/%d", id), &resp); err != nil {
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
