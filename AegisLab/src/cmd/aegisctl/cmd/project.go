package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"
	"aegis/consts"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Local structs for project API responses.

type projectDetail struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type projectListItem struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

func newClient() *client.Client {
	return client.NewClient(flagServer, flagToken, time.Duration(flagRequestTimeout)*time.Second)
}

func newResolver() *client.Resolver {
	return client.NewResolver(newClient())
}

var projectCmd = &cobra.Command{
	Use:     "project",
	Aliases: []string{"proj"},
	Short:   "Manage projects",
}

// --- project list ---

var projectListPage int
var projectListSize int

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		path := fmt.Sprintf("%s?page=%d&size=%d", consts.APIPathProjects, projectListPage, projectListSize)

		var resp client.APIResponse[client.PaginatedData[projectListItem]]
		if err := c.Get(path, &resp); err != nil {
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
		for _, p := range resp.Data.Items {
			rows = append(rows, []string{p.Name, p.Description, p.Status, p.CreatedAt})
		}
		output.PrintTable([]string{"Name", "Description", "Status", "Created"}, rows)
		return nil
	},
}

// --- project get ---

var projectGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get project details by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		r := client.NewResolver(c)

		id, err := r.ProjectID(args[0])
		if err != nil {
			return err
		}

		var resp client.APIResponse[projectDetail]
		if err := c.Get(consts.APIPathProject(id), &resp); err != nil {
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

// --- project create ---

var projectCreateName string
var projectCreateDesc string

var projectCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new project",
	RunE: func(cmd *cobra.Command, args []string) error {
		if projectCreateName == "" {
			return fmt.Errorf("--name is required")
		}

		c := newClient()

		body := map[string]string{
			"name":        projectCreateName,
			"description": projectCreateDesc,
		}

		var resp client.APIResponse[projectDetail]
		if err := c.Post(consts.APIPathProjects, body, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		output.PrintInfo(fmt.Sprintf("Project %q created (id: %d)", resp.Data.Name, resp.Data.ID))
		return nil
	},
}

// --- project update ---

var (
	projectUpdateDesc   string
	projectUpdateStatus string
	projectUpdatePublic string // tri-state: "", "true", "false"
)

var projectUpdateCmd = &cobra.Command{
	Use:   "update <name-or-id>",
	Short: "Update a project (description / status / is-public)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		descSet := cmd.Flags().Changed("description")
		statusSet := cmd.Flags().Changed("status")
		publicSet := cmd.Flags().Changed("is-public")
		if !descSet && !statusSet && !publicSet {
			return usageErrorf("at least one of --description, --status, or --is-public is required")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}

		c := newClient()
		r := client.NewResolver(c)
		id, name, err := r.ProjectIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("project %q not found: %v", args[0], err)
		}

		body := map[string]any{}
		if descSet {
			body["description"] = projectUpdateDesc
		}
		if statusSet {
			body["status"] = projectUpdateStatus
		}
		if publicSet {
			switch strings.ToLower(projectUpdatePublic) {
			case "true", "1", "yes":
				body["is_public"] = true
			case "false", "0", "no":
				body["is_public"] = false
			default:
				return usageErrorf("--is-public must be true or false")
			}
		}

		if flagDryRun {
			var curResp client.APIResponse[projectDetail]
			_ = c.Get(consts.APIPathProject(id), &curResp)

			planned, _ := json.MarshalIndent(body, "", "  ")
			fmt.Fprintf(os.Stderr, "Dry run — PATCH /api/v2/projects/%d\n%s\n", id, string(planned))

			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run":  true,
					"id":       id,
					"name":     name,
					"current":  curResp.Data,
					"proposed": body,
				})
			} else {
				fmt.Printf("Project: %s (id %d)\n", name, id)
				if descSet {
					fmt.Printf("  description: %q -> %q\n", curResp.Data.Description, projectUpdateDesc)
				}
				if statusSet {
					fmt.Printf("  status:      %q -> %q\n", curResp.Data.Status, projectUpdateStatus)
				}
				if publicSet {
					fmt.Printf("  is_public:   -> %v\n", body["is_public"])
				}
			}
			return nil
		}

		var resp client.APIResponse[projectDetail]
		if err := c.Patch(consts.APIPathProject(id), body, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Project %q (id %d) updated", name, id))
		return nil
	},
}

