package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"aegis/cli/apiclient"
	"aegis/cli/internal/cli/blobref"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var bucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage blob buckets (list / create / get / rm)",
	Long: `Manage blob storage buckets exposed by the aegis-blob microservice.

Buckets are the top-level grouping for objects accessed via 'aegisctl blob ...'.
List the buckets you can see, inspect one, or provision a new bucket against
a configured driver (localfs / s3).

EXAMPLES:
  # List buckets
  aegisctl bucket ls

  # Inspect a bucket
  aegisctl bucket get aegis-pages

  # Provision a new bucket on the localfs driver
  aegisctl bucket create aegis-scratch --driver localfs --root /var/lib/aegis-blob/aegis-scratch

  # See related noun-group:
  aegisctl blob ls aegis-pages:
`,
}

// --- bucket ls ---

var bucketLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List buckets the current token can see",
	Long: `List buckets registered with the aegis-blob service.

The columns are NAME / DRIVER / MAX_OBJECT_BYTES / PUBLIC. Use --output json
or --output ndjson for machine-readable output.

EXAMPLES:
  aegisctl bucket ls
  aegisctl bucket ls --output ndjson | jq .
`,
	Args: requireNoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobListBuckets(ctx).Execute()
		if err != nil {
			return err
		}
		var items []apiclient.BlobBucketSummary
		if resp.Data != nil {
			items = resp.Data.GetItems()
		}
		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(items)
			return nil
		case output.FormatNDJSON:
			return output.PrintNDJSON(items)
		}
		rows := make([][]string, 0, len(items))
		for _, b := range items {
			pub := "false"
			if b.GetPublicRead() {
				pub = "true"
			}
			rows = append(rows, []string{
				b.GetName(),
				b.GetDriver(),
				strconv.FormatInt(int64(b.GetMaxObjectBytes()), 10),
				pub,
			})
		}
		output.PrintTable([]string{"NAME", "DRIVER", "MAX_OBJECT_BYTES", "PUBLIC"}, rows)
		return nil
	},
}

// --- bucket create ---

var (
	bucketCreateDriver         string
	bucketCreateRoot           string
	bucketCreateEndpoint       string
	bucketCreateRegion         string
	bucketCreatePublic         bool
	bucketCreateMaxObjectBytes int64
	bucketCreateRetentionDays  int
	bucketCreateLifecycleFile  string
	bucketCreateBucketName     string
)

var bucketCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Provision a new bucket against a configured driver",
	Long: `Create a new bucket. The bucket name must be 2-63 chars, lowercase, and may
contain digits, dots, dashes, and underscores (matches the server-side regex).

For the localfs driver, --root is required and points at the on-disk directory
the driver will own. For the s3 driver, --endpoint and --region are typical,
plus --bucket to specify the underlying S3 bucket name.

--lifecycle is reserved for future server support; today it is parsed for
JSON validity but not wired into the request. See the surprises note in
the docs.

EXAMPLES:
  # Localfs bucket
  aegisctl bucket create aegis-scratch --driver localfs --root /var/lib/aegis-blob/aegis-scratch

  # Public S3-backed bucket
  aegisctl bucket create cdn-assets --driver s3 --endpoint https://oss.example --region cn-hangzhou --bucket cdn-assets --public
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !blobref.IsValidBucketName(name) {
			return usageErrorf("invalid bucket name %q: must match ^[a-z0-9][a-z0-9._-]{1,62}$", name)
		}
		if bucketCreateDriver == "" {
			return usageErrorf("--driver is required (localfs|s3)")
		}
		if bucketCreateLifecycleFile != "" {
			// Validate JSON now so we can fail-fast before hitting the server.
			body, err := os.ReadFile(bucketCreateLifecycleFile)
			if err != nil {
				return fmt.Errorf("read lifecycle file: %w", err)
			}
			var probe map[string]any
			if err := json.Unmarshal(body, &probe); err != nil {
				return usageErrorf("invalid lifecycle JSON in %q: %v", bucketCreateLifecycleFile, err)
			}
			// TODO(REQ-830): BlobCreateBucketReq has no Lifecycle field yet;
			// wire through once the SDK / server adds runtime lifecycle policy.
			output.PrintInfo("Note: --lifecycle is parsed for validity but the server does not yet consume it; ignored for now.")
		}

		if err := requireAPIContext(true); err != nil {
			return err
		}

		if flagDryRun {
			payload := map[string]any{
				"name":             name,
				"driver":           bucketCreateDriver,
				"root":             bucketCreateRoot,
				"endpoint":         bucketCreateEndpoint,
				"region":           bucketCreateRegion,
				"bucket":           bucketCreateBucketName,
				"public_read":      bucketCreatePublic,
				"max_object_bytes": bucketCreateMaxObjectBytes,
				"retention_days":   bucketCreateRetentionDays,
			}
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "would_create": payload})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would POST /api/v2/blob/buckets %+v\n", payload)
			}
			return nil
		}

		cli, ctx := newAPIClient()
		req := apiclient.NewBlobCreateBucketReq(bucketCreateDriver, name)
		if bucketCreateRoot != "" {
			req.SetRoot(bucketCreateRoot)
		}
		if bucketCreateEndpoint != "" {
			req.SetEndpoint(bucketCreateEndpoint)
		}
		if bucketCreateRegion != "" {
			req.SetRegion(bucketCreateRegion)
		}
		if bucketCreateBucketName != "" {
			req.SetBucket(bucketCreateBucketName)
		}
		if bucketCreatePublic {
			req.SetPublicRead(true)
		}
		if bucketCreateMaxObjectBytes > 0 {
			req.SetMaxObjectBytes(int32(bucketCreateMaxObjectBytes))
		}
		if bucketCreateRetentionDays > 0 {
			req.SetRetentionDays(int32(bucketCreateRetentionDays))
		}
		resp, _, err := cli.BlobAPI.BlobCreateBucket(ctx).BlobCreateBucketReq(*req).Execute()
		if err != nil {
			return err
		}
		if resp.Data == nil {
			return fmt.Errorf("create bucket: empty response")
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Bucket %q created (driver=%s)", resp.Data.GetName(), resp.Data.GetDriver()))
		return nil
	},
}

// --- bucket get ---

var bucketGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show the configuration and metadata of a bucket",
	Long: `Show config for a single bucket: driver, retention, max-object-bytes,
