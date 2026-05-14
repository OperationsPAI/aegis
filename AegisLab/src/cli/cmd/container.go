package cmd

import (
	"fmt"
	"os"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/output"
	"aegis/platform/consts"

	"github.com/spf13/cobra"
)

// Local structs for container API responses. Kept (rather than pulled from
// apiclient directly) because container_test.go and system_publish_chart.go
// consume these shapes, and because containerGetOutput embeds containerDetail
// to add CLI-only fields (default_version, version_count) that aren't in the
// API DTO.

type containerDetail struct {
	ID        int                    `json:"id"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Status    string                 `json:"status"`
	Versions  []containerVersionItem `json:"versions"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
}

type containerGetOutput struct {
	containerDetail
	DefaultVersion string `json:"default_version"`
	VersionCount   int    `json:"version_count"`
}

type containerListItem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type containerVersionItem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ImageRef  string `json:"image_ref"`
	Usage     int    `json:"usage"`
	UpdatedAt string `json:"updated_at"`
}

var containerCmd = &cobra.Command{
	Use:     "container",
	Aliases: []string{"ctr"},
	Short:   "Manage containers",
}

// --- container list ---

var containerListType string

// containerTypeNameToInt converts a human-readable container type to its API integer.
var containerTypeNameToInt = map[string]int32{
	"algorithm": 0,
	"benchmark": 1,
	"pedestal":  2,
}

var containerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		req := cli.ContainersAPI.ListContainers(ctx).Page(1).Size(100)
		if containerListType != "" {
			typeInt, ok := containerTypeNameToInt[containerListType]
			if !ok {
				return fmt.Errorf("invalid container type %q (valid: algorithm, benchmark, pedestal)", containerListType)
			}
			req = req.Type_(typeInt)
		}
		resp, _, err := req.Execute()
		if err != nil {
			return err
		}
		data := resp.GetData()
		raw := data.GetItems()
		items := make([]containerListItem, 0, len(raw))
		for _, c := range raw {
			items = append(items, containerListItem{
				ID:        int(c.GetId()),
				Name:      c.GetName(),
				Type:      c.GetType(),
				Status:    c.GetStatus(),
				CreatedAt: c.GetCreatedAt(),
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
		for _, item := range items {
			rows = append(rows, []string{item.Name, item.Type, item.Status, item.CreatedAt})
		}
		output.PrintTable([]string{"Name", "Type", "Status", "Created"}, rows)
		return nil
	},
}

// --- container get ---

var containerGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get container details by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := newResolver()
		id, err := r.ContainerID(args[0])
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.ContainersAPI.GetContainerById(ctx, int32(id)).Execute()
		if err != nil {
			return err
		}
		d := resp.GetData()
		detail := containerDetail{
			ID:        int(d.GetId()),
			Name:      d.GetName(),
			Type:      d.GetType(),
			Status:    d.GetStatus(),
			Versions:  apiVersionsToLocal(d.GetVersions()),
			CreatedAt: d.GetCreatedAt(),
			UpdatedAt: d.GetUpdatedAt(),
		}

		versionCount := len(detail.Versions)
		defaultVersion := "(none)"
		if versionCount > 0 {
			defaultVersion = detail.Versions[0].Name
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			out := containerGetOutput{
				containerDetail: detail,
				DefaultVersion:  defaultVersion,
				VersionCount:    versionCount,
			}
			output.PrintJSON(out)
			return nil
		}

		fmt.Printf("Name:     %s\n", detail.Name)
		fmt.Printf("ID:       %d\n", detail.ID)
		fmt.Printf("Type:     %s\n", detail.Type)
		fmt.Printf("Status:   %s\n", detail.Status)
		fmt.Printf("Versions: %d\n", versionCount)
		fmt.Printf("Default:  %s\n", defaultVersion)
		fmt.Printf("Created:  %s\n", detail.CreatedAt)
		fmt.Printf("Updated:  %s\n", detail.UpdatedAt)
		return nil
	},
}