// --- project delete ---

var projectDeleteYes bool

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <name-or-id>",
	Short: "Delete a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		r := client.NewResolver(c)
		id, name, err := r.ProjectIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("project %q not found: %v", args[0], err)
		}

		if flagDryRun {
			fmt.Fprintf(os.Stderr, "Dry run — would DELETE /api/v2/projects/%d (%s)\n", id, name)
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "id": id, "name": name})
			} else {
				fmt.Printf("Would delete project %s (id %d)\n", name, id)
			}
			return nil
		}

		if err := confirmDeletion("project", name, id, projectDeleteYes); err != nil {
			return err
		}

		var resp client.APIResponse[any]
		if err := c.Delete(consts.APIPathProject(id), &resp); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("Project %q (id %d) deleted", name, id))
		return nil
	},
}

// --- project resolve ---

var projectResolveCmd = &cobra.Command{
	Use:   "resolve <name-or-id>",
	Short: "Resolve a project reference to both its ID and name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		r := client.NewResolver(c)
		id, name, err := r.ProjectIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("project %q not found", args[0])
		}
		printResolvedIDName(id, name)
		return nil
	},
}

// printResolvedIDName writes the resolved {id, name} pair using the current
// output format (JSON or table). Shared by project/container/dataset resolve.
func printResolvedIDName(id int, name string) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{"id": id, "name": name})
		return
	}
	output.PrintTable([]string{"ID", "NAME"}, [][]string{{fmt.Sprintf("%d", id), name}})
}

// confirmDeletion enforces the shared TTY/--yes gate used by
// project/container/dataset delete commands.
func confirmDeletion(resource, name string, id int, yes bool) error {
	if yes {
		return nil
	}
	if flagNonInteractive {
		return usageErrorf("refusing to delete without --yes in non-interactive mode")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usageErrorf("refusing to delete without --yes when stdin is not a TTY")
	}
	fmt.Fprintf(os.Stderr, "Delete %s %s (id %d)? [y/N] ", resource, name, id)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		return usageErrorf("aborted by user")
	}
	return nil
}

func init() {
	projectListCmd.Flags().IntVar(&projectListPage, "page", 1, "Page number")
	projectListCmd.Flags().IntVar(&projectListSize, "size", 20, "Page size")

	projectCreateCmd.Flags().StringVar(&projectCreateName, "name", "", "Project name (required)")
	projectCreateCmd.Flags().StringVar(&projectCreateDesc, "description", "", "Project description")

	projectUpdateCmd.Flags().StringVar(&projectUpdateDesc, "description", "", "New description")
	projectUpdateCmd.Flags().StringVar(&projectUpdateStatus, "status", "", "New status (e.g. active, archived)")
	projectUpdateCmd.Flags().StringVar(&projectUpdatePublic, "is-public", "", "Set project visibility: true|false")

	projectDeleteCmd.Flags().BoolVar(&projectDeleteYes, "yes", false, "Skip confirmation prompt")
	projectDeleteCmd.Flags().BoolVar(&projectDeleteYes, "force", false, "Alias for --yes")

	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectGetCmd)
	projectCmd.AddCommand(projectCreateCmd)
	projectCmd.AddCommand(projectUpdateCmd)
	projectCmd.AddCommand(projectDeleteCmd)
	projectCmd.AddCommand(projectResolveCmd)

	// Defer dry-run marking until after root.go's init() attaches projectCmd
	// to rootCmd, so CommandPath() resolves to the full "aegisctl ..." path.
	cobra.OnInitialize(func() {
		markDryRunSupported(projectUpdateCmd)
		markDryRunSupported(projectDeleteCmd)
	})
}
