package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"

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

// stringFromAP fishes a string field out of a generated DTO's
// AdditionalProperties map. Used because some backend response fields
// (e.g. project.description) aren't surfaced in the swag-annotated
// response DTO and only land in AdditionalProperties at decode time.
func stringFromAP(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
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
		cli, ctx := newAPIClient()
		resp, _, err := cli.ProjectsAPI.ListProjects(ctx).
			Page(int32(projectListPage)).
			Size(int32(projectListSize)).
			Execute()
		if err != nil {
			return err
		}
		data := resp.GetData()
		raw := data.GetItems()
		items := make([]projectListItem, 0, len(raw))
		for _, p := range raw {
			items = append(items, projectListItem{
				ID:          int(p.GetId()),
				Name:        p.GetName(),
				Description: stringFromAP(p.AdditionalProperties, "description"),
				Status:      p.GetStatus(),
				CreatedAt:   p.GetCreatedAt(),
			})
		}

		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(map[string]any{"items": items, "pagination": data.GetPagination()})
			return nil
		case output.FormatNDJSON:
			if err := output.PrintMetaJSON(data.GetPagination()); err != nil {
				return err
			}
			return output.PrintNDJSON(items)
		}

		rows := make([][]string, 0, len(items))
		for _, p := range items {
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
		r := newResolver()
		id, err := r.ProjectID(args[0])
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.ProjectsAPI.GetProjectById(ctx, int32(id)).Execute()
		if err != nil {
			return err
		}
		d := resp.GetData()
		detail := projectDetail{
			ID:          int(d.GetId()),
			Name:        d.GetName(),
			Description: stringFromAP(d.AdditionalProperties, "description"),
			Status:      d.GetStatus(),
			CreatedAt:   d.GetCreatedAt(),
			UpdatedAt:   d.GetUpdatedAt(),
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(detail)
			return nil
		}

		fmt.Printf("Name:        %s\n", detail.Name)
		fmt.Printf("ID:          %d\n", detail.ID)
		fmt.Printf("Description: %s\n", detail.Description)
		fmt.Printf("Status:      %s\n", detail.Status)
		fmt.Printf("Created:     %s\n", detail.CreatedAt)
		fmt.Printf("Updated:     %s\n", detail.UpdatedAt)
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

		req := apiclient.ProjectCreateProjectReq{Name: projectCreateName}
		if projectCreateDesc != "" {
			req.SetDescription(projectCreateDesc)
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.ProjectsAPI.CreateProject(ctx).ProjectCreateProjectReq(req).Execute()
		if err != nil {
			return err
		}
		d := resp.GetData()
		detail := projectDetail{
			ID:          int(d.GetId()),
			Name:        d.GetName(),
			Description: stringFromAP(d.AdditionalProperties, "description"),
			Status:      d.GetStatus(),
			CreatedAt:   d.GetCreatedAt(),
			UpdatedAt:   d.GetUpdatedAt(),
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(detail)
			return nil
		}

		output.PrintInfo(fmt.Sprintf("Project %q created (id: %d)", detail.Name, detail.ID))
		return nil
	},
}

// --- project update ---

var (
	projectUpdateDesc   string
	projectUpdateStatus string
	projectUpdatePublic string // tri-state: "", "true", "false"
)

// statusNameToConst maps the human-readable strings the CLI accepts
// (active/archived for back-compat, and the backend's own enabled/disabled)
// to the int enum value the typed UpdateProject request requires.
func statusNameToConst(s string) (apiclient.ConstsStatusType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "enabled", "1":
		return apiclient.CONSTSSTATUSTYPE_CommonEnabled, nil
	case "archived", "disabled", "0":
		return apiclient.CONSTSSTATUSTYPE_CommonDisabled, nil
	default:
		return 0, fmt.Errorf("invalid --status %q (use enabled|disabled or active|archived)", s)
	}
}

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

		r := newResolver()
		id, name, err := r.ProjectIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("project %q not found: %v", args[0], err)
		}

		req := apiclient.ProjectUpdateProjectReq{}
		if descSet {
			req.SetDescription(projectUpdateDesc)
		}
		if statusSet {
			st, err := statusNameToConst(projectUpdateStatus)
			if err != nil {
				return usageErrorf("%v", err)
			}
			req.SetStatus(st)
		}
		if publicSet {
			switch strings.ToLower(projectUpdatePublic) {
			case "true", "1", "yes":
				req.SetIsPublic(true)
			case "false", "0", "no":
				req.SetIsPublic(false)
			default:
				return usageErrorf("--is-public must be true or false")
			}
		}

		cli, ctx := newAPIClient()

		if flagDryRun {
			var current projectDetail
			if curResp, _, err := cli.ProjectsAPI.GetProjectById(ctx, int32(id)).Execute(); err == nil {
				cd := curResp.GetData()
				current = projectDetail{
					ID:          int(cd.GetId()),
					Name:        cd.GetName(),
					Description: stringFromAP(cd.AdditionalProperties, "description"),
					Status:      cd.GetStatus(),
					CreatedAt:   cd.GetCreatedAt(),
					UpdatedAt:   cd.GetUpdatedAt(),
				}
			}

			plannedMap, _ := req.ToMap()
			planned, _ := json.MarshalIndent(plannedMap, "", "  ")
			fmt.Fprintf(os.Stderr, "Dry run — PATCH /api/v2/projects/%d\n%s\n", id, string(planned))

			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run":  true,
					"id":       id,
					"name":     name,
					"current":  current,
					"proposed": plannedMap,
				})
			} else {
				fmt.Printf("Project: %s (id %d)\n", name, id)
				if descSet {
					fmt.Printf("  description: %q -> %q\n", current.Description, projectUpdateDesc)
				}
				if statusSet {
					fmt.Printf("  status:      %q -> %q\n", current.Status, projectUpdateStatus)
				}
				if publicSet {
					fmt.Printf("  is_public:   -> %v\n", plannedMap["is_public"])
				}
			}
			return nil
		}

		resp, _, err := cli.ProjectsAPI.UpdateProject(ctx, int32(id)).ProjectUpdateProjectReq(req).Execute()
		if err != nil {
			return err
		}
		d := resp.GetData()

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(projectDetail{
				ID:          int(d.GetId()),
				Name:        d.GetName(),
				Description: stringFromAP(d.AdditionalProperties, "description"),
				Status:      d.GetStatus(),
				CreatedAt:   d.GetCreatedAt(),
				UpdatedAt:   d.GetUpdatedAt(),
			})
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
		r := newResolver()
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

		cli, ctx := newAPIClient()
		if _, _, err := cli.ProjectsAPI.DeleteProject(ctx, int32(id)).Execute(); err != nil {
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
		r := newResolver()
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