// apiVersionsToLocal copies a generated []ContainerContainerVersionResp into
// the local []containerVersionItem shape consumed by tests and the JSON/table
// renderers.
func apiVersionsToLocal(versions []apiclient.ContainerContainerVersionResp) []containerVersionItem {
	if versions == nil {
		return nil
	}
	out := make([]containerVersionItem, 0, len(versions))
	for _, v := range versions {
		out = append(out, containerVersionItem{
			ID:        int(v.GetId()),
			Name:      v.GetName(),
			ImageRef:  v.GetImageRef(),
			Usage:     int(v.GetUsage()),
			UpdatedAt: v.GetUpdatedAt(),
		})
	}
	return out
}

// --- container versions ---

var containerVersionsCmd = &cobra.Command{
	Use:   "versions <name>",
	Short: "List versions for a container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := newResolver()
		id, err := r.ContainerID(args[0])
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.ContainersAPI.ListContainerVersions(ctx, int32(id)).Execute()
		if err != nil {
			return err
		}
		data := resp.GetData()
		items := apiVersionsToLocal(data.GetItems())

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{"items": items, "pagination": data.GetPagination()})
			return nil
		}

		rows := make([][]string, 0, len(items))
		for _, v := range items {
			rows = append(rows, []string{v.Name, v.ImageRef, v.ImageRef, fmt.Sprintf("%d", v.Usage), v.UpdatedAt})
		}
		// The new IMAGE column mirrors the server-composed image_ref
		// (registry/namespace/repository:tag). Image is kept for backward
		// compatibility with existing agent scripts.
		output.PrintTable([]string{"Version", "Image", "IMAGE", "Usage", "Updated"}, rows)
		return nil
	},
}

// --- container version (subcommands) ---

var containerVersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Manage container versions",
}

// set-image

var (
	setImageID     int
	setImageRef    string
	setImageDryRun bool
)

var containerVersionSetImageCmd = &cobra.Command{
	Use:   "set-image",
	Short: "Rewrite the image reference of a container version in the database",
	Long: `Parse <ref> into (registry, namespace, repository, tag) and atomically
update the matching columns on the container_versions row identified by --id.

Tag is required. A reference without ":<tag>" is rejected. Digest refs
(@sha256:...) are rejected. References without a registry default to docker.io.
Nested namespaces are preserved (e.g. docker.io/foo/bar/baz:tag -> namespace
"foo/bar", repository "baz").`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if setImageID <= 0 {
			return fmt.Errorf("--id is required and must be positive")
		}
		if strings.TrimSpace(setImageRef) == "" {
			return fmt.Errorf("--ref is required")
		}
		parsed, err := parseImageRef(setImageRef)
		if err != nil {
			return err
		}

		cli, ctx := newAPIClient()

		current, err := fetchContainerVersionByID(setImageID)
		if err != nil {
			return fmt.Errorf("failed to fetch current container version: %w", err)
		}

		if setImageDryRun {
			printSetImageDiff(current, parsed)
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run":  true,
					"id":       setImageID,
					"current":  current,
					"proposed": parsed,
				})
			}
			return nil
		}

		req := apiclient.ContainerSetContainerVersionImageReq{
			Repository: parsed.Repository,
			Tag:        parsed.Tag,
		}
		if parsed.Registry != "" {
			req.SetRegistry(parsed.Registry)
		}
		if parsed.Namespace != "" {
			req.SetNamespace(parsed.Namespace)
		}
		resp, _, err := cli.ContainersAPI.SetContainerVersionImage(ctx, int32(setImageID)).
			ContainerSetContainerVersionImageReq(req).
			Execute()
		if err != nil {
			return err
		}
		d := resp.GetData()

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(d)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Container version %d image updated to %s", setImageID, parsed.String()))
		return nil
	},
}

// list-versions

var containerVersionListVersionsName string