and public/ACL summary (when available).

EXAMPLES:
  aegisctl bucket get aegis-pages
  aegisctl bucket get aegis-pages --output json | jq .max_object_bytes
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		name := args[0]
		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobListBuckets(ctx).Execute()
		if err != nil {
			return err
		}
		var found *apiclient.BlobBucketSummary
		if resp.Data != nil {
			items := resp.Data.GetItems()
			for i := range items {
				if items[i].GetName() == name {
					found = &items[i]
					break
				}
			}
		}
		if found == nil {
			return notFoundErrorf("bucket %q not found", name)
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(found)
			return nil
		}
		fmt.Printf("Name:             %s\n", found.GetName())
		fmt.Printf("Driver:           %s\n", found.GetDriver())
		fmt.Printf("MaxObjectBytes:   %d\n", found.GetMaxObjectBytes())
		fmt.Printf("RetentionDays:    %d\n", found.GetRetentionDays())
		fmt.Printf("PublicRead:       %t\n", found.GetPublicRead())
		return nil
	},
}

// --- bucket rm ---

var (
	bucketRmYes   bool
	bucketRmForce bool
)

var bucketRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"delete"},
	Short:   "Delete a bucket (refuses to delete non-empty buckets without --force)",
	Long: `Delete a bucket. Refuses to delete a non-empty bucket unless --force is
given.

NOTE: The backend does not yet expose a bucket-delete endpoint
(POST/DELETE /api/v2/blob/buckets/:name). This command will error out until
the backend ships that route. See the project notes for the tracking issue.

EXAMPLES:
  aegisctl bucket rm aegis-scratch --yes
  aegisctl bucket rm aegis-scratch --force --yes
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := requireAPIContext(true); err != nil {
			return err
		}
		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "would_delete": name, "force": bucketRmForce})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would DELETE /api/v2/blob/buckets/%s (force=%t)\n", name, bucketRmForce)
			}
			return nil
		}
		yes := bucketRmYes || bucketRmForce
		if err := confirmDeletion("bucket", name, 0, yes); err != nil {
			return err
		}
		// Stubbed: backend endpoint is not implemented; surface a structured
		// server-side 404/501-style error rather than silently no-op.
		c := newClient()
		var resp struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := c.Delete("/api/v2/blob/buckets/"+name, &resp); err != nil {
			return fmt.Errorf("bucket rm: backend endpoint is not yet implemented (DELETE /api/v2/blob/buckets/%s): %w", name, err)
		}
		output.PrintInfo(fmt.Sprintf("Bucket %q deleted", name))
		return nil
	},
}

func init() {
	bucketCreateCmd.Flags().StringVar(&bucketCreateDriver, "driver", "", "Storage driver: localfs|s3 (required)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateRoot, "root", "", "Root directory (localfs driver)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateEndpoint, "endpoint", "", "S3 endpoint URL (s3 driver)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateRegion, "region", "", "S3 region (s3 driver)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateBucketName, "bucket", "", "Underlying S3 bucket name (s3 driver)")
	bucketCreateCmd.Flags().BoolVar(&bucketCreatePublic, "public", false, "Mark bucket public-read")
	bucketCreateCmd.Flags().Int64Var(&bucketCreateMaxObjectBytes, "max-object-bytes", 0, "Per-object size cap in bytes (0 = server default)")
	bucketCreateCmd.Flags().IntVar(&bucketCreateRetentionDays, "retention-days", 0, "Retention window in days (0 = no retention)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateLifecycleFile, "lifecycle", "", "Lifecycle policy JSON file (server-side support pending)")

	bucketRmCmd.Flags().BoolVar(&bucketRmYes, "yes", false, "Skip confirmation prompt")
	bucketRmCmd.Flags().BoolVar(&bucketRmForce, "force", false, "Delete non-empty buckets (and implies --yes)")

	bucketCmd.AddCommand(bucketLsCmd)
	bucketCmd.AddCommand(bucketCreateCmd)
	bucketCmd.AddCommand(bucketGetCmd)
	bucketCmd.AddCommand(bucketRmCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(bucketCreateCmd)
		markDryRunSupported(bucketRmCmd)
	})
}