var containerVersionListVersionsCmd = &cobra.Command{
	Use:   "list-versions <container-name>",
	Short: "List versions for a container (with IMAGE column)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := containerVersionListVersionsName
		if len(args) == 1 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("container name is required (positional arg or --name)")
		}
		return runListVersions(name)
	},
}

// fetchContainerVersionByID searches known containers for the given version id.
// Returns the version item and its parent container name.
func fetchContainerVersionByID(versionID int) (*containerVersionItem, error) {
	c, ctx := newAPIClient()
	listResp, _, err := c.ContainersAPI.ListContainers(ctx).Page(1).Size(1000).Execute()
	if err != nil {
		return nil, err
	}
	r := newResolver()
	listData := listResp.GetData()
	for _, ctr := range listData.GetItems() {
		id, err := r.ContainerID(ctr.GetName())
		if err != nil {
			continue
		}
		vResp, _, err := c.ContainersAPI.ListContainerVersions(ctx, int32(id)).Page(1).Size(1000).Execute()
		if err != nil {
			continue
		}
		vData := vResp.GetData()
		for _, v := range apiVersionsToLocal(vData.GetItems()) {
			if v.ID == versionID {
				return &v, nil
			}
		}
	}
	return nil, fmt.Errorf("container version %d not found", versionID)
}

func printSetImageDiff(current *containerVersionItem, proposed imageRefParts) {
	currentRef := "(unknown)"
	if current != nil {
		currentRef = current.ImageRef
	}
	fmt.Fprintln(os.Stdout, "Dry run — no changes written.")
	fmt.Fprintf(os.Stdout, "  id:       %d\n", setImageID)
	fmt.Fprintf(os.Stdout, "  current:  %s\n", currentRef)
	fmt.Fprintf(os.Stdout, "  proposed: %s\n", proposed.String())
	fmt.Fprintf(os.Stdout, "            registry=%s namespace=%s repository=%s tag=%s\n",
		proposed.Registry, proposed.Namespace, proposed.Repository, proposed.Tag)
}

func runListVersions(containerName string) error {
	r := newResolver()
	id, err := r.ContainerID(containerName)
	if err != nil {
		return err
	}

	cli, ctx := newAPIClient()
	resp, _, err := cli.ContainersAPI.ListContainerVersions(ctx, int32(id)).Execute()
	if err != nil {
		return err
	}
	data := resp.GetData()
	items := apiVersionsToLocal(data.GetItems())

	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{"items": items, "pagination": data.GetPagination()})
		return nil
	}

	rows := make([][]string, 0, len(items))
	for _, v := range items {
		rows = append(rows, []string{
			fmt.Sprintf("%d", v.ID),
			v.Name,
			v.ImageRef,
			fmt.Sprintf("%d", v.Usage),
			v.UpdatedAt,
		})
	}
	// IMAGE column (registry/namespace/repository:tag) is sourced from the
	// server's image_ref field, which AfterFind composes from the four columns.
	output.PrintTable([]string{"ID", "Version", "IMAGE", "Usage", "Updated"}, rows)
	return nil
}

// --- container delete ---

var containerDeleteYes bool

var containerDeleteCmd = &cobra.Command{
	Use:   "delete <name-or-id>",
	Short: "Delete a container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		r := newResolver()
		id, name, err := r.ContainerIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("container %q not found: %v", args[0], err)
		}

		if flagDryRun {
			fmt.Fprintf(os.Stderr, "Dry run — would DELETE /api/v2/containers/%d (%s)\n", id, name)
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "id": id, "name": name})
			} else {
				fmt.Printf("Would delete container %s (id %d)\n", name, id)
			}
			return nil
		}
		if err := confirmDeletion("container", name, id, containerDeleteYes); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		if _, _, err := cli.ContainersAPI.DeleteContainer(ctx, int32(id)).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("Container %q (id %d) deleted", name, id))
		return nil
	},
}

// --- container resolve ---

var containerResolveCmd = &cobra.Command{
	Use:   "resolve <name-or-id>",
	Short: "Resolve a container reference to both its ID and name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		r := newResolver()
		id, name, err := r.ContainerIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("container %q not found", args[0])
		}
		printResolvedIDName(id, name)
		return nil
	},
}

// --- container build ---

var (
	containerBuildVersion string
	containerBuildForce   bool
)

var containerBuildCmd = &cobra.Command{
	Use:   "build <name>",
	Short: "Trigger a container build",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !containerBuildForce || flagDryRun {
			printContainerBuildPlan(name, containerBuildVersion)
			return nil
		}

		// The generated ContainersAPI.BuildContainerImage requires
		// (image_name, github_repository, ...) — the existing CLI's
		// {name, version} body shape does not map onto that contract,
		// so the manual POST is preserved until the flag surface is
		// reworked. See onboarding follow-up for the swag annotation
		// gap on this endpoint.
		c := newClient()

		body := map[string]string{
			"name": name,
		}
		if containerBuildVersion != "" {
			body["version"] = containerBuildVersion
		}

		var resp client.APIResponse[any]
		if err := c.Post(consts.APIPathContainersBuild, body, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		output.PrintInfo(fmt.Sprintf("Build triggered for container %q", name))
		return nil
	},
}

func printContainerBuildPlan(name, version string) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		payload := map[string]any{
			"dry_run": true,
			"name":    name,
		}
		if version != "" {
			payload["version"] = version
		}
		output.PrintJSON(payload)
		return
	}

	if flagNonInteractive && !containerBuildForce {
		output.PrintInfo("Refusing to build without --force in non-interactive mode")
		return
	}
	fmt.Fprintf(os.Stdout, "Build preview for container %q\n", name)
	if version != "" {
		fmt.Fprintf(os.Stdout, "  version: %s\n", version)
	}
	output.PrintInfo("Run --force to execute the build")
}

func init() {
	containerListCmd.Flags().StringVar(&containerListType, "type", "", "Filter by type: algorithm|benchmark|pedestal")

	containerBuildCmd.Flags().StringVar(&containerBuildVersion, "version", "", "Version tag for the build")
	containerBuildCmd.Flags().BoolVar(&containerBuildForce, "force", false, "Required to actually trigger the build")

	containerDeleteCmd.Flags().BoolVar(&containerDeleteYes, "yes", false, "Skip confirmation prompt")
	containerDeleteCmd.Flags().BoolVar(&containerDeleteYes, "force", false, "Alias for --yes")

	containerCmd.AddCommand(containerListCmd)
	containerCmd.AddCommand(containerGetCmd)
	containerCmd.AddCommand(containerVersionsCmd)
	containerCmd.AddCommand(containerBuildCmd)
	containerCmd.AddCommand(containerDeleteCmd)
	containerCmd.AddCommand(containerResolveCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(containerBuildCmd)
		markDryRunSupported(containerDeleteCmd)
	})

	// `container version` subcommands
	containerVersionSetImageCmd.Flags().IntVar(&setImageID, "id", 0, "Container version ID to update (required)")
	containerVersionSetImageCmd.Flags().StringVar(&setImageRef, "ref", "", "Full image reference <registry>/<namespace>/<repository>:<tag> (required)")
	containerVersionSetImageCmd.Flags().BoolVar(&setImageDryRun, "dry-run", false, "Print the current vs proposed diff without writing")

	containerVersionListVersionsCmd.Flags().StringVar(&containerVersionListVersionsName, "name", "", "Container name (alternatively pass as positional arg)")

	containerVersionDescribeCmd.Flags().StringVar(&containerVersionDescribeFormat, "format", "",
		"Output format: text|json|yaml (default text; falls back to --output when unset)")
	registerOutputFormats(containerVersionDescribeCmd, output.OutputFormat("yaml"))

	containerVersionCmd.AddCommand(containerVersionSetImageCmd)
	containerVersionCmd.AddCommand(containerVersionListVersionsCmd)
	containerVersionCmd.AddCommand(containerVersionDescribeCmd)
	containerCmd.AddCommand(containerVersionCmd)
}
